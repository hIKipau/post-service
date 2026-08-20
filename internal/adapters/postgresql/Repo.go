package postgresql

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Repo struct {
	*PostgreSQL
}

type Post struct {
	ID       uuid.UUID
	AuthorID uuid.UUID

	Text string

	RootID   *uuid.UUID
	ParentID *uuid.UUID

	ReplyCount int64
	LikeCount  int64

	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}

type FeedCandidate struct {
	PostID       uuid.UUID
	AuthorID     uuid.UUID
	CreatedAt    time.Time
	LikesCount   int64
	RepliesCount int64
}

func NewPostRepo(db *PostgreSQL) *Repo {
	return &Repo{PostgreSQL: db}
}

func (repo *Repo) CreatePost(
	ctx context.Context,
	post Post,
) error {

	repo.logger.Debug(
		"received post creation parameters",
		"post_id", post.ID,
		"author_id", post.AuthorID,
		"root_id", post.RootID,
		"parent_id", post.ParentID,
		"text_length", len(post.Text),
	)

	tx, err := repo.pool.Begin(ctx)
	if err != nil {
		repo.logger.Error(
			"failed to begin post creation transaction",
			"error", err,
			"post_id", post.ID,
		)

		return fmt.Errorf(
			"begin transaction to create post %s: %w",
			post.ID,
			err,
		)
	}
	defer tx.Rollback(ctx)

	const insertQuery = `
		INSERT INTO posts (
			id,
			author_id,
			text,
			root_id,
			parent_id,
			reply_count,
			like_count,
			created_at,
			updated_at,
			deleted_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`

	result, err := tx.Exec(
		ctx,
		insertQuery,
		post.ID,
		post.AuthorID,
		post.Text,
		post.RootID,
		post.ParentID,
		post.ReplyCount,
		post.LikeCount,
		post.CreatedAt,
		post.UpdatedAt,
		post.DeletedAt,
	)
	if err != nil {
		repo.logger.Error(
			"failed to insert post into database",
			"error", err,
			"post_id", post.ID,
			"author_id", post.AuthorID,
		)

		return fmt.Errorf(
			"insert post %s into database: %w",
			post.ID,
			err,
		)
	}

	if result.RowsAffected() != 1 {
		repo.logger.Error(
			"post insertion affected an unexpected number of rows",
			"post_id", post.ID,
			"affected_rows", result.RowsAffected(),
		)

		return fmt.Errorf(
			"insert post %s: expected 1 affected row, received %d",
			post.ID,
			result.RowsAffected(),
		)
	}

	repo.logger.Debug(
		"post inserted into database",
		"post_id", post.ID,
		"parent_id", post.ParentID,
	)

	if post.ParentID != nil {
		const incrementQuery = `
			UPDATE posts
			SET reply_count = reply_count + 1
			WHERE id = $1
			  AND deleted_at IS NULL
		`

		result, err = tx.Exec(
			ctx,
			incrementQuery,
			post.ParentID,
		)
		if err != nil {
			repo.logger.Error(
				"failed to increment parent post reply count",
				"error", err,
				"post_id", post.ID,
				"parent_id", post.ParentID,
			)

			return fmt.Errorf(
				"increment reply count of parent post %s after creating reply %s: %w",
				*post.ParentID,
				post.ID,
				err,
			)
		}

		if result.RowsAffected() != 1 {
			repo.logger.Debug(
				"parent post reply count was not updated",
				"post_id", post.ID,
				"parent_id", post.ParentID,
				"affected_rows", result.RowsAffected(),
			)

			return fmt.Errorf(
				"increment reply count of parent post %s: parent post does not exist or is deleted",
				*post.ParentID,
			)
		}

		repo.logger.Debug(
			"parent post reply count incremented",
			"post_id", post.ID,
			"parent_id", post.ParentID,
		)
	}

	if err := tx.Commit(ctx); err != nil {
		repo.logger.Error(
			"failed to commit post creation transaction",
			"error", err,
			"post_id", post.ID,
			"parent_id", post.ParentID,
		)

		return fmt.Errorf(
			"commit creation transaction for post %s: %w",
			post.ID,
			err,
		)
	}

	repo.logger.Debug(
		"completed post creation",
		"post_id", post.ID,
		"author_id", post.AuthorID,
		"root_id", post.RootID,
		"parent_id", post.ParentID,
	)

	return nil
}

