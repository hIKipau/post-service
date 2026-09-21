// Command reactions performs explicit offline import/restore of reaction membership.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"post-service/internal/adapters/postgresql"
	"post-service/internal/adapters/redis"
	"post-service/internal/reactionsync"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
)

// main runs one operator-requested maintenance operation and cancels it on termination signals.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stdout)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "reactions:", err)
		os.Exit(1)
	}
}

// run requires offline acknowledgement before connecting or changing either storage system.
func run(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("reactions", flag.ContinueOnError)
	flags.SetOutput(output)
	offline := flags.Bool("offline", false, "confirm all API instances/writers are stopped; operation replaces reaction data")
	timeout := flags.Duration("timeout", 10*time.Minute, "overall maintenance timeout")
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: reactions -offline [-timeout 10m] <import|restore>")
		fmt.Fprintln(output, "import: Redis membership -> PostgreSQL; restore: PostgreSQL membership -> Redis.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		return nil
	} else if err != nil {
		return err
	}
	if flags.NArg() != 1 || (flags.Arg(0) != "import" && flags.Arg(0) != "restore") {
		return errors.New("expected import or restore command; flags must precede the command")
	}
	if !*offline {
		return errors.New("stop all API instances and other reaction writers, back up both stores, then pass -offline")
	}
	if *timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return errors.New("cannot load .env; check permissions and syntax")
	}
	databaseURL, redisURL := os.Getenv("DATABASE_URL"), os.Getenv("REDIS_URL")
	if databaseURL == "" || redisURL == "" {
		return errors.New("DATABASE_URL and REDIS_URL are required")
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(output, nil))
	db, err := postgresql.New(ctx, databaseURL, logger)
	if err != nil {
		return err
	}
	defer db.Close()
	repo := postgresql.NewRepo(db)
	if err := repo.CheckReactionSchema(ctx); err != nil {
		return err
	}
	session, err := repo.AcquireReactionSession(ctx)
	if err != nil {
		return fmt.Errorf("acquire offline writer lock: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = session.Close(closeCtx)
	}()
	rdb, err := redis.New(ctx, redisURL, logger)
	if err != nil {
		return err
	}
	defer rdb.Close()
	cache := redis.NewCache(rdb)
	if err := cache.EnsureReactionGroup(ctx); err != nil {
		return err
	}
	// Repair historical aggregate snapshots, then replay all surviving events before taking a snapshot.
	if err := cache.BeginReactionMaintenance(ctx, flags.Arg(0)); err != nil {
		return err
	}
	if err := session.ReconcileReactionCounts(ctx); err != nil {
		return err
	}
	for {
		processed, err := reactionsync.ProcessNext(ctx, session, cache)
		if err != nil {
			return err
		}
		if !processed {
			break
		}
	}
	if flags.Arg(0) == "restore" {
		if err := cache.ClearReactionMembership(ctx); err != nil {
			return err
		}
	}
	after := uuid.Nil
	count := 0
	for {
		ids, err := session.ListReactionPosts(ctx, after)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			if flags.Arg(0) == "import" {
				snapshot, err := cache.ReadReactionSnapshot(ctx, id)
				if err != nil {
					return err
				}
				if err := session.ReplaceReactions(ctx, id, snapshot); err != nil {
					return err
				}
			} else {
				snapshot, err := session.ReadReactions(ctx, id)
				if err != nil {
					return err
				}
				if err := cache.RestoreReactionSnapshot(ctx, id, snapshot); err != nil {
					return err
				}
			}
			count++
		}
		after = ids[len(ids)-1]
	}
	if err := cache.EndReactionMaintenance(ctx); err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "Reaction %s completed for %d posts.\n", flags.Arg(0), count)
	return err
}
