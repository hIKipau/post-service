package usecase

import (
	"context"
	"log/slog"
	"post-service/internal/adapters/postgresql"
	"post-service/internal/adapters/redis"
	"post-service/internal/domain"
	"time"

	"github.com/google/uuid"
)

type Usecase struct {
	repo   Repository
	cache  Cache
	logger *slog.Logger
}

func NewUsecase(repo *postgresql.Repo, cache *redis.Cache, logger *slog.Logger) *Usecase {
	return &Usecase{
		repo:   repo,
		cache:  cache,
		logger: logger,
	}
}

type Repository interface {
	CreatePost(ctx context.Context, post domain.Post) error
	UpdatePost(ctx context.Context, postID, authorID uuid.UUID, text string) error
	DeletePost(ctx context.Context, postID, authorID uuid.UUID) error

	GetPostsByIDs(ctx context.Context, postIDs []uuid.UUID) ([]domain.Post, error)
	GetChildrenPosts(ctx context.Context, parentID uuid.UUID, pageSize, page int64) ([]domain.Post, error)
	GetFeedCandidates(ctx context.Context, userID uuid.UUID, limit int64) ([]domain.FeedCandidate, error)
}

type Cache interface {
	SetFeed(ctx context.Context, userID uuid.UUID, postIDs []uuid.UUID, ttl time.Duration) error
	GetFeed(ctx context.Context, userID uuid.UUID, limit int64) ([]uuid.UUID, error)
	DeleteFeed(ctx context.Context, userID uuid.UUID) error
	GetFeedLength(ctx context.Context, userID uuid.UUID) (int64, error)

	GetSeen(ctx context.Context, userID uuid.UUID) (map[uuid.UUID]struct{}, error)
	AddSeen(ctx context.Context, userID uuid.UUID, postIDs []uuid.UUID, ttl time.Duration) error

	AddLike(ctx context.Context, userID, postID uuid.UUID) error
	RemoveLike(ctx context.Context, userID, postID uuid.UUID) error
	GetPostsLikes(ctx context.Context, postIDs []uuid.UUID) (map[uuid.UUID]int64, error)

	AddDislike(ctx context.Context, userID, postID uuid.UUID) error
	RemoveDislike(ctx context.Context, userID, postID uuid.UUID) error
	GetPostsDislikes(ctx context.Context, postIDs []uuid.UUID) (map[uuid.UUID]int64, error)

	SetPostRepliesCount(ctx context.Context, postID uuid.UUID, replyCount int64, ttl time.Duration) error
	GetPostsRepliesCount(ctx context.Context, postIDs []uuid.UUID) (map[uuid.UUID]int64, error)
}
