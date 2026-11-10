// Command auth is the OrderOps auth service: user registration, login and
// JWT issuing for the order platform. One service, one database
// (orderops_auth), per the platform's database-per-service layout.
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

	"github.com/fahrizaku/orderops/services/auth/internal/config"
	"github.com/fahrizaku/orderops/services/auth/internal/user"
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

	if err := user.Migrate(ctx, pool); err != nil {
		log.Error("run migrations", "err", err)
		os.Exit(1)
	}

	repo := user.NewPostgresRepository(pool)
	tokens := user.NewTokenManager(cfg.JWTSecret, cfg.JWTExpiry)
	svc := user.NewService(repo, tokens, log)
	handler := user.NewHandler(svc, log, pool)

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Info("auth service listening", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server failed", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "err", err)
	}
	log.Info("auth service stopped")
}
