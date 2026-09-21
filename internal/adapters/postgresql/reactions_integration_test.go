package postgresql_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"post-service/internal/adapters/postgresql"
	redisadapter "post-service/internal/adapters/redis"
	"post-service/internal/domain"
	"post-service/internal/migrator"
	"post-service/internal/reactionsync"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// reactionDatabase applies the actual migrations in an isolated schema and opens the real repository.
func reactionDatabase(t *testing.T) (context.Context, *postgresql.Repo, *pgx.Conn) {
	t.Helper()
	ctx, databaseURL, conn := isolatedDatabase(t)
	provider, err := migrator.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		_ = provider.Close()
		t.Fatal(err)
	}
	if err := provider.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := postgresql.New(ctx, databaseURL, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	repo := postgresql.NewRepo(db)
	if err := repo.CheckReactionSchema(ctx); err != nil {
		t.Fatal(err)
	}
	return ctx, repo, conn
}

// TestReactionProjectionIntegration checks real transactions, idempotency, leadership and rollback on counter errors.
func TestReactionProjectionIntegration(t *testing.T) {
	ctx, repo, conn := reactionDatabase(t)
	post := testPost("reactions")
	if err := repo.CreatePost(ctx, post); err != nil {
		t.Fatal(err)
	}
	session, err := repo.AcquireReactionSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	if other, err := repo.AcquireReactionSession(ctx); !errors.Is(err, domain.ErrReactionSyncBusy) {
		if other != nil {
			_ = other.Close(ctx)
		}
		t.Fatalf("second writer acquired leadership: %v", err)
	}
	user := uuid.New()
	like := domain.ReactionEvent{EventID: uuid.New(), PostID: post.ID, UserID: user, Kind: domain.ReactionLike}
	for i := 0; i < 2; i++ {
		if err := session.ApplyReaction(ctx, like); err != nil {
			t.Fatal(err)
		}
	}
	assertReactionCounts(t, ctx, conn, post.ID, 1, 0)
	dislike := like
	dislike.EventID = uuid.New()
	dislike.Kind = domain.ReactionDislike
	if err := session.ApplyReaction(ctx, dislike); err != nil {
		t.Fatal(err)
	}
	if err := session.ApplyReaction(ctx, like); err != nil {
		t.Fatal(err)
	}
	assertReactionCounts(t, ctx, conn, post.ID, 0, 1)
	remove := like
	remove.EventID = uuid.New()
	remove.Kind = domain.ReactionNone
	// Force a constraint failure after membership/receipt changes; both must roll back.
	if _, err := conn.Exec(ctx, "UPDATE posts SET dislike_count = 0 WHERE id = $1", post.ID); err != nil {
		t.Fatal(err)
	}
	if err := session.ApplyReaction(ctx, remove); err == nil {
		t.Fatal("expected counter check violation")
	}
	var receipts int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM reaction_sync_events WHERE event_id = $1", remove.EventID).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("failed event receipt persisted: %d %v", receipts, err)
	}
	stored, err := session.ReadReactions(ctx, post.ID)
	if err != nil || len(stored) != 1 || stored[0].Kind != domain.ReactionDislike {
		t.Fatalf("failed event changed membership: %+v %v", stored, err)
	}
	if err := session.ReconcileReactionCounts(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.ApplyReaction(ctx, remove); err != nil {
		t.Fatal(err)
	}
	assertReactionCounts(t, ctx, conn, post.ID, 0, 0)
	missing := like
	missing.EventID = uuid.New()
	missing.PostID = uuid.New()
	if err := session.ApplyReaction(ctx, missing); err != nil {
		t.Fatal(err)
	}
	var outcome string
	if err := conn.QueryRow(ctx, "SELECT outcome FROM reaction_sync_events WHERE event_id = $1", missing.EventID).Scan(&outcome); err != nil || outcome != "post_missing" {
		t.Fatalf("missing-post outcome=%s %v", outcome, err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
	replacement, err := repo.AcquireReactionSession(ctx)
	if err != nil {
		t.Fatalf("leadership not released: %v", err)
	}
	_ = replacement.Close(ctx)
}

// TestReactionSyncRoundTripIntegration verifies live Redis events, legacy import and offline reconstruction against PostgreSQL.
func TestReactionSyncRoundTripIntegration(t *testing.T) {
	ctx, repo, conn := reactionDatabase(t)
	post := testPost("reaction round trip")
	if err := repo.CreatePost(ctx, post); err != nil {
		t.Fatal(err)
	}
	session, err := repo.AcquireReactionSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	server := miniredis.RunT(t)
	rdb, err := redisadapter.New(ctx, "redis://"+server.Addr(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	cache := redisadapter.NewCache(rdb)
	user := uuid.New()
	if err := cache.RestoreReactionSnapshot(ctx, post.ID, []domain.Reaction{{UserID: user, Kind: domain.ReactionLike}}); err != nil {
		t.Fatal(err)
	}
	legacy, err := cache.ReadReactionSnapshot(ctx, post.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := session.ReplaceReactions(ctx, post.ID, legacy); err != nil {
			t.Fatal(err)
		}
	}
	assertReactionCounts(t, ctx, conn, post.ID, 1, 0)
	if err := cache.AddDislike(ctx, user, post.ID); err != nil {
		t.Fatal(err)
	}
	if err := cache.EnsureReactionGroup(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, err := reactionsync.ProcessNext(ctx, session, cache); err != nil || !ok {
		t.Fatalf("projection=%v %v", ok, err)
	}
	assertReactionCounts(t, ctx, conn, post.ID, 0, 1)
	if err := cache.BeginReactionMaintenance(ctx, "restore"); err != nil {
		t.Fatal(err)
	}
	if err := cache.ClearReactionMembership(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := session.ReadReactions(ctx, post.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.RestoreReactionSnapshot(ctx, post.ID, stored); err != nil {
		t.Fatal(err)
	}
	if err := cache.EndReactionMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	if liked, err := cache.IsLiked(ctx, user, post.ID); err != nil || liked {
		t.Fatal("restored an obsolete like")
	}
	if disliked, err := cache.IsDisliked(ctx, user, post.ID); err != nil || !disliked {
		t.Fatal("failed to restore dislike membership")
	}
	if event, err := cache.NextReactionEvent(ctx); err != nil || event != nil {
		t.Fatalf("restore emitted events: %+v %v", event, err)
	}
}

// assertReactionCounts checks the SQL aggregate values after a projection or maintenance operation.
func assertReactionCounts(t *testing.T, ctx context.Context, conn *pgx.Conn, postID uuid.UUID, likes, dislikes int64) {
	t.Helper()
	var actualLikes, actualDislikes int64
	if err := conn.QueryRow(ctx, "SELECT like_count, dislike_count FROM posts WHERE id = $1", postID).Scan(&actualLikes, &actualDislikes); err != nil {
		t.Fatal(err)
	}
	if actualLikes != likes || actualDislikes != dislikes {
		t.Fatalf("counts=%d/%d want=%d/%d", actualLikes, actualDislikes, likes, dislikes)
	}
}
