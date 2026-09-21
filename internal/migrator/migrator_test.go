package migrator

import (
	"context"
	"errors"
	"io"
	"testing"

	"post-service/migrations"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// TestNewRejectsInvalidConfiguration verifies validation without exposing the connection string.
func TestNewRejectsInvalidConfiguration(t *testing.T) {
	for _, input := range []struct{ url, message string }{
		{"", "DATABASE_URL is required"},
		{" \t", "DATABASE_URL is required"},
		{"postgres://user:secret@localhost:badport/db", "invalid DATABASE_URL"},
	} {
		provider, err := New(context.Background(), input.url)
		if provider != nil {
			_ = provider.Close()
			t.Fatal("unexpected provider")
		}
		if err == nil || err.Error() != input.message {
			t.Fatalf("configuration error = %v, want %s", err, input.message)
		}
	}
}

// TestNewChecksConnection verifies that a cancelled ping cannot produce a usable migrator.
func TestNewChecksConnection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	provider, err := New(ctx, "postgres://test:test@127.0.0.1:1/test?sslmode=disable")
	if provider != nil {
		_ = provider.Close()
		t.Fatal("unexpected provider")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("connection error = %v", err)
	}
}

// TestRunRejectsUnknownCommand verifies command validation happens before connecting to PostgreSQL.
func TestRunRejectsUnknownCommand(t *testing.T) {
	if err := Run(context.Background(), "", "reset", io.Discard); err == nil || err.Error() == "DATABASE_URL is required" {
		t.Fatalf("command error = %v", err)
	}
}

// TestEmbeddedMigrations verifies that Goose discovers the bundled, ordered migration files.
func TestEmbeddedMigrations(t *testing.T) {
	config, err := pgx.ParseConfig("postgres://test:test@127.0.0.1:1/test?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDB(*config)
	defer db.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.Files, goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	sources := provider.ListSources()
	if len(sources) != 2 || sources[0].Version != 1 || sources[1].Version != 2 {
		t.Fatalf("unexpected sources: %+v", sources)
	}
}
