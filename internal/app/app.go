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
	"post-service/internal/security/jwt"

	"syscall"
)

func Run(ctx context.Context, config *config.Config, logger *slog.Logger) error {
	const op string = "internal/app/Run"
	logger.Info(fmt.Sprintf("Starting %s", op))

	logger.Debug("Initialize database...")
	pgsql, err := postgresql.New(ctx, config.DatabaseURL, logger)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	defer pgsql.Close()
	logger.Debug("Database initialized")

	logger.Debug("Initialize cache storage...")
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
	logger.Debug("Cache initialized")

	logger.Debug("Creating Repo and Cache objects...")
	postRepo := postgresql.NewPostRepo(pgsql)
	postCache := redis.NewPostCache(rdb)
	logger.Debug("Repo and Cache initialized")

	fetcher := jwt.NewJWKSFetcher(logger)
	logger.Debug("Fetching JWK...")
	publicKey, kid, err := fetcher.Fetch(ctx, config.JwksURL)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	logger.Debug("JWK was fetched successfully")

	verifier := jwt.NewVerifier(publicKey, kid, logger)

	logger.Info("Server Started")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	logger.Warn("Interrupt received, shutting down...")

	return nil
}
