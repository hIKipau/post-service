package postgresql_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"post-service/internal/adapters/postgresql"
	"post-service/internal/domain"
	"post-service/internal/migrator"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"
)

// isolatedDatabase creates a unique schema in an explicitly supplied test database and removes only that schema.
func isolatedDatabase(t *testing.T) (context.Context, string, *pgx.Conn) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to a dedicated PostgreSQL test database to run migrations integration tests")
	}
	u, err := url.Parse(databaseURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("TEST_DATABASE_URL must be a postgres:// or postgresql:// URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	schema := "post_service_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("remove isolated test schema: %v", err)
		}
	})
	query := u.Query()
	// Do not include public: neither Goose nor the repository may touch existing tables.
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	isolatedURL := u.String()
	conn, err := pgx.Connect(ctx, isolatedURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return ctx, isolatedURL, conn
}

// TestMigrationsIntegration checks Up/Down, SQL constraints and real repository queries in an isolated schema.
func TestMigrationsIntegration(t *testing.T) {
	ctx, databaseURL, conn := isolatedDatabase(t)
	provider, err := migrator.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Close() })
	assertVersion(t, ctx, provider, 0)
	results, err := provider.Up(ctx)
	if err != nil || len(results) != 2 {
		t.Fatalf("initial up = %v, %v", results, err)
	}
	assertVersion(t, ctx, provider, 2)
	if results, err := provider.Up(ctx); err != nil || len(results) != 0 {
		t.Fatalf("repeated up = %v, %v", results, err)
	}
	statuses, err := provider.Status(ctx)
	if err != nil || len(statuses) != 2 {
		t.Fatalf("status = %v, %v", statuses, err)
	}
	for _, status := range statuses {
		if status.State != goose.StateApplied {
			t.Fatalf("migration %d is %s", status.Source.Version, status.State)
		}
	}
	var indexCount int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM pg_indexes WHERE schemaname = current_schema() AND tablename = 'posts'").Scan(&indexCount); err != nil || indexCount != 5 {
		t.Fatalf("indexes = %d, %v; want primary key and four query/FK indexes", indexCount, err)
	}

	db, err := postgresql.New(ctx, databaseURL, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	repo := postgresql.NewRepo(db)
	root := testPost("root")
	reply := testPost("reply")
	reply.ParentID, reply.RootID = &root.ID, &root.ID
	nested := testPost("nested reply")
	nested.ParentID, nested.RootID = &reply.ID, &root.ID
	for _, post := range []domain.Post{root, reply, nested} {
		if err := repo.CreatePost(ctx, post); err != nil {
			t.Fatal(err)
		}
	}
	posts, err := repo.GetPostsByIDs(ctx, []uuid.UUID{reply.ID, root.ID, nested.ID})
	if err != nil || len(posts) != 3 {
		t.Fatalf("posts = %v, %v", posts, err)
	}
	if posts[0].ID != reply.ID || posts[1].ID != root.ID || posts[2].ID != nested.ID || posts[0].ReplyCount != 1 || posts[1].ReplyCount != 1 {
		t.Fatalf("unexpected ordering or reply counts: %+v", posts)
	}
	children, err := repo.GetChildrenPosts(ctx, root.ID, 20, 0)
	if err != nil || len(children) != 1 || children[0].ID != reply.ID {
		t.Fatalf("children = %v, %v", children, err)
	}
	// Equal timestamps require the UUID tie breaker to avoid skipped or repeated candidates.
	if _, err := conn.Exec(ctx, "UPDATE posts SET created_at = $1", time.Now().UTC().Truncate(time.Microsecond)); err != nil {
		t.Fatal(err)
	}
	var cursor *domain.FeedCursor
	candidateIDs := make(map[uuid.UUID]struct{})
	reader := uuid.New()
	for i := 0; i < 3; i++ {
		page, err := repo.GetFeedCandidates(ctx, reader, 1, cursor)
		if err != nil || len(page) != 1 {
			t.Fatalf("cursor page %d = %+v, %v", i, page, err)
		}
		if _, exists := candidateIDs[page[0].PostID]; exists {
			t.Fatal("cursor repeated a candidate")
		}
		candidateIDs[page[0].PostID] = struct{}{}
		cursor = &domain.FeedCursor{CreatedAt: page[0].CreatedAt, PostID: page[0].PostID}
	}
	if page, err := repo.GetFeedCandidates(ctx, reader, 1, cursor); err != nil || len(page) != 0 {
		t.Fatalf("exhausted cursor = %+v, %v", page, err)
	}
	root.Text = strings.Repeat("я", 10000)
	if err := repo.UpdatePost(ctx, root); err != nil {
		t.Fatalf("10,000 Unicode characters must be accepted: %v", err)
	}
	if err := repo.DeletePost(ctx, nested.ID, nested.AuthorID); err != nil {
		t.Fatal(err)
	}
	posts, err = repo.GetPostsByIDs(ctx, []uuid.UUID{reply.ID, nested.ID})
	if err != nil || len(posts) != 2 || posts[0].ReplyCount != 0 || posts[1].DeletedAt == nil || posts[1].Text != "" {
		t.Fatalf("soft deletion or reply decrement failed: %+v, %v", posts, err)
	}
	candidates, err := repo.GetFeedCandidates(ctx, root.AuthorID, 100, nil)
	if err != nil || len(candidates) != 1 || candidates[0].PostID != reply.ID {
		t.Fatalf("feed must exclude own/deleted posts: %+v, %v", candidates, err)
	}

	t.Run("constraints", func(t *testing.T) {
		for _, query := range []string{
			"UPDATE posts SET like_count = -1 WHERE id = $1",
			"UPDATE posts SET dislike_count = -1 WHERE id = $1",
			"UPDATE posts SET reply_count = -1 WHERE id = $1",
			"UPDATE posts SET text = '' WHERE id = $1",
			"UPDATE posts SET text = repeat('я', 10001) WHERE id = $1",
			"UPDATE posts SET author_id = '00000000-0000-0000-0000-000000000000' WHERE id = $1",
			"UPDATE posts SET parent_id = id, root_id = id WHERE id = $1",
			"UPDATE posts SET root_id = id WHERE id = $1",
		} {
			_, err := conn.Exec(ctx, query, root.ID)
			assertSQLState(t, err, "23514")
		}
		_, err := conn.Exec(ctx, "UPDATE posts SET parent_id = $2, root_id = $2 WHERE id = $1", reply.ID, uuid.New())
		assertSQLState(t, err, "23503")
		_, err = conn.Exec(ctx, "DELETE FROM posts WHERE id = $1", root.ID)
		assertSQLState(t, err, "23503")
	})

	// Soft-deleting a root must preserve replies and their references.
	if err := repo.DeletePost(ctx, root.ID, root.AuthorID); err != nil {
		t.Fatal(err)
	}
	posts, err = repo.GetPostsByIDs(ctx, []uuid.UUID{root.ID, reply.ID})
	if err != nil || len(posts) != 2 || posts[0].DeletedAt == nil || posts[1].DeletedAt != nil {
		t.Fatalf("root deletion damaged the thread: %+v, %v", posts, err)
	}
	lateReply := testPost("reply to deleted root")
	lateReply.ParentID, lateReply.RootID = &root.ID, &root.ID
	if err := repo.CreatePost(ctx, lateReply); !errors.Is(err, domain.ErrPostNotFound) {
		t.Fatalf("reply to deleted parent = %v", err)
	}
	posts, err = repo.GetPostsByIDs(ctx, []uuid.UUID{lateReply.ID})
	if err != nil || len(posts) != 0 {
		t.Fatalf("failed reply transaction left a row: %+v, %v", posts, err)
	}

	if _, err := provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	assertVersion(t, ctx, provider, 1)
	var rowCount int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM posts").Scan(&rowCount); err != nil || rowCount != 3 {
		t.Fatalf("index rollback lost posts: %d, %v", rowCount, err)
	}
	if _, err := provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	assertVersion(t, ctx, provider, 0)
	var absent bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass('posts') IS NULL").Scan(&absent); err != nil || !absent {
		t.Fatalf("table remains after full rollback: %v", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("reapply after rollback: %v", err)
	}
	assertVersion(t, ctx, provider, 2)
}

// testPost builds an active post with unique IDs and explicit application-style timestamps.
func testPost(text string) domain.Post {
	now := time.Now().UTC()
	return domain.Post{ID: uuid.New(), AuthorID: uuid.New(), Text: text, CreatedAt: now, UpdatedAt: now}
}

// assertVersion checks Goose's recorded schema version.
func assertVersion(t *testing.T, ctx context.Context, provider *goose.Provider, want int64) {
	t.Helper()
	version, err := provider.GetDBVersion(ctx)
	if err != nil || version != want {
		t.Fatalf("schema version = %d, %v; want %d", version, err, want)
	}
}

// assertSQLState checks PostgreSQL's structured error code rather than locale-dependent text.
func assertSQLState(t *testing.T, err error, want string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != want {
		t.Fatalf("SQL error = %v; want SQLSTATE %s", err, want)
	}
}
