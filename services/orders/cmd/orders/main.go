// Command orders is the OrderOps orders service: it owns orders and their
// line items in its own database (orderops_orders) and publishes
// order.created events to RabbitMQ through a transactional outbox.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fahrizaku/orderops/services/orders/internal/authn"
	"github.com/fahrizaku/orderops/services/orders/internal/config"
	"github.com/fahrizaku/orderops/services/orders/internal/order"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.Load()
	if cfg.JWTSecret == "dev-secret-change-me" {
		log.Warn("JWT_SECRET uses the development default - set a real secret outside local dev")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("connect to postgres", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := order.Migrate(ctx, pool); err != nil {
		log.Error("run migrations", "err", err)
		os.Exit(1)
	}

	repo := order.NewPostgresRepository(pool)

	// The publisher dials lazily, so the service starts even while RabbitMQ
	// is still coming up; the outbox relay retries until events are sent.
	publisher := order.NewAMQPPublisher(cfg.RabbitMQURL)
	defer publisher.Close()
	relayCtx, stopRelay := context.WithCancel(context.Background())
	defer stopRelay()
	relay := order.NewRelay(repo, publisher, log, 2*time.Second)
	go relay.Run(relayCtx)

	svc := order.NewService(repo, log)
	handler := order.NewHandler(svc, authn.NewVerifier(cfg.JWTSecret), log, pool)

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Info("orders service listening", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server failed", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	stopRelay()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "err", err)
	}
	log.Info("orders service stopped")
}
