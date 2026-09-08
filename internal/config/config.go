// Package config reads process configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config is everything the server needs at start-up.
type Config struct {
	DatabaseURL     string
	HTTPAddr        string
	Seed            bool
	LogLevel        string
	ServiceName     string
	OTLPEndpoint    string // empty disables trace export
	ShutdownTimeout time.Duration
}

// FromEnv builds a Config; DATABASE_URL is mandatory.
func FromEnv() (Config, error) {
	c := Config{
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		HTTPAddr:        envOr("HTTP_ADDR", ":8080"),
		LogLevel:        envOr("LOG_LEVEL", "info"),
		ServiceName:     envOr("OTEL_SERVICE_NAME", "service-scheduler"),
		OTLPEndpoint:    os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		ShutdownTimeout: 15 * time.Second,
	}
	if c.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if v := os.Getenv("APP_SEED"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("APP_SEED must be a boolean, got %q", v)
		}
		c.Seed = b
	}
	return c, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