func (repo *Repo) DeletePost(
	ctx context.Context,
	postID uuid.UUID,
	authorID uuid.UUID,
) error {
	repo.logger.Debug(
		"received post deletion parameters",
		"post_id", postID,
		"author_id", authorID,
	)

	tx, err := repo.pool.Begin(ctx)
	if err != nil {
		repo.logger.Error(
			"failed to begin post deletion transaction",
			"error", err,
			"post_id", postID,
		)

		return fmt.Errorf(
			"begin transaction to delete post %s: %w",
			postID,
			err,
		)
	}
	defer tx.Rollback(ctx)

	const deleteQuery = `
		UPDATE posts
		SET
			text = '',
			deleted_at = now(),
			updated_at = now()
		WHERE id = $1
		  AND author_id = $2
		  AND deleted_at IS NULL
		RETURNING parent_id
	`

	var parentID *uuid.UUID

	err = tx.QueryRow(
		ctx,
		deleteQuery,
		postID,
		authorID,
	).Scan(&parentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			repo.logger.Debug(
				"post deletion did not affect any rows",
				"post_id", postID,
				"author_id", authorID,
			)

			return fmt.Errorf(
				"delete post %s: post does not exist, is already deleted, or belongs to another author",
				postID,
			)
		}

		repo.logger.Error(
			"failed to soft delete post",
			"error", err,
			"post_id", postID,
			"author_id", authorID,
		)

		return fmt.Errorf(
			"execute soft delete query for post %s: %w",
			postID,
			err,
		)
	}

	repo.logger.Debug(
		"post marked as deleted",
		"post_id", postID,
		"parent_id", parentID,
	)

	if parentID != nil {
		const decrementQuery = `
			UPDATE posts
			SET reply_count = GREATEST(reply_count - 1, 0)
			WHERE id = $1
			  AND deleted_at IS NULL
		`

		result, err := tx.Exec(ctx, decrementQuery, parentID)
		if err != nil {
			repo.logger.Error(
				"failed to decrement parent post reply count",
				"error", err,
				"post_id", postID,
				"parent_id", parentID,
			)

			return fmt.Errorf(
				"decrement reply count for parent post %s after deleting reply %s: %w",
				*parentID,
				postID,
				err,
			)
		}

		if result.RowsAffected() != 1 {
			repo.logger.Debug(
				"parent reply count was not updated",
				"post_id", postID,
				"parent_id", parentID,
				"affected_rows", result.RowsAffected(),
			)

			return fmt.Errorf(
				"decrement reply count for parent post %s: parent post does not exist or is deleted",
				*parentID,
			)
		}

		repo.logger.Debug(
			"parent post reply count decremented",
			"post_id", postID,
			"parent_id", parentID,
		)
	}

	if err := tx.Commit(ctx); err != nil {
		repo.logger.Error(
			"failed to commit post deletion transaction",
			"error", err,
			"post_id", postID,
			"parent_id", parentID,
		)

		return fmt.Errorf(
			"commit deletion transaction for post %s: %w",
			postID,
			err,
		)
	}

	repo.logger.Debug(
		"completed post deletion",
		"post_id", postID,
		"author_id", authorID,
		"parent_id", parentID,
	)

	return nil
}

func (repo *Repo) UpdatePost(
	ctx context.Context,
	postID uuid.UUID,
	authorID uuid.UUID,
	text string,
) error {
	repo.logger.Debug(
		"received post update parameters",
		"post_id", postID,
		"author_id", authorID,
		"text_length", len(text),
	)

	const query = `
		UPDATE posts
		SET
			text = $1,
			updated_at = now()
		WHERE id = $2
		  AND author_id = $3
		  AND deleted_at IS NULL
	`

	result, err := repo.pool.Exec(
		ctx,
		query,
		text,
		postID,
		authorID,
	)
	if err != nil {
		repo.logger.Error(
			"failed to execute post update query",
			"error", err,
			"post_id", postID,
			"author_id", authorID,
		)

		return fmt.Errorf(
			"execute update query for post %s: %w",
			postID,
			err,
		)
	}

	if result.RowsAffected() == 0 {
		repo.logger.Debug(
			"post update did not affect any rows",
			"post_id", postID,
			"author_id", authorID,
		)

		return fmt.Errorf(
			"update post %s: post does not exist, is deleted, or belongs to another author",
			postID,
		)
	}

	repo.logger.Debug(
		"completed post update",
		"post_id", postID,
		"author_id", authorID,
		"affected_rows", result.RowsAffected(),
	)

	return nil
}

