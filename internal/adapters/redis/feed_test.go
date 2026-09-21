package redis

import (
	"context"
	"reflect"
	"testing"
	"time"

	"post-service/internal/domain"

	"github.com/google/uuid"
)

// TestFeedCommitChecksRevision verifies atomic replacement, cursor persistence and stale-write rejection.
func TestFeedCommitChecksRevision(t *testing.T) {
	cache, server := testCache(t)
	ctx, user := context.Background(), uuid.New()
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	initial, err := cache.GetFeedState(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	initial.IDs = []uuid.UUID{b, c}
	initial.Cursor = &domain.FeedCursor{CreatedAt: time.Now().UTC(), PostID: c}
	ok, err := cache.CommitFeed(ctx, user, initial, []uuid.UUID{a}, time.Hour, 3*time.Hour)
	if err != nil || !ok {
		t.Fatalf("commit=%v err=%v", ok, err)
	}
	state, err := cache.GetFeedState(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state.IDs, initial.IDs) || !reflect.DeepEqual(state.Cursor, initial.Cursor) || state.Revision == "" {
		t.Fatalf("unexpected state: %+v", state)
	}
	if _, ok := state.Seen[a]; !ok || len(state.Seen) != 1 {
		t.Fatalf("seen=%v", state.Seen)
	}
	ok, err = cache.CommitFeed(ctx, user, initial, []uuid.UUID{b}, time.Hour, 3*time.Hour)
	if err != nil || ok {
		t.Fatalf("stale commit=%v err=%v", ok, err)
	}
	unchanged, err := cache.GetFeedState(ctx, user)
	if err != nil || !reflect.DeepEqual(state, unchanged) {
		t.Fatalf("stale commit changed state: %+v, %v", unchanged, err)
	}
	state.IDs = nil
	state.Cursor = nil
	ok, err = cache.CommitFeed(ctx, user, state, []uuid.UUID{b, c}, time.Hour, 3*time.Hour)
	if err != nil || !ok {
		t.Fatalf("drain=%v err=%v", ok, err)
	}
	if server.Exists(feedKeys(user)[0]) {
		t.Fatal("drained queue still exists")
	}
	server.FastForward(2 * time.Hour)
	expired, err := cache.GetFeedState(ctx, user)
	if err != nil || expired.Revision != "" || expired.Cursor != nil || len(expired.Seen) != 3 {
		t.Fatalf("queue metadata expiry=%+v err=%v", expired, err)
	}
	server.FastForward(2 * time.Hour)
	expired, err = cache.GetFeedState(ctx, user)
	if err != nil || len(expired.Seen) != 0 {
		t.Fatalf("seen expiry=%+v err=%v", expired, err)
	}
}

// TestFeedCommitWrongTypeDoesNotPartiallyWrite verifies preflight validation before Lua mutations.
func TestFeedCommitWrongTypeDoesNotPartiallyWrite(t *testing.T) {
	cache, _ := testCache(t)
	ctx, user := context.Background(), uuid.New()
	keys := feedKeys(user)
	id := uuid.New()
	if err := cache.client.RPush(ctx, keys[0], id.String()).Err(); err != nil {
		t.Fatal(err)
	}
	if err := cache.client.Set(ctx, keys[3], "not-a-set", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.CommitFeed(ctx, user, domain.FeedState{}, []uuid.UUID{id}, time.Hour, time.Hour); err == nil {
		t.Fatal("expected malformed seen key to fail")
	}
	ids, err := cache.client.LRange(ctx, keys[0], 0, -1).Result()
	if err != nil || len(ids) != 1 || ids[0] != id.String() {
		t.Fatalf("queue mutated: %v, %v", ids, err)
	}
	if count, err := cache.client.Exists(ctx, keys[1], keys[2]).Result(); err != nil || count != 0 {
		t.Fatalf("metadata mutated: %d, %v", count, err)
	}
}

// TestFeedStateValidation rejects malformed stored IDs/cursors and invalid expiry durations.
func TestFeedStateValidation(t *testing.T) {
	for _, part := range []string{"queue", "cursor", "seen"} {
		t.Run(part, func(t *testing.T) {
			cache, _ := testCache(t)
			ctx, user := context.Background(), uuid.New()
			keys := feedKeys(user)
			var err error
			switch part {
			case "queue":
				err = cache.client.RPush(ctx, keys[0], "invalid").Err()
			case "cursor":
				err = cache.client.Set(ctx, keys[1], "{}", time.Hour).Err()
			case "seen":
				err = cache.client.SAdd(ctx, keys[3], "invalid").Err()
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := cache.GetFeedState(ctx, user); err == nil {
				t.Fatal("expected corrupt state error")
			}
		})
	}
	cache, _ := testCache(t)
	if _, err := cache.CommitFeed(context.Background(), uuid.New(), domain.FeedState{}, nil, 0, time.Hour); err == nil {
		t.Fatal("expected invalid TTL error")
	}
}
