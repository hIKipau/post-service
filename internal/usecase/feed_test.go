package usecase

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	redisadapter "post-service/internal/adapters/redis"
	"post-service/internal/domain"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
)

type feedTestRepo struct {
	Repository
	posts                 map[uuid.UUID]domain.Post
	candidates            []domain.FeedCandidate
	loadErr, candidateErr error
	beforeLoad            func()
	queries               atomic.Int32
}

// GetFeedCandidates models the repository's descending composite cursor and counts batch reads.
func (r *feedTestRepo) GetFeedCandidates(_ context.Context, user uuid.UUID, limit int64, before *domain.FeedCursor) ([]domain.FeedCandidate, error) {
	r.queries.Add(1)
	if r.candidateErr != nil {
		return nil, r.candidateErr
	}
	result := make([]domain.FeedCandidate, 0)
	for _, candidate := range r.candidates {
		if candidate.AuthorID == user {
			continue
		}
		if before != nil && (candidate.CreatedAt.After(before.CreatedAt) || (candidate.CreatedAt.Equal(before.CreatedAt) && strings.Compare(candidate.PostID.String(), before.PostID.String()) >= 0)) {
			continue
		}
		result = append(result, candidate)
		if int64(len(result)) == limit {
			break
		}
	}
	return result, nil
}

// GetPostsByIDs models ordered repository reads and can inject a failure or synchronization barrier.
func (r *feedTestRepo) GetPostsByIDs(_ context.Context, ids []uuid.UUID) ([]domain.Post, error) {
	if r.beforeLoad != nil {
		r.beforeLoad()
	}
	if r.loadErr != nil {
		return nil, r.loadErr
	}
	result := make([]domain.Post, 0, len(ids))
	for _, id := range ids {
		if post, ok := r.posts[id]; ok {
			result = append(result, post)
		}
	}
	return result, nil
}

