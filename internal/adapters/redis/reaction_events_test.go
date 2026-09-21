package redis

import (
	"context"
	"sync"
	"testing"

	"post-service/internal/domain"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

// TestReactionEventsAreOrderedAndPendingFirst verifies no-op filtering, switching and restart-safe redelivery.
func TestReactionEventsAreOrderedAndPendingFirst(t *testing.T) {
	cache, _ := testCache(t)
	ctx, user, post := context.Background(), uuid.New(), uuid.New()
	for _, action := range []func(context.Context, uuid.UUID, uuid.UUID) error{
		cache.AddLike, cache.AddLike, cache.RemoveDislike, cache.AddDislike, cache.RemoveLike, cache.RemoveDislike, cache.RemoveDislike,
	} {
		if err := action(ctx, user, post); err != nil {
			t.Fatal(err)
		}
	}
	// Create the group after events exist: starting at '$' would incorrectly skip this backlog.
	if err := cache.EnsureReactionGroup(ctx); err != nil {
		t.Fatal(err)
	}
	if err := cache.EnsureReactionGroup(ctx); err != nil {
		t.Fatal(err)
	}
	eventIDs := make(map[uuid.UUID]bool)
	for _, kind := range []domain.ReactionKind{domain.ReactionLike, domain.ReactionDislike, domain.ReactionNone} {
		event, err := cache.NextReactionEvent(ctx)
		if err != nil || event == nil || event.Kind != kind || event.PostID != post || event.UserID != user {
			t.Fatalf("event=%+v err=%v", event, err)
		}
		if eventIDs[event.EventID] {
			t.Fatal("event ID reused")
		}
		eventIDs[event.EventID] = true
		redelivered, err := NewCache(cache.Redis).NextReactionEvent(ctx)
		if err != nil || redelivered == nil || *redelivered != *event {
			t.Fatalf("pending event not recovered: %+v, %v", redelivered, err)
		}
		if err := cache.AckReactionEvent(ctx, event.StreamID); err != nil {
			t.Fatal(err)
		}
	}
	if event, err := cache.NextReactionEvent(ctx); err != nil || event != nil {
		t.Fatalf("unexpected event=%+v err=%v", event, err)
	}
	if length, err := cache.client.XLen(ctx, reactionStream).Result(); err != nil || length != 3 {
		t.Fatalf("stream length=%d err=%v", length, err)
	}
	if pending, err := cache.client.XPending(ctx, reactionStream, reactionGroup).Result(); err != nil || pending.Count != 0 {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
}

// TestConcurrentReactionReadersCannotOvertakePending models overlapping readers during leadership failover.
func TestConcurrentReactionReadersCannotOvertakePending(t *testing.T) {
	cache, _ := testCache(t)
	ctx, user, post := context.Background(), uuid.New(), uuid.New()
	if err := cache.AddLike(ctx, user, post); err != nil {
		t.Fatal(err)
	}
	if err := cache.AddDislike(ctx, user, post); err != nil {
		t.Fatal(err)
	}
	if err := cache.EnsureReactionGroup(ctx); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			event, err := cache.NextReactionEvent(ctx)
			if err != nil || event == nil || event.Kind != domain.ReactionLike {
				t.Errorf("reader overtook pending event: %+v %v", event, err)
			}
		}()
	}
	wg.Wait()
	if pending, err := cache.client.XPending(ctx, reactionStream, reactionGroup).Result(); err != nil || pending.Count != 1 {
		t.Fatalf("multiple pending events: %+v %v", pending, err)
	}
}

