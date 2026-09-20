package postgresql

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgreSQL struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// New creates a pool and verifies connectivity, closing the pool if the initial ping fails.
func New(ctx context.Context, databaseUrl string, logger *slog.Logger) (*PostgreSQL, error) {
	logger.Info("Connecting to PostgreSQL...")

	config, err := pgxpool.ParseConfig(databaseUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database url: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to PostgreSQL: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping PostgreSQL: %w", err)
	}

	logger.Info("Successfully connected to PostgreSQL")
	return &PostgreSQL{pool: pool, logger: logger}, nil
}

func (pgsql *PostgreSQL) Close() {
	pgsql.pool.Close()
}
