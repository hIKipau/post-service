package usecase

import (
	"context"
	"fmt"
	"post-service/internal/domain"

	"github.com/google/uuid"
)

//- CreatePost
//- UpdatePost
//- DeletePost

// CreatePost creates a new post.
func (uc *Usecase) CreatePost(ctx context.Context, post domain.Post) error {
	uc.logger.Debug(
		"Creating post",
		"post_id", post.ID,
		"author_id", post.AuthorID,
	)

	if err := uc.repo.CreatePost(ctx, post); err != nil {
		uc.logger.Error(
			"Failed to create post",
			"error", err,
			"post_id", post.ID,
			"author_id", post.AuthorID,
		)

		return fmt.Errorf("create post: %w", err)
	}

	uc.logger.Info(
		"Post created",
		"post_id", post.ID,
		"author_id", post.AuthorID,
	)

	return nil
}

// UpdatePost updates an existing post.
func (uc *Usecase) UpdatePost(ctx context.Context, post domain.Post) error {
	uc.logger.Debug(
		"Updating post",
		"post_id", post.ID,
		"author_id", post.AuthorID,
	)

	if err := uc.repo.UpdatePost(ctx, post); err != nil {
		uc.logger.Error(
			"Failed to update post",
			"error", err,
			"post_id", post.ID,
			"author_id", post.AuthorID,
		)

		return fmt.Errorf("update post: %w", err)
	}

	uc.logger.Info(
		"Post updated",
		"post_id", post.ID,
		"author_id", post.AuthorID,
	)

	return nil
}

// DeletePost deletes a post owned by the specified author.
func (uc *Usecase) DeletePost(ctx context.Context, postID uuid.UUID, authorID uuid.UUID, ) error {
	uc.logger.Debug(
		"Deleting post",
		"post_id", postID,
		"author_id", authorID,
	)

	if err := uc.repo.DeletePost(ctx, postID, authorID); err != nil {
		uc.logger.Error(
			"Failed to delete post",
			"error", err,
			"post_id", postID,
			"author_id", authorID,
		)

		return fmt.Errorf("delete post: %w", err)
	}

	uc.logger.Info(
		"Post deleted",
		"post_id", postID,
		"author_id", authorID,
	)

	return nil
}
