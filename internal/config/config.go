package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
	"github.com/joho/godotenv"
)

type Config struct {
	Env               string        `env:"ENV" env-required:"true"`
	DatabaseURL       string        `env:"DATABASE_URL" env-required:"true"`
	RedisURL          string        `env:"REDIS_URL" env-required:"true"`
	JwksURL           string        `env:"JWKS_URL" env-required:"true"`
	HTTPAddress       string        `env:"HTTP_ADDRESS" env-required:"true"`
	ReadTimeout       time.Duration `env:"HTTP_READ_TIMEOUT" env-default:"5s"`
	WriteTimeout      time.Duration `env:"HTTP_WRITE_TIMEOUT" env-default:"10s"`
	IdleTimeout       time.Duration `env:"HTTP_IDLE_TIMEOUT" env-default:"60s"`
	ReadHeaderTimeout time.Duration `env:"HTTP_READ_HEADER_TIMEOUT" env-default:"5s"`
	StartupTimeout    time.Duration `env:"STARTUP_TIMEOUT" env-default:"10s"`
	ShutdownTimeout   time.Duration `env:"HTTP_SHUTDOWN_TIMEOUT" env-default:"10s"`
}

// MustLoad reads an optional .env file and loads settings from the environment.
func MustLoad() (*Config, error) {

	err := godotenv.Load(".env")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("failed to load .env: %w", err)
	}

	var cfg Config
	err = cleanenv.ReadEnv(&cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to read environment configuration: %w", err)
	}
	if cfg.StartupTimeout <= 0 || cfg.ShutdownTimeout <= 0 {
		return nil, fmt.Errorf("startup and shutdown timeouts must be positive")
	}

	return &cfg, nil
}
