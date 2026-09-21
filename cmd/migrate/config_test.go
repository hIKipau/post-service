package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"strings"
	"testing"
	"time"
)

// TestParseOptions accepts explicit commands and rejects unsafe or ambiguous invocations.
func TestParseOptions(t *testing.T) {
	for _, command := range []string{"up", "down", "status", "version"} {
		t.Run(command, func(t *testing.T) {
			opts, err := parseOptions([]string{command}, io.Discard)
			if err != nil || opts.command != command || opts.timeout != 5*time.Minute {
				t.Fatalf("parseOptions = %+v, %v", opts, err)
			}
		})
	}
	for _, args := range [][]string{nil, {"reset"}, {"up", "down"}, {"-timeout", "0", "up"}, {"-timeout", "-1s", "up"}, {"up", "-timeout", "1s"}, {"-timeout", "invalid", "up"}} {
		if _, err := parseOptions(args, io.Discard); err == nil {
			t.Errorf("expected invalid options for %v", args)
		}
	}
	opts, err := parseOptions([]string{"-timeout", "30s", "up"}, io.Discard)
	if err != nil || opts.timeout != 30*time.Second {
		t.Fatalf("custom timeout = %+v, %v", opts, err)
	}
	if _, err := parseOptions([]string{"-h"}, io.Discard); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("help error = %v", err)
	}
}

// TestRunHelp verifies that help works without database or application configuration.
func TestRunHelp(t *testing.T) {
	var output bytes.Buffer
	if err := run(context.Background(), []string{"-h"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Usage:") {
		t.Fatalf("missing usage: %s", output.String())
	}
}

// TestRunRequiresOnlyDatabaseURL ensures the migrator does not require Redis or JWT settings.
func TestRunRequiresOnlyDatabaseURL(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("DATABASE_URL", "")
	if err := run(context.Background(), []string{"up"}, io.Discard); err == nil || err.Error() != "DATABASE_URL is required" {
		t.Fatalf("missing database URL error = %v", err)
	}
}
