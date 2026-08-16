package config

import (
	"testing"
	"time"
)

func TestLoadUsesEnvironmentOverrides(t *testing.T) {
	t.Setenv("RC_HTTP_ADDR", ":9090")
	t.Setenv("RC_WORKER_CONCURRENCY", "8")
	t.Setenv("RC_DELIVERY_TIMEOUT", "2s")
	t.Setenv("RC_LEASE_DURATION", "10s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPAddress != ":9090" {
		t.Fatalf("HTTPAddress = %q, want %q", cfg.HTTPAddress, ":9090")
	}
	if cfg.WorkerConcurrency != 8 {
		t.Fatalf("WorkerConcurrency = %d, want 8", cfg.WorkerConcurrency)
	}
	if cfg.DeliveryTimeout != 2*time.Second {
		t.Fatalf("DeliveryTimeout = %s, want 2s", cfg.DeliveryTimeout)
	}
}

func TestValidateRejectsLeaseShorterThanDeliveryTimeout(t *testing.T) {
	cfg := Config{
		HTTPAddress:       ":8080",
		DatabaseURL:       "postgres://example",
		LogFormat:         "json",
		WorkerConcurrency: 1,
		DeliveryTimeout:   5 * time.Second,
		PollInterval:      time.Second,
		LeaseDuration:     5 * time.Second,
		MaxAttempts:       1,
		BaseBackoff:       time.Second,
		MaxBackoff:        time.Minute,
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want lease validation error")
	}
}

func TestValidateServerSecrets(t *testing.T) {
	cfg := Config{}
	if err := cfg.ValidateServerSecrets(); err == nil {
		t.Fatal("ValidateServerSecrets() error = nil, want missing token error")
	}
	cfg.APIToken = "internal-token"
	if err := cfg.ValidateServerSecrets(); err == nil {
		t.Fatal("ValidateServerSecrets() error = nil, want missing encryption key error")
	}
	cfg.HeaderEncryptionKey = "configured"
	if err := cfg.ValidateServerSecrets(); err != nil {
		t.Fatalf("ValidateServerSecrets() error = %v", err)
	}
}

func TestAllowHTTPDelivery(t *testing.T) {
	if !(Config{Environment: "development"}).AllowHTTPDelivery() {
		t.Fatal("development should allow HTTP delivery")
	}
	if (Config{Environment: "production"}).AllowHTTPDelivery() {
		t.Fatal("production should require HTTPS delivery")
	}
}
