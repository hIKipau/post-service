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
func (r *readRepo) GetPostsByIDs(_ context.Context, ids []uuid.UUID) ([]domain.Post, error) {
	result := make([]domain.Post, 0, len(ids))
	for _, id := range ids {
		for _, post := range r.posts {
			if post.ID == id {
				result = append(result, post)
				break
			}
		}
	}
	return result, nil
}
func (r *readRepo) GetFeedCandidates(context.Context, uuid.UUID, int64, *domain.FeedCursor) ([]domain.FeedCandidate, error) {
	return r.candidates, nil
}

type readCache struct {
	Cache
	ids                 []uuid.UUID
	seen                map[uuid.UUID]struct{}
	likeErr, dislikeErr error
}

func (c *readCache) GetFeedState(context.Context, uuid.UUID) (domain.FeedState, error) {
	return domain.FeedState{IDs: append([]uuid.UUID(nil), c.ids...), Seen: c.seen}, nil
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
func (c *readCache) CommitFeed(_ context.Context, _ uuid.UUID, state domain.FeedState, shown []uuid.UUID, _, _ time.Duration) (bool, error) {
	c.ids = append([]uuid.UUID(nil), state.IDs...)
	if c.seen == nil {
		c.seen = make(map[uuid.UUID]struct{})
	}
	for _, id := range shown {
		c.seen[id] = struct{}{}
	}
	return true, nil
}

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
			cache := &readCache{}
			if mode == "cached feed" {
				cache.ids = []uuid.UUID{id}
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
