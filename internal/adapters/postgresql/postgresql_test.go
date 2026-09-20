package postgresql

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
)

// TestNewChecksConnection verifies that creating a lazy pool alone does not count as a successful startup.
func TestNewChecksConnection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	db, err := New(ctx, "postgres://test:test@127.0.0.1:1/test?sslmode=disable", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if db != nil {
		db.Close()
		t.Fatal("returned a pool without a successful connection check")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancelled connection check, got %v", err)
	}
}
