package postgresql

import (
	"context"
	"errors"
	"fmt"

	"post-service/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const reactionSyncLockID int64 = 0x5053545245414354

// ReactionSession owns a dedicated connection and the database-wide reaction writer lock.
// All writes use this connection; losing it also loses leadership, never silently reconnecting.
type ReactionSession struct{ conn *pgx.Conn }

// CheckReactionSchema fails startup early when the required reaction migration is missing.
func (repo *Repo) CheckReactionSchema(ctx context.Context) error {
	_, err := repo.pool.Exec(ctx, `SELECT r.post_id, r.user_id, r.kind, r.updated_at, e.event_id, e.outcome
FROM post_reactions r CROSS JOIN reaction_sync_events e LIMIT 0`)
	if err != nil {
		return fmt.Errorf("reaction schema is unavailable; run migrations: %w", err)
	}
	return nil
}

// AcquireReactionSession elects one ordered writer without reserving a connection from the HTTP pool.
func (repo *Repo) AcquireReactionSession(ctx context.Context) (*ReactionSession, error) {
	conn, err := pgx.ConnectConfig(ctx, repo.pool.Config().ConnConfig.Copy())
	if err != nil {
		return nil, fmt.Errorf("connect reaction writer: %w", err)
	}
	var acquired bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", reactionSyncLockID).Scan(&acquired); err != nil {
		_ = conn.Close(ctx)
		return nil, err
	}
	if !acquired {
		_ = conn.Close(ctx)
		return nil, domain.ErrReactionSyncBusy
	}
	return &ReactionSession{conn: conn}, nil
}

// Close releases leadership by closing its physical session, including when the connection is broken.
func (session *ReactionSession) Close(ctx context.Context) error { return session.conn.Close(ctx) }

// ApplyReaction atomically deduplicates an event, changes membership and adjusts the post counters.
func (session *ReactionSession) ApplyReaction(ctx context.Context, event domain.ReactionEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	tx, err := session.conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, "INSERT INTO reaction_sync_events (event_id) VALUES ($1) ON CONFLICT DO NOTHING", event.EventID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	// Lock the parent row even when no reaction row exists yet; this serializes aggregate updates.
	var postID uuid.UUID
	err = tx.QueryRow(ctx, "SELECT id FROM posts WHERE id = $1 FOR NO KEY UPDATE", event.PostID).Scan(&postID)
	if errors.Is(err, pgx.ErrNoRows) {
		// A physically removed post must not block every subsequent event. Soft-deleted posts still exist.
		if _, err := tx.Exec(ctx, "UPDATE reaction_sync_events SET outcome = 'post_missing' WHERE event_id = $1", event.EventID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	var previous domain.ReactionKind
	err = tx.QueryRow(ctx, "SELECT kind FROM post_reactions WHERE post_id = $1 AND user_id = $2", event.PostID, event.UserID).Scan(&previous)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if event.Kind == domain.ReactionNone {
		_, err = tx.Exec(ctx, "DELETE FROM post_reactions WHERE post_id = $1 AND user_id = $2", event.PostID, event.UserID)
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO post_reactions (post_id, user_id, kind) VALUES ($1, $2, $3)
ON CONFLICT (post_id, user_id) DO UPDATE SET kind = EXCLUDED.kind, updated_at = now()`, event.PostID, event.UserID, event.Kind)
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE posts SET like_count = like_count + $2, dislike_count = dislike_count + $3 WHERE id = $1`,
		event.PostID, reactionDelta(previous, event.Kind, domain.ReactionLike), reactionDelta(previous, event.Kind, domain.ReactionDislike))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// reactionDelta returns the change to one aggregate when membership switches to an absolute state.
func reactionDelta(previous, next, counted domain.ReactionKind) int64 {
	var delta int64
	if previous == counted {
		delta--
	}
	if next == counted {
		delta++
	}
	return delta
}
