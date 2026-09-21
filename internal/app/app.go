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
	"syscall"
	"time"

	"post-service/internal/adapters/postgresql"
	"post-service/internal/adapters/redis"
	"post-service/internal/config"
	"post-service/internal/reactionsync"
	"post-service/internal/security/jwt"
	httptransport "post-service/internal/transport/http"
	"post-service/internal/usecase"
)

// Run initializes storage and authentication, connects the HTTP components and serves until shutdown.
func Run(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	if cfg == nil {
		return errors.New("application configuration is required")
	}
	if cfg.StartupTimeout <= 0 || cfg.ShutdownTimeout <= 0 {
		return errors.New("startup and shutdown timeouts must be positive")
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("Starting post service")
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	startupCtx, cancelStartup := context.WithTimeout(ctx, cfg.StartupTimeout)
	defer cancelStartup()

	logger.Debug("Initialize database...")
	pgsql, err := postgresql.New(startupCtx, cfg.DatabaseURL, logger)
	if err != nil {
		return fmt.Errorf("initialize PostgreSQL: %w", err)
	}
	defer pgsql.Close()
	logger.Debug("Database initialized")

	logger.Debug("Initialize cache storage...")
	rdb, err := redis.New(startupCtx, cfg.RedisURL, logger)
	if err != nil {
		return fmt.Errorf("initialize Redis: %w", err)
	}
	defer func() {
		err := rdb.Close()
		if err != nil {
			logger.Warn("Failed to close Redis connection", "error", err)
		}
	}()
	logger.Debug("Cache initialized")

	logger.Debug("Creating Repo and Cache objects...")
	postRepo := postgresql.NewRepo(pgsql)
	postCache := redis.NewCache(rdb)
	if err := postCache.CheckReactionMaintenance(startupCtx); err != nil {
		return err
	}
	if err := postRepo.CheckReactionSchema(startupCtx); err != nil {
		return err
	}
	if err := postCache.EnsureReactionGroup(startupCtx); err != nil {
		return fmt.Errorf("initialize reaction stream: %w", err)
	}
	logger.Debug("Repo and Cache initialized")

	logger.Debug("Creating Usecase...")
	uc := usecase.NewUsecase(postRepo, postCache, logger)
	logger.Debug("Usecase initialized")

	logger.Debug("Fetching JWK...")
	fetcher := jwt.NewJWKSFetcher(logger)
	publicKey, kid, err := fetcher.Fetch(startupCtx, cfg.JwksURL)
	if err != nil {
		return fmt.Errorf("initialize JWT verifier: %w", err)
	}
	cancelStartup()
	verifier := jwt.NewVerifier(publicKey, kid, logger)
	logger.Debug("JWT verifier initialized")

	// Keep synchronization alive while HTTP requests drain; stop it before closing either storage.
	workerCtx, stopWorker := context.WithCancel(context.WithoutCancel(ctx))
	worker := reactionsync.New(func(ctx context.Context) (reactionsync.Session, error) {
		return postRepo.AcquireReactionSession(ctx)
	}, postCache, logger)
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); worker.Run(workerCtx) }()
	defer func() { stopWorker(); <-workerDone }()

	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           httptransport.NewRouter(uc, verifier, logger),
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
	return serveHTTP(ctx, server, logger, cfg.ShutdownTimeout)
}

// serveHTTP binds the configured address and manages the HTTP server's lifetime.
func serveHTTP(ctx context.Context, server *http.Server, logger *slog.Logger, shutdownTimeout time.Duration) error {
	if ctx.Err() != nil {
		return nil
	}
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", server.Addr)
	if err != nil {
		return fmt.Errorf("listen HTTP: %w", err)
	}
	return serveListener(ctx, server, listener, logger, shutdownTimeout)
}

// serveListener drains active requests on cancellation and closes connections if the drain times out.
func serveListener(ctx context.Context, server *http.Server, listener net.Listener, logger *slog.Logger, shutdownTimeout time.Duration) error {
	defer listener.Close()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	logger.Info("HTTP server started", "address", listener.Addr().String())
	var serveErr error
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			serveErr = fmt.Errorf("serve HTTP: %w", err)
		}
		// Serve can fail while handlers are still using storage. Drain them
		// before Run releases the database and Redis connections.
		done = nil
	case <-ctx.Done():
	}

	logger.Info("Shutting down HTTP server", "timeout", shutdownTimeout)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		logger.Warn("Graceful HTTP shutdown failed; closing connections", "error", shutdownErr)
		closeErr := server.Close()
		shutdownErr = fmt.Errorf("shutdown HTTP: %w", errors.Join(shutdownErr, closeErr))
	}
	if done != nil {
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			serveErr = fmt.Errorf("serve HTTP: %w", err)
		}
	}
	if err := errors.Join(serveErr, shutdownErr); err != nil {
		return err
	}
	logger.Info("HTTP server stopped")
	return nil
}
