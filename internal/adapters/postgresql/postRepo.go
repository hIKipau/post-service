package postgresql

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type PostRepo struct {
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

func NewPostRepo(db *PostgreSQL) *PostRepo {
	return &PostRepo{PostgreSQL: db}
}

func (repo *PostRepo) CreatePost(
	ctx context.Context,
	post Post,
) error {
	tx, err := repo.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("error beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	const query = `
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
		query,
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
		return fmt.Errorf("insert post: %w", err)
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("insert post: expected 1 affected row")
	}

	if post.ParentID != nil {
		const incrementQuery = `UPDATE posts
			SET reply_count = reply_count + 1
			WHERE id = $1
			  AND deleted_at IS NULL`

		result, err := tx.Exec(ctx, incrementQuery, post.ParentID)
		if err != nil {
			return fmt.Errorf("insert post: %w", err)
		}
		if result.RowsAffected() != 1 {
			return fmt.Errorf("insert post: expected 1 affected row")
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

func (repo *PostRepo) DeletePost(
	ctx context.Context,
	postID uuid.UUID,
	authorID uuid.UUID,
) error {
	tx, err := repo.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("error beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	const query = `
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
		query,
		postID,
		authorID,
	).Scan(&parentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("post not found or access denied")
		}
		return fmt.Errorf("soft delete post: %w", err)
	}

	if parentID != nil {
		const decrementQuery = `
			UPDATE posts
			SET reply_count = GREATEST(reply_count - 1, 0)
			WHERE id = $1
			  AND deleted_at IS NULL
		`
		result, err := tx.Exec(ctx, decrementQuery, parentID)
		if err != nil {
			return fmt.Errorf("decrement parent reply count: %w", err)
		}
		if result.RowsAffected() != 1 {
			return fmt.Errorf("parent post not found")
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil

}

func (repo *PostRepo) UpdatePost(ctx context.Context, postID uuid.UUID, authorID uuid.UUID, text string) error {
	const query = `
		UPDATE posts
		SET
			text = $1,
			updated_at = now()
		WHERE id = $2
		  AND author_id = $3
		  AND deleted_at IS NULL
	`

	result, err := repo.pool.Exec(ctx, query, text, postID, authorID)
	if err != nil {
		return fmt.Errorf("update post: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("post not found or access denied")
	}

	return nil
}

func (repo *PostRepo) GetChildrenPosts(ctx context.Context, parentID uuid.UUID, pageSize, page int64) ([]Post, error) {
	if page < 0 {

		return nil, fmt.Errorf("page cannot be negative")
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
	rows, err := repo.pool.Query(ctx, query, parentID, offset, pageSize)
	if err != nil {
		return nil, fmt.Errorf("get children posts: %w", err)
	}
	defer rows.Close()
	var posts []Post
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
			return nil, fmt.Errorf("scan child post: %w", err)
		}
		posts = append(posts, post)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate children posts: %w", err)
	}
	return posts, nil
}

func (repo *PostRepo) GetPostsByIDs(ctx context.Context, posts []uuid.UUID) ([]Post, error) {
	if len(posts) == 0 {
		return []Post{}, fmt.Errorf("0 posts recieved")
	}

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
		WHERE id = ANY($1::uuid[])
	`
	rows, err := repo.pool.Query(ctx, query, posts)
	if err != nil {

		return nil, fmt.Errorf("get posts by ids: %w", err)
	}
	defer rows.Close()

	result := make([]Post, 0, len(posts))
	for rows.Next() {
		var post Post
		err := rows.Scan(
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
		)
		if err != nil {
			return nil, fmt.Errorf("scan post: %w", err)
		}
		result = append(result, post)
	}

	return result, nil
}
