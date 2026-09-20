package config

import (
	"testing"
	"time"
)

// TestLoadWithoutDotEnv verifies environment-only startup and lifecycle timeout validation.
func TestLoadWithoutDotEnv(t *testing.T) {
	t.Chdir(t.TempDir())
	for key, value := range map[string]string{
		"ENV": "test", "DATABASE_URL": "postgres://localhost/test", "REDIS_URL": "redis://localhost:6379",
		"JWKS_URL": "http://localhost/jwks", "HTTP_ADDRESS": "127.0.0.1:8080",
		"STARTUP_TIMEOUT": "12s", "HTTP_SHUTDOWN_TIMEOUT": "3s",
		"HTTP_READ_TIMEOUT": "5s", "HTTP_WRITE_TIMEOUT": "10s", "HTTP_IDLE_TIMEOUT": "60s", "HTTP_READ_HEADER_TIMEOUT": "5s",
	} {
		t.Setenv(key, value)
	}
	cfg, err := MustLoad()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StartupTimeout != 12*time.Second || cfg.ShutdownTimeout != 3*time.Second {
		t.Fatalf("unexpected timeouts: %+v", cfg)
	}
	for _, key := range []string{"STARTUP_TIMEOUT", "HTTP_SHUTDOWN_TIMEOUT"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "0s")
			if _, err := MustLoad(); err == nil {
				t.Fatal("expected non-positive timeout to be rejected")
			}
		})
	}
}