func (repo *Repo) GetChildrenPosts(
	ctx context.Context,
	parentID uuid.UUID,
	pageSize int64,
	page int64,
) ([]Post, error) {

	repo.logger.Debug(
		"received child posts retrieval parameters",
		"parent_id", parentID,
		"page", page,
		"page_size", pageSize,
	)

	if page < 0 {
		repo.logger.Debug(
			"child posts retrieval rejected because page is negative",
			"page", page,
		)

		return nil, fmt.Errorf(
			"retrieve child posts: page must not be negative, received %d",
			page,
		)
	}

	if pageSize <= 0 {
		repo.logger.Debug(
			"child posts retrieval rejected because page size is invalid",
			"page_size", pageSize,
		)

		return nil, fmt.Errorf(
			"retrieve child posts: page size must be greater than zero, received %d",
			pageSize,
		)
	}

	offset := page * pageSize

	const query = `
		SELECT
			id,
			author_id,
			text,
			root_id,
			parent_id,
			reply_count,
			like_count,
			created_at,
			updated_at,
			deleted_at
		FROM posts
		WHERE parent_id = $1
		  AND deleted_at IS NULL
		ORDER BY
			like_count DESC,
			created_at DESC,
			id DESC
		LIMIT $2
		OFFSET $3
	`

	rows, err := repo.pool.Query(
		ctx,
		query,
		parentID,
		pageSize,
		offset,
	)
	if err != nil {
		repo.logger.Error(
			"failed to execute child posts retrieval query",
			"error", err,
			"parent_id", parentID,
			"page", page,
			"page_size", pageSize,
			"offset", offset,
		)

		return nil, fmt.Errorf(
			"execute query to retrieve child posts for parent %s: %w",
			parentID,
			err,
		)
	}
	defer rows.Close()

	posts := make([]Post, 0, pageSize)

	for rows.Next() {
		var post Post

		if err := rows.Scan(
			&post.ID,
			&post.AuthorID,
			&post.Text,
			&post.RootID,
			&post.ParentID,
			&post.ReplyCount,
			&post.LikeCount,
			&post.CreatedAt,
			&post.UpdatedAt,
			&post.DeletedAt,
		); err != nil {
			repo.logger.Error(
				"failed to read child post fields from database row",
				"error", err,
				"parent_id", parentID,
			)

			return nil, fmt.Errorf(
				"read child post fields from database result for parent %s: %w",
				parentID,
				err,
			)
		}

		posts = append(posts, post)
	}

	if err := rows.Err(); err != nil {
		repo.logger.Error(
			"failed while iterating over child posts",
			"error", err,
			"parent_id", parentID,
		)

		return nil, fmt.Errorf(
			"iterate over child posts returned for parent %s: %w",
			parentID,
			err,
		)
	}

	repo.logger.Debug(
		"completed child posts retrieval",
		"parent_id", parentID,
		"page", page,
		"page_size", pageSize,
		"offset", offset,
		"retrieved_count", len(posts),
	)

	return posts, nil
}

func (repo *Repo) GetPostsByIDs(
	ctx context.Context,
	postIDs []uuid.UUID,
) ([]Post, error) {

	repo.logger.Debug(
		"received post IDs for retrieval",
		"post_ids", postIDs,
	)

	if len(postIDs) == 0 {
		repo.logger.Debug("posts retrieval skipped because ID list is empty")

		return []Post{}, nil
	}

	const query = `SELECT
    	p.id,
    	p.author_id,
    	p.text,
    	p.root_id,
    	p.parent_id,
    	p.reply_count,
   		p.like_count,
    	p.created_at,
    	p.updated_at,
    	p.deleted_at
	FROM unnest($1::uuid[]) WITH ORDINALITY AS ids(id, position)
	JOIN posts p ON p.id = ids.id
	ORDER BY ids.position
	`

	rows, err := repo.pool.Query(ctx, query, postIDs)
	if err != nil {
		repo.logger.Error(
			"failed to execute posts retrieval query",
			"error", err,
			"posts_count", len(postIDs),
		)

		return nil, fmt.Errorf(
			"execute query to retrieve posts by IDs: %w",
			err,
		)
	}
	defer rows.Close()

	result := make([]Post, 0, len(postIDs))

	for rows.Next() {
		var post Post

		if err := rows.Scan(
			&post.ID,
			&post.AuthorID,
			&post.Text,
			&post.RootID,
			&post.ParentID,
			&post.ReplyCount,
			&post.LikeCount,
			&post.CreatedAt,
			&post.UpdatedAt,
			&post.DeletedAt,
		); err != nil {
			repo.logger.Error(
				"failed to read post fields from database row",
				"error", err,
			)

			return nil, fmt.Errorf(
				"read post fields from database result: %w",
				err,
			)
		}

		result = append(result, post)
	}

	if err := rows.Err(); err != nil {
		repo.logger.Error(
			"failed while iterating over retrieved posts",
			"error", err,
		)

		return nil, fmt.Errorf(
			"iterate over posts returned by database: %w",
			err,
		)
	}

	repo.logger.Debug(
		"completed posts retrieval by IDs",
		"requested_post_ids", postIDs,
		"retrieved_posts", result,
	)

	return result, nil
}

func (repo *Repo) GetFeedCandidatesIDs(ctx context.Context, userID uuid.UUID, limit int64) ([]FeedCandidate, error) {

	if limit >= 0 {
		return []FeedCandidate{}, fmt.Errorf("cant getting feed candidates, limit is negative: %d", limit)
	}

	const query = `
		SELECT
    	id,
    	author_id,
    	created_at,
    	likes_count,
    	replies_count
	FROM posts
	WHERE author_id <> $1
	ORDER BY created_at DESC, id DESC
	LIMIT $2;
	`

	rows, err := repo.pool.Query(ctx, query, userID, limit)
	if err != nil {
		return []FeedCandidate{}, fmt.Errorf("cant exec query: %w", err)
	}
	defer rows.Close()

	candidates := make([]FeedCandidate, 0, limit)

	for rows.Next() {
		var candidate FeedCandidate
		err = rows.Scan(
			&candidate.PostID,
			&candidate.AuthorID,
			&candidate.CreatedAt,
			&candidate.LikesCount,
			&candidate.RepliesCount,
		)
		if err != nil {
			return nil, fmt.Errorf("cant scan feed candidate: %w", err)
		}
		candidates = append(candidates, candidate)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("cant iterate feed candidates: %w", err)
	}

	return candidates, nil
}
