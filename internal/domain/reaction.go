package domain

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

type ReactionKind int16

const (
	ReactionNone    ReactionKind = 0
	ReactionLike    ReactionKind = 1
	ReactionDislike ReactionKind = -1
)

var ErrReactionSyncBusy = errors.New("reaction synchronization is owned by another process")

// ReactionEvent records an absolute per-user state, not a counter increment.
type ReactionEvent struct {
	EventID  uuid.UUID
	StreamID string
	PostID   uuid.UUID
	UserID   uuid.UUID
	Kind     ReactionKind
}

// Validate rejects malformed events before any persistent changes are attempted.
func (event ReactionEvent) Validate() error {
	if event.EventID == uuid.Nil || event.PostID == uuid.Nil || event.UserID == uuid.Nil {
		return errors.New("reaction event IDs must be nonzero UUIDs")
	}
	if event.Kind != ReactionNone && event.Kind != ReactionLike && event.Kind != ReactionDislike {
		return fmt.Errorf("invalid reaction kind: %d", event.Kind)
	}
	return nil
}

// Reaction is one active member of a post's reaction snapshot.
type Reaction struct {
	UserID uuid.UUID
	Kind   ReactionKind
}
