// Package config loads the auth service configuration from environment
// variables, with defaults that match docker-compose for local development.
package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port        string
	DatabaseURL string
	JWTSecret   string
	JWTExpiry   time.Duration
}

func Load() Config {
	hours := 24
	if v := os.Getenv("JWT_EXPIRY_HOURS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			hours = n
		}
	}
	return Config{
		Port:        env("PORT", "8081"),
		DatabaseURL: env("DATABASE_URL", "postgres://orderops:orderops@localhost:5432/orderops_auth?sslmode=disable"),
		JWTSecret:   env("JWT_SECRET", "dev-secret-change-me"),
		JWTExpiry:   time.Duration(hours) * time.Hour,
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
