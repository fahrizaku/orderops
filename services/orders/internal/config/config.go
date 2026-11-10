// Package config loads the orders service configuration from environment
// variables, with defaults that match docker-compose for local development.
package config

import "os"

type Config struct {
	Port        string
	DatabaseURL string
	RabbitMQURL string
	JWTSecret   string
}

func Load() Config {
	return Config{
		Port:        env("PORT", "8082"),
		DatabaseURL: env("DATABASE_URL", "postgres://orderops:orderops@localhost:5432/orderops_orders?sslmode=disable"),
		RabbitMQURL: env("RABBITMQ_URL", "amqp://orderops:orderops@localhost:5672/"),
		JWTSecret:   env("JWT_SECRET", "dev-secret-change-me"),
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
