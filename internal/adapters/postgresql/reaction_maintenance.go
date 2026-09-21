package postgresql

import (
	"context"
	"errors"

	"post-service/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ListReactionPosts enumerates existing posts, including soft-deleted ones, in bounded maintenance batches.
func (session *ReactionSession) ListReactionPosts(ctx context.Context, after uuid.UUID) ([]uuid.UUID, error) {
	rows, err := session.conn.Query(ctx, "SELECT id FROM posts WHERE id > $1 ORDER BY id LIMIT 100", after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]uuid.UUID, 0, 100)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ReadReactions exports durable membership for one post while the maintenance process owns the writer lock.
func (session *ReactionSession) ReadReactions(ctx context.Context, postID uuid.UUID) ([]domain.Reaction, error) {
	rows, err := session.conn.Query(ctx, "SELECT user_id, kind FROM post_reactions WHERE post_id = $1 ORDER BY user_id", postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.Reaction, 0)
	for rows.Next() {
		var reaction domain.Reaction
		if err := rows.Scan(&reaction.UserID, &reaction.Kind); err != nil {
			return nil, err
		}
		result = append(result, reaction)
	}
	return result, rows.Err()
}

// ReplaceReactions imports one complete Redis snapshot and resets its SQL counters in the same transaction.
// API writers must be stopped throughout maintenance; this operation intentionally replaces membership.
func (session *ReactionSession) ReplaceReactions(ctx context.Context, postID uuid.UUID, reactions []domain.Reaction) error {
	seen := make(map[uuid.UUID]struct{}, len(reactions))
	rows := make([][]any, 0, len(reactions))
	var likes, dislikes int64
	for _, reaction := range reactions {
		if reaction.UserID == uuid.Nil || (reaction.Kind != domain.ReactionLike && reaction.Kind != domain.ReactionDislike) {
			return errors.New("invalid reaction snapshot")
		}
		if _, exists := seen[reaction.UserID]; exists {
			return errors.New("duplicate user in reaction snapshot")
		}
		seen[reaction.UserID] = struct{}{}
		if reaction.Kind == domain.ReactionLike {
			likes++
		} else {
			dislikes++
		}
		rows = append(rows, []any{postID, reaction.UserID, reaction.Kind})
	}
	tx, err := session.conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var id uuid.UUID
	if err := tx.QueryRow(ctx, "SELECT id FROM posts WHERE id = $1 FOR NO KEY UPDATE", postID).Scan(&id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM post_reactions WHERE post_id = $1", postID); err != nil {
		return err
	}
	if len(rows) > 0 {
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"post_reactions"}, []string{"post_id", "user_id", "kind"}, pgx.CopyFromRows(rows)); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, "UPDATE posts SET like_count = $2, dislike_count = $3 WHERE id = $1", postID, likes, dislikes); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ReconcileReactionCounts repairs historical snapshot counters from durable membership before offline replay.
func (session *ReactionSession) ReconcileReactionCounts(ctx context.Context) error {
	_, err := session.conn.Exec(ctx, `UPDATE posts p SET
like_count = (SELECT count(*) FROM post_reactions r WHERE r.post_id = p.id AND r.kind = 1),
dislike_count = (SELECT count(*) FROM post_reactions r WHERE r.post_id = p.id AND r.kind = -1)`)
	return err
}
