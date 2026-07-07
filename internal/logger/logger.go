package logger

import (
	"log/slog"
	"os"
)

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
