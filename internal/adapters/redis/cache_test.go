package redis

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

func testCache(t *testing.T) (*Cache, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: server.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	return &Cache{&Redis{client: client, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}, server
}

func TestRepliesCacheMissAndZero(t *testing.T) {
	cache, server := testCache(t)
	ctx := context.Background()
	missing, zero, populated := uuid.New(), uuid.New(), uuid.New()
	for id, count := range map[uuid.UUID]int64{zero: 0, populated: 7} {
		if err := cache.SetPostRepliesCount(ctx, id, count, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	counts, err := cache.GetPostsRepliesCount(ctx, []uuid.UUID{missing, zero, populated})
	if err != nil || len(counts) != 2 || counts[populated] != 7 {
		t.Fatalf("counts=%v err=%v", counts, err)
	}
	if _, ok := counts[missing]; ok {
		t.Fatal("cache miss must not be reported as zero")
	}
	if value, ok := counts[zero]; !ok || value != 0 {
		t.Fatal("cached zero must be present")
	}
	server.FastForward(2 * time.Minute)
	counts, err = cache.GetPostsRepliesCount(ctx, []uuid.UUID{zero, populated})
	if err != nil || len(counts) != 0 {
		t.Fatalf("expired counts=%v err=%v", counts, err)
	}
}

func TestRepliesCacheErrors(t *testing.T) {
	for _, badValue := range []string{"wrong-type", "invalid-number"} {
		t.Run(badValue, func(t *testing.T) {
			cache, _ := testCache(t)
			ctx := context.Background()
			bad, missing := uuid.New(), uuid.New()
			key := "post:replies:count:" + bad.String()
			var err error
			if badValue == "wrong-type" {
				err = cache.client.SAdd(ctx, key, "member").Err()
			} else {
				err = cache.client.Set(ctx, key, "not-a-number", 0).Err()
			}
			if err != nil {
				t.Fatal(err)
			}
			// A preceding redis.Nil must not hide errors in later commands.
			if _, err := cache.GetPostsRepliesCount(ctx, []uuid.UUID{missing, bad}); err == nil {
				t.Fatal("expected malformed cache data to fail")
			}
		})
	}
	cache, _ := testCache(t)
	if err := cache.client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.GetPostsRepliesCount(context.Background(), []uuid.UUID{uuid.New()}); err == nil {
		t.Fatal("expected closed client error")
	}
}

func TestEmptyFeedAndCounts(t *testing.T) {
	cache, _ := testCache(t)
	ctx, user := context.Background(), uuid.New()
	state, err := cache.GetFeedState(ctx, user)
	if err != nil || len(state.IDs) != 0 || len(state.Seen) != 0 || state.Cursor != nil {
		t.Fatalf("empty feed=%+v err=%v", state, err)
	}
	counts, err := cache.GetPostsRepliesCount(ctx, nil)
	if err != nil || counts == nil || len(counts) != 0 {
		t.Fatalf("counts=%v err=%v", counts, err)
	}
}

func TestReactionSwitchAndConcurrency(t *testing.T) {
	cache, _ := testCache(t)
	ctx, user, post := context.Background(), uuid.New(), uuid.New()
	assertReaction := func(wantLike, wantDislike bool) {
		t.Helper()
		liked, err := cache.IsLiked(ctx, user, post)
		if err != nil {
			t.Fatal(err)
		}
		disliked, err := cache.IsDisliked(ctx, user, post)
		if err != nil {
			t.Fatal(err)
		}
		if liked != wantLike || disliked != wantDislike {
			t.Fatalf("like=%v dislike=%v; want %v/%v", liked, disliked, wantLike, wantDislike)
		}
	}
	for i := 0; i < 2; i++ {
		if err := cache.AddLike(ctx, user, post); err != nil {
			t.Fatal(err)
		}
	}
	assertReaction(true, false)
	if err := cache.AddDislike(ctx, user, post); err != nil {
		t.Fatal(err)
	}
	assertReaction(false, true)
	if err := cache.AddLike(ctx, user, post); err != nil {
		t.Fatal(err)
	}
	assertReaction(true, false)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var err error
			if i%2 == 0 {
				err = cache.AddLike(ctx, user, post)
			} else {
				err = cache.AddDislike(ctx, user, post)
			}
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	likes, err := cache.GetPostsLikes(ctx, []uuid.UUID{post})
	if err != nil {
		t.Fatal(err)
	}
	dislikes, err := cache.GetPostsDislikes(ctx, []uuid.UUID{post})
	if err != nil {
		t.Fatal(err)
	}
	if likes[post]+dislikes[post] != 1 {
		t.Fatalf("likes=%v dislikes=%v", likes, dislikes)
	}
	for i := 0; i < 2; i++ {
		if err := cache.RemoveLike(ctx, user, post); err != nil {
			t.Fatal(err)
		}
		if err := cache.RemoveDislike(ctx, user, post); err != nil {
			t.Fatal(err)
		}
	}
	assertReaction(false, false)
}

func TestReactionWrongTypePreservesExistingReaction(t *testing.T) {
	cache, _ := testCache(t)
	ctx, user, post := context.Background(), uuid.New(), uuid.New()
	if err := cache.AddDislike(ctx, user, post); err != nil {
		t.Fatal(err)
	}
	if err := cache.client.Set(ctx, "post:likes:"+post.String(), "invalid", 0).Err(); err != nil {
		t.Fatal(err)
	}
	err := cache.AddLike(ctx, user, post)
	if err == nil || !strings.Contains(err.Error(), "WRONGTYPE") {
		t.Fatalf("unexpected error: %v", err)
	}
	disliked, err := cache.IsDisliked(ctx, user, post)
	if err != nil || !disliked {
		t.Fatalf("old reaction was lost: disliked=%v err=%v", disliked, err)
	}
}
