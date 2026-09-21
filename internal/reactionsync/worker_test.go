package reactionsync

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"post-service/internal/domain"

	"github.com/google/uuid"
)

type fakeSession struct {
	applied map[uuid.UUID]bool
	states  []domain.ReactionKind
	fail    error
	closed  bool
}

// ApplyReaction emulates atomic receipt deduplication and can fail before committing.
func (session *fakeSession) ApplyReaction(_ context.Context, event domain.ReactionEvent) error {
	if session.fail != nil {
		return session.fail
	}
	if session.applied == nil {
		session.applied = make(map[uuid.UUID]bool)
	}
	if !session.applied[event.EventID] {
		session.applied[event.EventID] = true
		session.states = append(session.states, event.Kind)
	}
	return nil
}

// Close records that leadership was released.
func (session *fakeSession) Close(context.Context) error { session.closed = true; return nil }

type fakeStream struct {
	events                   []domain.ReactionEvent
	ackErr, readErr, initErr error
	acks                     int
}

// EnsureReactionGroup injects initialization failures before any event is processed.
func (stream *fakeStream) EnsureReactionGroup(context.Context) error { return stream.initErr }

// NextReactionEvent keeps returning the current event until it is acknowledged.
func (stream *fakeStream) NextReactionEvent(context.Context) (*domain.ReactionEvent, error) {
	if stream.readErr != nil {
		return nil, stream.readErr
	}
	if len(stream.events) == 0 {
		return nil, nil
	}
	return &stream.events[0], nil
}

// AckReactionEvent removes a committed event unless an acknowledgement failure is injected.
func (stream *fakeStream) AckReactionEvent(context.Context, string) error {
	stream.acks++
	if stream.ackErr != nil {
		return stream.ackErr
	}
	stream.events = stream.events[1:]
	return nil
}

// TestProcessNextReplaysAfterLostAck verifies that SQL failure never acknowledges and lost ACK never double-applies.
func TestProcessNextReplaysAfterLostAck(t *testing.T) {
	failure := errors.New("storage unavailable")
	session := &fakeSession{fail: failure}
	stream := &fakeStream{events: []domain.ReactionEvent{{EventID: uuid.New(), Kind: domain.ReactionLike}, {EventID: uuid.New(), Kind: domain.ReactionDislike}}}
	ctx := context.Background()
	if _, err := ProcessNext(ctx, session, stream); !errors.Is(err, failure) || stream.acks != 0 {
		t.Fatalf("failed SQL acknowledged: %v", err)
	}
	session.fail = nil
	stream.ackErr = failure
	if _, err := ProcessNext(ctx, session, stream); !errors.Is(err, failure) {
		t.Fatal("lost ACK not reported")
	}
	if len(session.states) != 1 || session.states[0] != domain.ReactionLike {
		t.Fatal("first event not committed")
	}
	stream.ackErr = nil
	for i := 0; i < 2; i++ {
		if ok, err := ProcessNext(ctx, session, stream); err != nil || !ok {
			t.Fatalf("retry=%v %v", ok, err)
		}
	}
	if len(session.states) != 2 || session.states[1] != domain.ReactionDislike {
		t.Fatalf("duplicate/out of order states: %v", session.states)
	}
	if ok, err := ProcessNext(ctx, session, stream); err != nil || ok {
		t.Fatalf("empty stream=%v %v", ok, err)
	}
}

// TestWorkerSessionAlwaysCloses checks cleanup on initialization, read and apply failures.
func TestWorkerSessionAlwaysCloses(t *testing.T) {
	for _, point := range []string{"init", "read", "apply"} {
		t.Run(point, func(t *testing.T) {
			failure := errors.New("failure")
			session := &fakeSession{}
			stream := &fakeStream{events: []domain.ReactionEvent{{EventID: uuid.New()}}}
			switch point {
			case "init":
				stream.initErr = failure
			case "read":
				stream.readErr = failure
			case "apply":
				session.fail = failure
			}
			worker := New(func(context.Context) (Session, error) { return session, nil }, stream, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err := worker.runSession(context.Background()); !errors.Is(err, failure) || !session.closed || stream.acks != 0 {
				t.Fatalf("cleanup/error lost: %v, %+v", err, session)
			}
		})
	}
}

// TestWorkerReleasesLeadershipOnCancellation verifies that an acquired SQL session cannot outlive the worker.
func TestWorkerReleasesLeadershipOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := &fakeSession{}
	acquired := make(chan struct{})
	worker := New(func(context.Context) (Session, error) { close(acquired); return session, nil }, &fakeStream{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	done := make(chan struct{})
	go func() { defer close(done); worker.Run(ctx) }()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("worker did not acquire leadership")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
	if !session.closed {
		t.Fatal("worker leaked its SQL session")
	}
}

// TestWorkerStopsWithoutLeadership verifies cancellation interrupts retry delays and no stream work is attempted.
func TestWorkerStopsWithoutLeadership(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	acquired := make(chan struct{})
	worker := New(func(context.Context) (Session, error) { close(acquired); return nil, domain.ErrReactionSyncBusy }, nil, nil)
	done := make(chan struct{})
	go func() { defer close(done); worker.Run(ctx) }()
	<-acquired
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
}
