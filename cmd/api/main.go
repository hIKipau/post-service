package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"post-service/internal/app"
	"post-service/internal/config"
)

func main() {
	cfg, err := config.MustLoad()
	if err != nil {
		log.Fatal(err)
	}

	logger := NewLogger(cfg.Env)

	err = app.Run(context.Background(), cfg, logger)
	if err != nil {
		log.Fatal(err)
	}

}

func NewLogger(env string) *slog.Logger {
	var level slog.Level

	switch env {
	case "local", "dev":
		level = slog.LevelDebug
	default:
		level = slog.LevelInfo
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	return logger
}