// feedFixture uses the real Redis adapter with miniredis and deterministic newest-first post timestamps.
func feedFixture(t *testing.T, count int) (*Usecase, *feedTestRepo, *redisadapter.Cache, uuid.UUID, []uuid.UUID) {
	t.Helper()
	server := miniredis.RunT(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	connection, err := redisadapter.New(context.Background(), "redis://"+server.Addr(), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	cache := redisadapter.NewCache(connection)
	repo := &feedTestRepo{posts: make(map[uuid.UUID]domain.Post)}
	now := time.Now().UTC().Truncate(time.Microsecond)
	ids := make([]uuid.UUID, count)
	for i := range ids {
		id, author := uuid.New(), uuid.New()
		created := now.Add(-time.Duration(i) * time.Minute)
		ids[i] = id
		repo.posts[id] = domain.Post{ID: id, AuthorID: author, Text: "post", CreatedAt: created, UpdatedAt: created, ReplyCount: 2}
		repo.candidates = append(repo.candidates, domain.FeedCandidate{PostID: id, AuthorID: author, CreatedAt: created, RepliesCount: 2})
	}
	return newReadUsecase(repo, cache), repo, cache, uuid.New(), ids
}

// seedFeed creates an initial queue and seen history through the same atomic operation used by production.
func seedFeed(t *testing.T, cache *redisadapter.Cache, user uuid.UUID, ids, seen []uuid.UUID) {
	t.Helper()
	ok, err := cache.CommitFeed(context.Background(), user, domain.FeedState{IDs: ids}, seen, feedTTL, feedSeenTTL)
	if err != nil || !ok {
		t.Fatalf("seed=%v err=%v", ok, err)
	}
}

// feedState loads a snapshot for assertions and fails immediately on a Redis error.
func feedState(t *testing.T, cache *redisadapter.Cache, user uuid.UUID) domain.FeedState {
	t.Helper()
	state, err := cache.GetFeedState(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

// TestFeedPagesDoNotRepeat ensures initial generation and cached reads use the same 20-post delivery path.
func TestFeedPagesDoNotRepeat(t *testing.T) {
	uc, _, cache, user, _ := feedFixture(t, 100)
	delivered := make(map[uuid.UUID]struct{})
	for page := 0; page < 5; page++ {
		posts, err := uc.GetFeed(context.Background(), user)
		if err != nil || len(posts) != 20 {
			t.Fatalf("page %d: %d posts, %v", page, len(posts), err)
		}
		for _, post := range posts {
			if _, ok := delivered[post.ID]; ok {
				t.Fatalf("duplicate %s", post.ID)
			}
			delivered[post.ID] = struct{}{}
		}
		state := feedState(t, cache, user)
		if len(state.IDs) != 100-(page+1)*20 || len(state.Seen) != (page+1)*20 {
			t.Fatalf("page %d: queue=%d seen=%d", page, len(state.IDs), len(state.Seen))
		}
	}
	posts, err := uc.GetFeed(context.Background(), user)
	if err != nil || posts == nil || len(posts) != 0 {
		t.Fatalf("exhausted feed=%v, %v", posts, err)
	}
}

// TestFeedRefillPreservesSevenQueuedPosts ensures replenishment appends without replacing or duplicating the tail.
func TestFeedRefillPreservesSevenQueuedPosts(t *testing.T) {
	uc, _, cache, user, ids := feedFixture(t, 100)
	seedFeed(t, cache, user, ids[:7], nil)
	posts, err := uc.GetFeed(context.Background(), user)
	if err != nil || len(posts) != 20 {
		t.Fatalf("posts=%d err=%v", len(posts), err)
	}
	for i := 0; i < 7; i++ {
		if posts[i].ID != ids[i] {
			t.Fatal("queued tail was not preserved")
		}
	}
	state := feedState(t, cache, user)
	if len(state.IDs) != 80 || len(state.Seen) != 20 {
		t.Fatalf("queue=%d seen=%d", len(state.IDs), len(state.Seen))
	}
	for _, id := range state.IDs {
		if _, ok := state.Seen[id]; ok {
			t.Fatal("delivered ID remained queued")
		}
	}
}

// TestFeedPartialPage marks only the seven available posts as seen rather than assuming a full page.
func TestFeedPartialPage(t *testing.T) {
	uc, _, cache, user, _ := feedFixture(t, 7)
	posts, err := uc.GetFeed(context.Background(), user)
	if err != nil || len(posts) != 7 {
		t.Fatalf("posts=%d err=%v", len(posts), err)
	}
	state := feedState(t, cache, user)
	if len(state.IDs) != 0 || len(state.Seen) != 7 {
		t.Fatalf("state=%+v", state)
	}
}

// TestFeedCursorContinuesBeyondSeenBatches verifies bounded work, saved progress and access beyond the latest 100.
func TestFeedCursorContinuesBeyondSeenBatches(t *testing.T) {
	uc, repo, cache, user, ids := feedFixture(t, 501)
	seedFeed(t, cache, user, nil, ids[:500])
	for attempt := 0; attempt < 3; attempt++ {
		before := repo.queries.Load()
		posts, err := uc.GetFeed(context.Background(), user)
		if err != nil {
			t.Fatal(err)
		}
		if repo.queries.Load()-before > feedScanBatches {
			t.Fatal("scan exceeded request budget")
		}
		if attempt < 2 {
			if len(posts) != 0 || feedState(t, cache, user).Cursor == nil {
				t.Fatal("expected saved progress through seen candidates")
			}
		} else if len(posts) != 1 || posts[0].ID != ids[500] {
			t.Fatalf("older post not reached: %+v", posts)
		}
	}
}

// TestFeedRefillChecksNewPostsWhileScanningOlderHistory avoids hiding new publications behind a deep cursor.
func TestFeedRefillChecksNewPostsWhileScanningOlderHistory(t *testing.T) {
	uc, repo, cache, user, ids := feedFixture(t, 350)
	seedFeed(t, cache, user, nil, ids[:350])
	if _, err := uc.GetFeed(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	fresh := domain.Post{ID: uuid.New(), AuthorID: uuid.New(), Text: "fresh", CreatedAt: time.Now().UTC()}
	repo.posts[fresh.ID] = fresh
	repo.candidates = append([]domain.FeedCandidate{{PostID: fresh.ID, AuthorID: fresh.AuthorID, CreatedAt: fresh.CreatedAt}}, repo.candidates...)
	posts, err := uc.GetFeed(context.Background(), user)
	if err != nil || len(posts) != 1 || posts[0].ID != fresh.ID {
		t.Fatalf("new post missed: %+v, %v", posts, err)
	}
}

// TestFeedSkipsUnavailablePosts backfills from queued IDs and records only active foreign posts in seen.
func TestFeedSkipsUnavailablePosts(t *testing.T) {
	uc, repo, cache, user, ids := feedFixture(t, 24)
	seedFeed(t, cache, user, ids, nil)
	delete(repo.posts, ids[0])
	deleted := repo.posts[ids[1]]
	now := time.Now()
	deleted.DeletedAt = &now
	repo.posts[ids[1]] = deleted
	own := repo.posts[ids[2]]
	own.AuthorID = user
	repo.posts[ids[2]] = own
	posts, err := uc.GetFeed(context.Background(), user)
	if err != nil || len(posts) != 20 || posts[0].ID != ids[3] {
		t.Fatalf("posts=%+v err=%v", posts, err)
	}
	state := feedState(t, cache, user)
	if len(state.IDs) != 1 || len(state.Seen) != 20 {
		t.Fatalf("state=%+v", state)
	}
	for _, id := range ids[:3] {
		if _, ok := state.Seen[id]; ok {
			t.Fatal("unavailable post marked as seen")
		}
	}
}

type failedReactionCache struct {
	Cache
	err error
}

// GetPostsLikes injects an error after posts have been loaded but before the queue is committed.
func (c failedReactionCache) GetPostsLikes(context.Context, []uuid.UUID) (map[uuid.UUID]int64, error) {
	return nil, c.err
}

// TestFeedReadFailurePreservesState ensures PostgreSQL, candidate and reaction failures do not consume a page.
func TestFeedReadFailurePreservesState(t *testing.T) {
	for _, failurePoint := range []string{"posts", "candidates", "reactions"} {
		t.Run(failurePoint, func(t *testing.T) {
			uc, repo, cache, user, ids := feedFixture(t, 30)
			failure := errors.New("storage unavailable")
			queued := ids[:20]
			switch failurePoint {
			case "posts":
				repo.loadErr = failure
			case "candidates":
				repo.candidateErr = failure
				queued = ids[:7]
			case "reactions":
				uc.cache = failedReactionCache{Cache: cache, err: failure}
			}
			seedFeed(t, cache, user, queued, nil)
			before := feedState(t, cache, user)
			posts, err := uc.GetFeed(context.Background(), user)
			if !errors.Is(err, failure) || posts != nil {
				t.Fatalf("posts=%v err=%v", posts, err)
			}
			if after := feedState(t, cache, user); !reflect.DeepEqual(before, after) {
				t.Fatalf("failed read changed state: %+v", after)
			}
		})
	}
}

// TestConcurrentFeedRequestsRetryStaleSnapshot forces two readers to prepare the same page before either commits.
func TestConcurrentFeedRequestsRetryStaleSnapshot(t *testing.T) {
	uc, repo, cache, user, ids := feedFixture(t, 40)
	seedFeed(t, cache, user, ids, nil)
	var loads atomic.Int32
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	repo.beforeLoad = func() {
		if loads.Add(1) <= 2 {
			ready <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	}
	type result struct {
		posts []domain.Post
		err   error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() { posts, err := uc.GetFeed(ctx, user); results <- result{posts, err} }()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal("concurrent readers did not reach the barrier")
		}
	}
	close(release)
	unique := make(map[uuid.UUID]struct{})
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err != nil || len(r.posts) != 20 {
			t.Fatalf("posts=%d err=%v", len(r.posts), r.err)
		}
		for _, post := range r.posts {
			if _, ok := unique[post.ID]; ok {
				t.Fatal("concurrent requests returned duplicate posts")
			}
			unique[post.ID] = struct{}{}
		}
	}
	if len(feedState(t, cache, user).Seen) != 40 {
		t.Fatal("incorrect concurrent seen history")
	}
}

// TestFeedRankingUsesLiveReactions verifies Redis counts replace stale SQL snapshots without altering the scan cursor.
func TestFeedRankingUsesLiveReactions(t *testing.T) {
	uc, repo, cache, user, ids := feedFixture(t, 100)
	repo.candidates[0].LikesCount = 1000000 // This stale snapshot must not influence ranking.
	for i := 0; i < 8; i++ {
		if err := cache.AddLike(context.Background(), uuid.New(), ids[99]); err != nil {
			t.Fatal(err)
		}
	}
	posts, err := uc.GetFeed(context.Background(), user)
	if err != nil || len(posts) != 20 || posts[0].ID != ids[99] || posts[0].LikeCount != 8 || posts[0].ReplyCount != 2 {
		t.Fatalf("live ranking=%+v err=%v", posts, err)
	}
	state := feedState(t, cache, user)
	if state.Cursor == nil || state.Cursor.PostID != ids[99] {
		t.Fatal("scan cursor must follow the original SQL order, not the last ranked candidate")
	}
}

type conflictingFeedCache struct {
	Cache
	attempts int
	err      error
}

// CommitFeed injects persistent contention or a Redis failure without mutating the stored queue.
func (c *conflictingFeedCache) CommitFeed(context.Context, uuid.UUID, domain.FeedState, []uuid.UUID, time.Duration, time.Duration) (bool, error) {
	c.attempts++
	return false, c.err
}

// TestFeedCommitFailures verifies bounded retries and ensures uncommitted pages are never returned.
func TestFeedCommitFailures(t *testing.T) {
	for _, commitErr := range []error{nil, errors.New("redis unavailable")} {
		uc, _, cache, user, ids := feedFixture(t, 20)
		seedFeed(t, cache, user, ids, nil)
		before := feedState(t, cache, user)
		conflicts := &conflictingFeedCache{Cache: cache, err: commitErr}
		uc.cache = conflicts
		posts, err := uc.GetFeed(context.Background(), user)
		if err == nil || posts != nil {
			t.Fatalf("uncommitted page returned: %+v, %v", posts, err)
		}
		wantAttempts := feedCommitAttempts
		if commitErr != nil {
			wantAttempts = 1
			if !errors.Is(err, commitErr) {
				t.Fatalf("lost commit error: %v", err)
			}
		}
		if conflicts.attempts != wantAttempts {
			t.Fatalf("commit attempts=%d want=%d", conflicts.attempts, wantAttempts)
		}
		if after := feedState(t, cache, user); !reflect.DeepEqual(before, after) {
			t.Fatal("failed commit changed state")
		}
	}
}

// TestFeedCleansLegacyDuplicates removes old queued seen entries while preserving pending ID order.
func TestFeedCleansLegacyDuplicates(t *testing.T) {
	uc, _, cache, user, ids := feedFixture(t, 25)
	queue := append([]uuid.UUID{ids[0], ids[1], ids[1]}, ids[2:]...)
	seedFeed(t, cache, user, queue, ids[:1])
	posts, err := uc.GetFeed(context.Background(), user)
	if err != nil || len(posts) != 20 || posts[0].ID != ids[1] {
		t.Fatalf("legacy page=%+v err=%v", posts, err)
	}
	state := feedState(t, cache, user)
	if len(state.IDs) != 4 || len(state.Seen) != 21 {
		t.Fatalf("legacy cleanup state=%+v", state)
	}
}

// TestFeedRanking verifies freshness, engagement, dislikes, future timestamps and deterministic tie breakers.
func TestFeedRanking(t *testing.T) {
	now := time.Now().UTC()
	base := domain.FeedCandidate{CreatedAt: now}
	if score := feedScore(base, now); score != 1 {
		t.Fatalf("new post score=%f", score)
	}
	old := base
	old.CreatedAt = now.Add(-24 * time.Hour)
	if feedScore(old, now) != 0.5 {
		t.Fatal("age did not reduce score")
	}
	liked := base
	liked.LikesCount = 10
	replied := base
	replied.RepliesCount = 10
	disliked := liked
	disliked.DislikesCount = 5
	if feedScore(liked, now) <= feedScore(replied, now) || feedScore(replied, now) <= feedScore(base, now) || feedScore(disliked, now) != 1 {
		t.Fatal("unexpected engagement weights")
	}
	future := base
	future.CreatedAt = now.Add(time.Hour)
	if feedScore(future, now) != 1 {
		t.Fatal("future date inflated score")
	}
	large := base
	large.LikesCount = math.MaxInt64
	large.RepliesCount = math.MaxInt64
	if score := feedScore(large, now); math.IsNaN(score) || math.IsInf(score, 0) || score <= 1 {
		t.Fatalf("invalid large-counter score=%f", score)
	}
	a, b := uuid.MustParse("00000000-0000-0000-0000-000000000001"), uuid.MustParse("00000000-0000-0000-0000-000000000002")
	candidates := []domain.FeedCandidate{{PostID: a, CreatedAt: now}, {PostID: b, CreatedAt: now}}
	rankFeedCandidates(candidates, now)
	if candidates[0].PostID != b {
		t.Fatal("UUID tie breaker is not descending")
	}
	slices.Reverse(candidates)
	rankFeedCandidates(candidates, now)
	if candidates[0].PostID != b {
		t.Fatal("ranking is nondeterministic")
	}
}
