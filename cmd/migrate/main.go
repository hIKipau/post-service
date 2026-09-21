package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"syscall"

	"post-service/internal/migrator"

	"github.com/joho/godotenv"
)

// main runs the standalone migrator and cancels database work on SIGINT or SIGTERM.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stdout)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

// run parses the command, loads optional local configuration and enforces its deadline.
func run(ctx context.Context, args []string, output io.Writer) error {
	opts, err := parseOptions(args, output)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		// A dotenv parser error can contain credentials from the malformed line.
		return errors.New("cannot load .env: check file permissions and dotenv syntax")
	}
	ctx, cancel := context.WithTimeout(ctx, opts.timeout)
	defer cancel()
	return migrator.Run(ctx, os.Getenv("DATABASE_URL"), opts.command, output)
}
