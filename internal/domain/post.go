package domain

import (
	"time"

	"github.com/google/uuid"
)

type Post struct {
	ID           uuid.UUID
	AuthorID     uuid.UUID
	Text         string
	RootID       *uuid.UUID
	ParentID     *uuid.UUID
	ReplyCount   int64
	LikeCount    int64
	DislikeCount int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    *time.Time
}
type FeedCandidate struct {
	PostID        uuid.UUID
	AuthorID      uuid.UUID
	CreatedAt     time.Time
	LikesCount    int64
	DislikesCount int64
	RepliesCount  int64
}
