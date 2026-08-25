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
	"post-service/internal/usecase"

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
	postRepo := postgresql.NewRepo(pgsql)
	defer postRepo.Close()
	postCache := redis.NewCache(rdb)
	defer postCache.Close()
	logger.Debug("Repo and Cache initialized")

	logger.Debug("Creating Usecase...")
	uc := usecase.NewUsecase(postRepo, postCache, logger)
	logger.Debug("Usecase initialized")

	logger.Debug("Fetching JWK...")
	fetcher := jwt.NewJWKSFetcher(logger)
	publicKey, kid, err := fetcher.Fetch(ctx, config.JwksURL)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	verifier := jwt.NewVerifier(publicKey, kid, logger)
	logger.Debug("JWK was fetched,verifier was created successfully")

	logger.Info("Server Started")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	logger.Warn("Interrupt received, shutting down...")

	return nil
}
