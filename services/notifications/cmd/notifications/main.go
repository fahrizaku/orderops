// Command notifications is the OrderOps notifications service: it consumes
// order events from RabbitMQ, writes them to a MongoDB audit log, and
// serves the log over HTTP with Redis-backed caching and rate limiting.
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

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	"github.com/fahrizaku/orderops/services/notifications/internal/audit"
	"github.com/fahrizaku/orderops/services/notifications/internal/config"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.Load()
	ctx := context.Background()

	// MongoDB holds the audit log; it is this service's core dependency, so
	// startup fails fast when it is unreachable.
	mongoCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	mongoClient, err := mongo.Connect(mongoCtx, options.Client().ApplyURI(cfg.MongoURL))
	if err != nil {
		log.Error("connect to mongodb", "err", err)
		os.Exit(1)
	}
	if err := mongoClient.Ping(mongoCtx, readpref.Primary()); err != nil {
		log.Error("ping mongodb", "err", err)
		os.Exit(1)
	}
	cancel()
	defer func() {
		dctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = mongoClient.Disconnect(dctx)
	}()

	store := audit.NewMongoStore(mongoClient, cfg.MongoDB)
	if err := store.EnsureIndexes(ctx); err != nil {
		log.Error("ensure audit indexes", "err", err)
		os.Exit(1)
	}

	// Redis backs the read cache and the rate limiter. The service still
	// works without it (cache misses, limiter fails open), so a failed
	// ping is a warning, not an exit.
	redisOpts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		log.Error("parse redis url", "err", err)
		os.Exit(1)
	}
	redisClient := redis.NewClient(redisOpts)
	defer redisClient.Close()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Warn("redis not reachable at startup; cache and rate limiting will fail open", "err", err)
	}

	cache := audit.NewRedisCache(redisClient)
	limiter := audit.NewRedisLimiter(redisClient, int64(cfg.RateLimitPerMin))
	svc := audit.NewService(store, cache, cfg.CacheTTL, log)
	handler := audit.NewHandler(svc, limiter, log, store, cache)

	// The consumer dials in a retry loop, so the service starts even while
	// RabbitMQ is still coming up.
	consumer := audit.NewConsumer(cfg.RabbitMQURL, audit.NewEventHandler(store, log), log)
	consumeCtx, stopConsume := context.WithCancel(context.Background())
	defer stopConsume()
	go consumer.Run(consumeCtx)

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Info("notifications service listening", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server failed", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	stopConsume()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "err", err)
	}
	log.Info("notifications service stopped")
}
