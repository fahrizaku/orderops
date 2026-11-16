// Package config loads the notifications service configuration from
// environment variables, with defaults that match docker-compose for
// local development.
package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port            string
	RabbitMQURL     string
	MongoURL        string
	MongoDB         string
	RedisURL        string
	CacheTTL        time.Duration
	RateLimitPerMin int
}

func Load() Config {
	return Config{
		Port:            env("PORT", "8083"),
		RabbitMQURL:     env("RABBITMQ_URL", "amqp://orderops:orderops@localhost:5672/"),
		MongoURL:        env("MONGO_URL", "mongodb://orderops:orderops@localhost:27017"),
		MongoDB:         env("MONGO_DB", "orderops_notifications"),
		RedisURL:        env("REDIS_URL", "redis://localhost:6379"),
		CacheTTL:        time.Duration(envInt("CACHE_TTL_SECONDS", 30)) * time.Second,
		RateLimitPerMin: envInt("RATE_LIMIT_PER_MINUTE", 120),
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v > 0 {
		return v
	}
	return fallback
}
