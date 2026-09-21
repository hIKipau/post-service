package usecase

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"post-service/internal/domain"

	"github.com/google/uuid"
)

const (
	feedPageSize       = 20
	feedBatchSize      = 100
	feedScanBatches    = 3
	feedCommitAttempts = 3
	feedTTL            = 24 * time.Hour
	feedSeenTTL        = 3 * 24 * time.Hour
)

// GetFeed prepares at most 20 active posts and atomically advances the queue and seen history.
// Failed reads leave the queue unchanged; concurrent commits retry from a fresh snapshot.
func (uc *Usecase) GetFeed(ctx context.Context, userID uuid.UUID) ([]domain.Post, error) {
	now := time.Now()
	for attempt := 0; attempt < feedCommitAttempts; attempt++ {
		state, err := uc.cache.GetFeedState(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("read feed state: %w", err)
		}
		// Also cleans queues written by the previous implementation, which queued already-seen IDs.
		state.IDs = pendingFeedIDs(state.IDs, state.Seen)
		if len(state.IDs) < feedPageSize {
			if err := uc.refillFeed(ctx, userID, &state, now); err != nil {
				return nil, err
			}
		}
		posts, shown, err := uc.prepareFeedPage(ctx, userID, &state)
		if err != nil {
			return nil, err
		}
		committed, err := uc.cache.CommitFeed(ctx, userID, state, shown, feedTTL, feedSeenTTL)
		if err != nil {
			return nil, fmt.Errorf("commit feed: %w", err)
		}
		if committed {
			uc.logger.Debug("Feed page prepared", "user_id", userID, "posts_count", len(posts), "queued_count", len(state.IDs))
			return posts, nil
		}
	}
	return nil, errors.New("feed changed concurrently; retry the request")
}

// pendingFeedIDs preserves queue order while removing duplicates and previously delivered IDs.
func pendingFeedIDs(ids []uuid.UUID, seen map[uuid.UUID]struct{}) []uuid.UUID {
	result := make([]uuid.UUID, 0, len(ids))
	unique := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		if _, ok := unique[id]; ok {
			continue
		}
		unique[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

// refillFeed checks the newest batch first, then resumes older candidates with a bounded scan.
// Existing queued IDs stay at the front; the cursor follows SQL order, never ranking order.
func (uc *Usecase) refillFeed(ctx context.Context, userID uuid.UUID, state *domain.FeedState, now time.Time) error {
	excluded := make(map[uuid.UUID]struct{}, len(state.Seen)+len(state.IDs))
	for id := range state.Seen {
		excluded[id] = struct{}{}
	}
	for _, id := range state.IDs {
		excluded[id] = struct{}{}
	}
	candidates := make([]domain.FeedCandidate, 0, feedBatchSize)
	var before *domain.FeedCursor
	for batch := 0; batch < feedScanBatches; batch++ {
		raw, err := uc.repo.GetFeedCandidates(ctx, userID, feedBatchSize, before)
		if err != nil {
			return fmt.Errorf("get feed candidates: %w", err)
		}
		for _, candidate := range raw {
			if _, ok := excluded[candidate.PostID]; ok {
				continue
			}
			excluded[candidate.PostID] = struct{}{}
			candidates = append(candidates, candidate)
		}
		if len(raw) < feedBatchSize {
			state.Cursor = nil // The next refill starts a new scan, allowing new posts to enter.
			break
		}
		last := raw[len(raw)-1]
		// A fresh-head check must not erase progress into older pages.
		if batch > 0 || state.Cursor == nil {
			state.Cursor = &domain.FeedCursor{CreatedAt: last.CreatedAt, PostID: last.PostID}
		}
		if len(state.IDs)+len(candidates) >= feedPageSize {
			break
		}
		before = state.Cursor
	}
	if len(candidates) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(candidates))
	for i, candidate := range candidates {
		ids[i] = candidate.PostID
	}
	likes, err := uc.cache.GetPostsLikes(ctx, ids)
	if err != nil {
		return fmt.Errorf("get candidate likes: %w", err)
	}
	dislikes, err := uc.cache.GetPostsDislikes(ctx, ids)
	if err != nil {
		return fmt.Errorf("get candidate dislikes: %w", err)
	}
	for i := range candidates {
		candidates[i].LikesCount = likes[candidates[i].PostID]
		candidates[i].DislikesCount = dislikes[candidates[i].PostID]
	}
	rankFeedCandidates(candidates, now)
	for _, candidate := range candidates {
		state.IDs = append(state.IDs, candidate.PostID)
	}
	return nil
}

// prepareFeedPage loads queued posts before consuming them and skips missing, deleted or own posts.
// It uses remaining queued IDs to replace invalid entries, without an unbounded database refill loop.
func (uc *Usecase) prepareFeedPage(ctx context.Context, userID uuid.UUID, state *domain.FeedState) ([]domain.Post, []uuid.UUID, error) {
	posts := make([]domain.Post, 0, feedPageSize)
	for len(state.IDs) > 0 && len(posts) < feedPageSize {
		count := min(feedPageSize-len(posts), len(state.IDs))
		ids := state.IDs[:count]
		loaded, err := uc.repo.GetPostsByIDs(ctx, ids)
		if err != nil {
			return nil, nil, fmt.Errorf("load feed posts: %w", err)
		}
		byID := make(map[uuid.UUID]domain.Post, len(loaded))
		for _, post := range loaded {
			byID[post.ID] = post
		}
		for _, id := range ids {
			post, ok := byID[id]
			if ok && post.DeletedAt == nil && post.AuthorID != userID {
				posts = append(posts, post)
			}
		}
		state.IDs = state.IDs[count:]
	}
	shown := make([]uuid.UUID, len(posts))
	for i, post := range posts {
		shown[i] = post.ID
	}
	if len(shown) == 0 {
		return posts, shown, nil
	}
	likes, err := uc.cache.GetPostsLikes(ctx, shown)
	if err != nil {
		return nil, nil, fmt.Errorf("get feed likes: %w", err)
	}
	dislikes, err := uc.cache.GetPostsDislikes(ctx, shown)
	if err != nil {
		return nil, nil, fmt.Errorf("get feed dislikes: %w", err)
	}
	for i := range posts {
		posts[i].LikeCount = likes[posts[i].ID]
		posts[i].DislikeCount = dislikes[posts[i].ID]
	}
	return posts, shown, nil
}

// rankFeedCandidates orders candidates by score, then newest creation time and descending UUID.
func rankFeedCandidates(candidates []domain.FeedCandidate, now time.Time) {
	scores := make(map[uuid.UUID]float64, len(candidates))
	for _, candidate := range candidates {
		scores[candidate.PostID] = feedScore(candidate, now)
	}
	slices.SortFunc(candidates, func(a, b domain.FeedCandidate) int {
		if scores[a.PostID] > scores[b.PostID] {
			return -1
		}
		if scores[a.PostID] < scores[b.PostID] {
			return 1
		}
		if cmp := b.CreatedAt.Compare(a.CreatedAt); cmp != 0 {
			return cmp
		}
		return strings.Compare(b.PostID.String(), a.PostID.String())
	})
}

// feedScore balances freshness with a logarithmically dampened engagement boost.
func feedScore(candidate domain.FeedCandidate, now time.Time) float64 {
	activity := math.Max(0, float64(candidate.LikesCount)+0.5*float64(candidate.RepliesCount)-2*float64(candidate.DislikesCount))
	ageHours := math.Max(0, now.Sub(candidate.CreatedAt).Hours())
	return (1 + math.Log1p(activity)) / (1 + ageHours/24)
}
