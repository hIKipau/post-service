package domain

import (
	"time"

	"github.com/google/uuid"
)

// FeedCursor identifies the last scanned candidate in descending creation order.
type FeedCursor struct {
	CreatedAt time.Time `json:"created_at"`
	PostID    uuid.UUID `json:"post_id"`
}

// FeedState is a consistent snapshot of one user's pending queue and delivery history.
// Revision is an opaque token used to reject concurrent updates to the same snapshot.
type FeedState struct {
	IDs      []uuid.UUID
	Cursor   *FeedCursor
	Seen     map[uuid.UUID]struct{}
	Revision string
}
