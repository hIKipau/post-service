package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"post-service/internal/adapters/postgresql"
	"post-service/internal/adapters/redis"
	"post-service/internal/config"
	"syscall"
)

func Run(ctx context.Context, config *config.Config, logger *slog.Logger) error {
	const op string = "internal/app/Run"

	pgsql, err := postgresql.New(ctx, config.DatabaseURL, logger)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	pgsql.Close()

	rdb, err := redis.New(ctx, config.RedisURL, logger)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	defer func() {
		err := rdb.Close()
		if err != nil {
			logger.Warn("Failed to close Redis connection, idk why")
		}
	}()

	logger.Info("Server Started")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	logger.Warn("Interrupt received, shutting down...")

	return nil
}
