package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Environment       string
	HTTPAddress       string
	DatabaseURL       string
	APIToken          string
	LogLevel          string
	LogFormat         string
	WorkerConcurrency int
	DeliveryTimeout   time.Duration
	PollInterval      time.Duration
	LeaseDuration     time.Duration
	MaxAttempts       int
	BaseBackoff       time.Duration
	MaxBackoff        time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		Environment:       envOrDefault("RC_ENV", "development"),
		HTTPAddress:       envOrDefault("RC_HTTP_ADDR", ":8080"),
		DatabaseURL:       envOrDefault("RC_DATABASE_URL", "postgres://rc:rc@localhost:5432/rc?sslmode=disable"),
		APIToken:          os.Getenv("RC_API_TOKEN"),
		LogLevel:          envOrDefault("RC_LOG_LEVEL", "info"),
		LogFormat:         envOrDefault("RC_LOG_FORMAT", "json"),
		WorkerConcurrency: 4,
		DeliveryTimeout:   5 * time.Second,
		PollInterval:      500 * time.Millisecond,
		LeaseDuration:     30 * time.Second,
		MaxAttempts:       10,
		BaseBackoff:       5 * time.Second,
		MaxBackoff:        15 * time.Minute,
	}

	var err error
	if cfg.WorkerConcurrency, err = intFromEnv("RC_WORKER_CONCURRENCY", cfg.WorkerConcurrency); err != nil {
		return Config{}, err
	}
	if cfg.DeliveryTimeout, err = durationFromEnv("RC_DELIVERY_TIMEOUT", cfg.DeliveryTimeout); err != nil {
		return Config{}, err
	}
	if cfg.PollInterval, err = durationFromEnv("RC_POLL_INTERVAL", cfg.PollInterval); err != nil {
		return Config{}, err
	}
	if cfg.LeaseDuration, err = durationFromEnv("RC_LEASE_DURATION", cfg.LeaseDuration); err != nil {
		return Config{}, err
	}
	if cfg.MaxAttempts, err = intFromEnv("RC_MAX_ATTEMPTS", cfg.MaxAttempts); err != nil {
		return Config{}, err
	}
	if cfg.BaseBackoff, err = durationFromEnv("RC_BASE_BACKOFF", cfg.BaseBackoff); err != nil {
		return Config{}, err
	}
	if cfg.MaxBackoff, err = durationFromEnv("RC_MAX_BACKOFF", cfg.MaxBackoff); err != nil {
		return Config{}, err
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.HTTPAddress == "" {
		return errors.New("RC_HTTP_ADDR must not be empty")
	}
	if c.DatabaseURL == "" {
		return errors.New("RC_DATABASE_URL must not be empty")
	}
	if c.LogFormat != "json" && c.LogFormat != "console" {
		return fmt.Errorf("RC_LOG_FORMAT must be json or console, got %q", c.LogFormat)
	}
	if c.WorkerConcurrency <= 0 {
		return errors.New("RC_WORKER_CONCURRENCY must be greater than zero")
	}
	if c.DeliveryTimeout <= 0 || c.PollInterval <= 0 || c.LeaseDuration <= 0 {
		return errors.New("duration settings must be greater than zero")
	}
	if c.LeaseDuration <= c.DeliveryTimeout {
		return errors.New("RC_LEASE_DURATION must be greater than RC_DELIVERY_TIMEOUT")
	}
	if c.MaxAttempts <= 0 {
		return errors.New("RC_MAX_ATTEMPTS must be greater than zero")
	}
	if c.BaseBackoff <= 0 || c.MaxBackoff < c.BaseBackoff {
		return errors.New("backoff settings are invalid")
	}
	return nil
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func intFromEnv(key string, fallback int) (int, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}

func durationFromEnv(key string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}
