package migrator

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/pressly/goose/v3"
)

// ValidateCommand rejects unsupported operations before opening a database connection.
func ValidateCommand(command string) error {
	switch command {
	case "up", "down", "status", "version":
		return nil
	default:
		return fmt.Errorf("unknown migration command %q: use up, down, status or version", command)
	}
}

// Run executes one explicit migration command and reports its result to output.
// Down rolls back only the latest migration and may permanently remove data.
func Run(ctx context.Context, databaseURL, command string, output io.Writer) (err error) {
	if err := ValidateCommand(command); err != nil {
		return err
	}
	provider, err := New(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, provider.Close()) }()
	return execute(ctx, provider, command, output)
}

// execute dispatches a validated command to Goose and prints successful results.
func execute(ctx context.Context, provider *goose.Provider, command string, output io.Writer) error {
	switch command {
	case "up":
		results, err := provider.Up(ctx)
		if err != nil {
			return fmt.Errorf("apply migrations: %w", err)
		}
		if len(results) == 0 {
			_, err = fmt.Fprintln(output, "Database is up to date.")
			return err
		}
		for _, result := range results {
			if _, err := fmt.Fprintln(output, result); err != nil {
				return err
			}
		}
	case "down":
		result, err := provider.Down(ctx)
		if err != nil {
			return fmt.Errorf("roll back migration: %w", err)
		}
		_, err = fmt.Fprintln(output, result)
		return err
	case "status":
		statuses, err := provider.Status(ctx)
		if err != nil {
			return fmt.Errorf("read migration status: %w", err)
		}
		for _, status := range statuses {
			if _, err := fmt.Fprintf(output, "%05d\t%s\t%s\n", status.Source.Version, status.State, status.Source.Path); err != nil {
				return err
			}
		}
	case "version":
		version, err := provider.GetDBVersion(ctx)
		if err != nil {
			return fmt.Errorf("read migration version: %w", err)
		}
		_, err = fmt.Fprintln(output, version)
		return err
	default:
		return ValidateCommand(command)
	}
	return nil
}
