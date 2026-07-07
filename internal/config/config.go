package config

import (
	"fmt"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
	"github.com/joho/godotenv"
)

type Config struct {
	Env         string `env:"ENV" env-required:"true"`
	DatabaseURL string `env:"DATABASE_URL" env-required:"true"`

	HTTPAddress       string        `env:"HTTP_ADDRESS" env-required:"true"`
	ReadTimeout       time.Duration `env:"HTTP_READ_TIMEOUT" env-default:"5s"`
	WriteTimeout      time.Duration `env:"HTTP_WRITE_TIMEOUT" env-default:"10s"`
	IdleTimeout       time.Duration `env:"HTTP_IDLE_TIMEOUT" env-default:"60s"`
	ReadHeaderTimeout time.Duration `env:"HTTP_READ_HEADER_TIMEOUT" env-default:"5s"`
}

func MustLoad() (*Config, error) {

	err := godotenv.Load(".env.local")
	if err != nil {
		return nil, fmt.Errorf("failed to load .env: %w", err)
	}

	var cfg Config
	err = cleanenv.ReadEnv(&cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to read .env: %w", err)
	}

	return &cfg, nil
}
