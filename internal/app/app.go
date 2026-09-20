package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"post-service/internal/adapters/postgresql"
	"post-service/internal/adapters/redis"
	"post-service/internal/config"
	"post-service/internal/security/jwt"
	httptransport "post-service/internal/transport/http"
	"post-service/internal/usecase"
	"time"

	"syscall"
)

func Run(ctx context.Context, config *config.Config, logger *slog.Logger) error {
	const op string = "internal/app/Run"
	logger.Info(fmt.Sprintf("Starting %s", op))
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

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
	postCache := redis.NewCache(rdb)
	logger.Debug("Repo and Cache initialized")

	logger.Debug("Creating Usecase...")
	uc := usecase.NewUsecase(postRepo, postCache, logger)
	logger.Debug("Usecase initialized")

	logger.Debug("Fetching JWK...")
	fetcher := jwt.NewJWKSFetcher(logger)
	fetchCtx, cancelFetch := context.WithTimeout(ctx, 10*time.Second)
	publicKey, kid, err := fetcher.Fetch(fetchCtx, config.JwksURL)
	cancelFetch()
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	verifier := jwt.NewVerifier(publicKey, kid, logger)
	logger.Debug("JWK was fetched,verifier was created successfully")

	server := &http.Server{
		Addr:              config.HTTPAddress,
		Handler:           httptransport.NewRouter(uc, verifier, logger),
		ReadTimeout:       config.ReadTimeout,
		WriteTimeout:      config.WriteTimeout,
		IdleTimeout:       config.IdleTimeout,
		ReadHeaderTimeout: config.ReadHeaderTimeout,
	}
	return serveHTTP(ctx, server, logger)
}

func serveHTTP(ctx context.Context, server *http.Server, logger *slog.Logger) error {
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return fmt.Errorf("listen HTTP: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	logger.Info("HTTP server started", "address", listener.Addr().String())
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		logger.Info("Shutting down HTTP server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			<-done
			return fmt.Errorf("shutdown HTTP: %w", err)
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
