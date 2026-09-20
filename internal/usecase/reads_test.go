package usecase

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"post-service/internal/domain"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Embedded interfaces make unexpected calls fail, including attempts to read
// reply counts from Redis instead of retaining the PostgreSQL value.
type readRepo struct {
	Repository
	posts      []domain.Post
	candidates []domain.FeedCandidate
}

func (r *readRepo) GetChildrenPosts(context.Context, uuid.UUID, int64, int64) ([]domain.Post, error) {
	return append([]domain.Post(nil), r.posts...), nil
}
func (r *readRepo) GetPostsByIDs(context.Context, []uuid.UUID) ([]domain.Post, error) {
	return append([]domain.Post(nil), r.posts...), nil
}
func (r *readRepo) GetFeedCandidates(context.Context, uuid.UUID, int64) ([]domain.FeedCandidate, error) {
	return r.candidates, nil
}

type readCache struct {
	Cache
	length              int64
	ids                 []uuid.UUID
	seen                map[uuid.UUID]struct{}
	likeErr, dislikeErr error
}

func (c *readCache) GetFeedLength(context.Context, uuid.UUID) (int64, error) { return c.length, nil }
func (c *readCache) GetFeed(context.Context, uuid.UUID, int64) ([]uuid.UUID, error) {
	return c.ids, nil
}
func (c *readCache) GetSeen(context.Context, uuid.UUID) (map[uuid.UUID]struct{}, error) {
	return c.seen, nil
}
func (c *readCache) GetPostsLikes(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]int64, error) {
	result := make(map[uuid.UUID]int64)
	for _, id := range ids {
		result[id] = 4
	}
	return result, c.likeErr
}
func (c *readCache) GetPostsDislikes(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]int64, error) {
	result := make(map[uuid.UUID]int64)
	for _, id := range ids {
		result[id] = 2
	}
	return result, c.dislikeErr
}
func (c *readCache) AddSeen(context.Context, uuid.UUID, []uuid.UUID, time.Duration) error { return nil }
func (c *readCache) DeleteFeed(context.Context, uuid.UUID) error                          { return nil }
func (c *readCache) SetFeed(context.Context, uuid.UUID, []uuid.UUID, time.Duration) error { return nil }

func newReadUsecase(repo Repository, cache Cache) *Usecase {
	return &Usecase{repo: repo, cache: cache, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestReadsRetainDatabaseReplyCount(t *testing.T) {
	for _, mode := range []string{"replies", "cached feed", "generated feed"} {
		t.Run(mode, func(t *testing.T) {
			id := uuid.New()
			repo := &readRepo{
				posts:      []domain.Post{{ID: id, ReplyCount: 9}},
				candidates: []domain.FeedCandidate{{PostID: id}},
			}
			cache := &readCache{ids: []uuid.UUID{id}}
			if mode == "cached feed" {
				cache.length = 20
			}
			uc := newReadUsecase(repo, cache)
			var posts []domain.Post
			var err error
			if mode == "replies" {
				posts, err = uc.GetRepliesPosts(context.Background(), uuid.New(), 20, 0)
			} else {
				posts, err = uc.GetFeed(context.Background(), uuid.New())
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(posts) != 1 || posts[0].ReplyCount != 9 || posts[0].LikeCount != 4 || posts[0].DislikeCount != 2 {
				t.Fatalf("unexpected posts: %+v", posts)
			}
		})
	}
}

func TestEmptyFeedAfterFiltering(t *testing.T) {
	for _, allSeen := range []bool{false, true} {
		id := uuid.New()
		repo, cache := &readRepo{}, &readCache{}
		if allSeen {
			repo.candidates = []domain.FeedCandidate{{PostID: id}}
			cache.seen = map[uuid.UUID]struct{}{id: {}}
		}
		posts, err := newReadUsecase(repo, cache).GetFeed(context.Background(), uuid.New())
		if err != nil || posts == nil || len(posts) != 0 {
			t.Fatalf("allSeen=%v posts=%v err=%v", allSeen, posts, err)
		}
	}
}

func TestGetPostRejectsMissingOrDeleted(t *testing.T) {
	id := uuid.New()
	now := time.Now()
	for _, posts := range [][]domain.Post{nil, {{ID: id, DeletedAt: &now}}} {
		uc := newReadUsecase(&readRepo{posts: posts}, &readCache{})
		if _, err := uc.GetPost(context.Background(), id); !errors.Is(err, domain.ErrPostNotFound) {
			t.Fatalf("expected not found, got %v", err)
		}
	}
	uc := newReadUsecase(&readRepo{posts: []domain.Post{{ID: id}}}, &readCache{})
	post, err := uc.GetPost(context.Background(), id)
	if err != nil || post.ID != id {
		t.Fatalf("post=%+v err=%v", post, err)
	}
}

func TestRepliesPropagateReactionErrors(t *testing.T) {
	failure := errors.New("redis unavailable")
	for _, cache := range []*readCache{{likeErr: failure}, {dislikeErr: failure}} {
		repo := &readRepo{posts: []domain.Post{{ID: uuid.New(), LikeCount: 99}}}
		posts, err := newReadUsecase(repo, cache).GetRepliesPosts(context.Background(), uuid.New(), 20, 0)
		if !errors.Is(err, failure) || posts != nil {
			t.Fatalf("posts=%v err=%v", posts, err)
		}
	}
}
