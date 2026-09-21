// Package migrator runs embedded Goose migrations independently of the HTTP service.
package migrator

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"post-service/migrations"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// New opens PostgreSQL and creates a Goose provider with embedded SQL and a migration lock.
// The caller must close the returned provider to release its database connections.
func New(ctx context.Context, databaseURL string) (*goose.Provider, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		// Parsing errors may include the original connection string and its credentials.
		return nil, errors.New("invalid DATABASE_URL")
	}
	db := stdlib.OpenDB(*config)
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect to migration database: %w", err)
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create migration lock: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.Files,
		goose.WithSessionLocker(locker),
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create migration provider: %w", err)
	}
	return provider, nil
}
