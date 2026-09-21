// Package reactionsync projects the ordered Redis reaction log into PostgreSQL.
package reactionsync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"post-service/internal/domain"
)

type Session interface {
	ApplyReaction(context.Context, domain.ReactionEvent) error
	Close(context.Context) error
}

type Stream interface {
	EnsureReactionGroup(context.Context) error
	NextReactionEvent(context.Context) (*domain.ReactionEvent, error)
	AckReactionEvent(context.Context, string) error
}

type AcquireSession func(context.Context) (Session, error)

type Worker struct {
	acquire AcquireSession
	stream  Stream
	logger  *slog.Logger
}

// New creates a sequential worker whose session factory enforces cross-process leadership.
func New(acquire AcquireSession, stream Stream, logger *slog.Logger) *Worker {
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{acquire: acquire, stream: stream, logger: logger}
}

// Run retries unavailable storage and leadership acquisition until cancellation.
// A failed event is never skipped, so later states cannot overtake an earlier state.
func (worker *Worker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		err := worker.runSession(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil && !errors.Is(err, domain.ErrReactionSyncBusy) {
			worker.logger.Error("Reaction synchronization paused; retrying", "error", err)
		}
		if !wait(ctx, time.Second) {
			return
		}
	}
}

// runSession holds leadership across reads, SQL commits and acknowledgements and always closes it.
func (worker *Worker) runSession(ctx context.Context) error {
	acquireCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	session, err := worker.acquire(acquireCtx)
	cancel()
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := session.Close(closeCtx); err != nil {
			worker.logger.Warn("Close reaction writer", "error", err)
		}
	}()
	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = worker.stream.EnsureReactionGroup(initCtx)
	cancel()
	if err != nil {
		return err
	}
	worker.logger.Info("Reaction synchronization writer acquired leadership")
	for ctx.Err() == nil {
		stepCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		processed, err := ProcessNext(stepCtx, session, worker.stream)
		cancel()
		if err != nil {
			return err
		}
		if !processed && !wait(ctx, 250*time.Millisecond) {
			break
		}
	}
	return ctx.Err()
}

// ProcessNext commits one event before acknowledging it; false means the stream is currently drained.
// The caller must own the ordered writer lock and initialize the consumer group first.
func ProcessNext(ctx context.Context, session Session, stream Stream) (bool, error) {
	event, err := stream.NextReactionEvent(ctx)
	if err != nil {
		return false, err
	}
	if event == nil {
		return false, nil
	}
	if err := session.ApplyReaction(ctx, *event); err != nil {
		return false, fmt.Errorf("persist reaction event %s (%s): %w", event.StreamID, event.EventID, err)
	}
	if err := stream.AckReactionEvent(ctx, event.StreamID); err != nil {
		return false, fmt.Errorf("acknowledge reaction event %s: %w", event.StreamID, err)
	}
	return true, nil
}

// wait provides an interruptible retry/idle delay, allowing prompt shutdown.
func wait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