// TestReactionPublishFailurePreservesMembership checks the event destination before changing visible reactions.
func TestReactionPublishFailurePreservesMembership(t *testing.T) {
	cache, _ := testCache(t)
	ctx, user, post := context.Background(), uuid.New(), uuid.New()
	if err := cache.client.SAdd(ctx, "post:dislikes:"+post.String(), user.String()).Err(); err != nil {
		t.Fatal(err)
	}
	if err := cache.client.Set(ctx, reactionStream, "wrong-type", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := cache.AddLike(ctx, user, post); err == nil {
		t.Fatal("expected event publish failure")
	}
	if liked, err := cache.IsLiked(ctx, user, post); err != nil || liked {
		t.Fatal("like changed despite publish error")
	}
	if disliked, err := cache.IsDisliked(ctx, user, post); err != nil || !disliked {
		t.Fatal("dislike changed despite publish error")
	}
}

// TestMalformedReactionEventBlocksLaterEvents preserves ordering instead of silently acknowledging invalid data.
func TestMalformedReactionEventBlocksLaterEvents(t *testing.T) {
	cache, _ := testCache(t)
	ctx := context.Background()
	if err := cache.client.XAdd(ctx, &goredis.XAddArgs{Stream: reactionStream, Values: map[string]any{"state": "bad"}}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := cache.AddLike(ctx, uuid.New(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	if err := cache.EnsureReactionGroup(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := cache.NextReactionEvent(ctx); err == nil {
			t.Fatal("malformed event was skipped")
		}
	}
	if pending, err := cache.client.XPending(ctx, reactionStream, reactionGroup).Result(); err != nil || pending.Count != 1 {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
}

// TestMaintenanceBarrier persists after a failed operation, prevents writes and requires resuming the same mode.
func TestMaintenanceBarrier(t *testing.T) {
	cache, _ := testCache(t)
	ctx := context.Background()
	if err := cache.CheckReactionMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	if err := cache.BeginReactionMaintenance(ctx, "restore"); err != nil {
		t.Fatal(err)
	}
	if err := cache.CheckReactionMaintenance(ctx); err == nil {
		t.Fatal("startup not blocked during maintenance")
	}
	if err := cache.AddLike(ctx, uuid.New(), uuid.New()); err == nil {
		t.Fatal("mutation accepted during maintenance")
	}
	if err := cache.BeginReactionMaintenance(ctx, "import"); err == nil {
		t.Fatal("allowed importing a partially restored Redis snapshot")
	}
	if err := cache.BeginReactionMaintenance(ctx, "restore"); err != nil {
		t.Fatal("cannot resume restore")
	}
	if err := cache.EndReactionMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	if err := cache.CheckReactionMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	if err := cache.AddLike(ctx, uuid.New(), uuid.New()); err != nil {
		t.Fatal(err)
	}
}

// TestReactionSnapshotRoundTrip restores membership without events and clears only reaction keys.
func TestReactionSnapshotRoundTrip(t *testing.T) {
	cache, _ := testCache(t)
	ctx, post := context.Background(), uuid.New()
	likeUser, dislikeUser := uuid.New(), uuid.New()
	snapshot := []domain.Reaction{{UserID: likeUser, Kind: domain.ReactionLike}, {UserID: dislikeUser, Kind: domain.ReactionDislike}}
	if err := cache.RestoreReactionSnapshot(ctx, post, snapshot); err != nil {
		t.Fatal(err)
	}
	loaded, err := cache.ReadReactionSnapshot(ctx, post)
	if err != nil || len(loaded) != 2 {
		t.Fatalf("snapshot=%+v err=%v", loaded, err)
	}
	if exists, err := cache.client.Exists(ctx, reactionStream).Result(); err != nil || exists != 0 {
		t.Fatal("restore emitted reaction events")
	}
	if err := cache.EnsureReactionGroup(ctx); err != nil {
		t.Fatal(err)
	}
	if err := cache.client.Set(ctx, "feed:unrelated", "keep", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := cache.ClearReactionMembership(ctx); err != nil {
		t.Fatal(err)
	}
	loaded, err = cache.ReadReactionSnapshot(ctx, post)
	if err != nil || len(loaded) != 0 {
		t.Fatalf("remaining membership=%+v err=%v", loaded, err)
	}
	if count, err := cache.client.Exists(ctx, reactionStream, "feed:unrelated").Result(); err != nil || count != 2 {
		t.Fatal("clear touched unrelated keys")
	}
}
