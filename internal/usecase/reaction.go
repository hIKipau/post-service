package usecase

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// LikePost adds a like to the post and removes an existing dislike.
func (uc *Usecase) LikePost(ctx context.Context, postID uuid.UUID, userID uuid.UUID) error {
	uc.logger.Debug(
		"Processing post like",
		"post_id", postID,
		"user_id", userID,
	)

	// The cache replaces the opposite reaction atomically.
	if err := uc.cache.AddLike(ctx, userID, postID); err != nil {
		uc.logger.Error(
			"Failed to add post like",
			"error", err,
			"post_id", postID,
			"user_id", userID,
		)
		return fmt.Errorf("add post like: %w", err)
	}

	uc.logger.Info(
		"Post liked",
		"post_id", postID,
		"user_id", userID,
	)

	return nil
}

// DislikePost adds a dislike to the post and removes an existing like.
func (uc *Usecase) DislikePost(ctx context.Context, postID uuid.UUID, userID uuid.UUID) error {
	uc.logger.Debug(
		"Processing post dislike",
		"post_id", postID,
		"user_id", userID,
	)

	// The cache replaces the opposite reaction atomically.
	if err := uc.cache.AddDislike(ctx, userID, postID); err != nil {
		uc.logger.Error(
			"Failed to add post dislike",
			"error", err,
			"post_id", postID,
			"user_id", userID,
		)
		return fmt.Errorf("add post dislike: %w", err)
	}

	uc.logger.Info(
		"Post disliked",
		"post_id", postID,
		"user_id", userID,
	)

	return nil
}

// UnLikePost removes a like from the post.
func (uc *Usecase) UnLikePost(ctx context.Context, postID uuid.UUID, userID uuid.UUID) error {
	uc.logger.Debug(
		"Processing post unlike",
		"post_id", postID,
		"user_id", userID,
	)

	if err := uc.cache.RemoveLike(ctx, userID, postID); err != nil {
		uc.logger.Error(
			"Failed to remove post like",
			"error", err,
			"post_id", postID,
			"user_id", userID,
		)
		return fmt.Errorf("remove post like: %w", err)
	}

	uc.logger.Info(
		"Post unliked",
		"post_id", postID,
		"user_id", userID,
	)

	return nil
}

// UnDislikePost removes a dislike from the post.
func (uc *Usecase) UnDislikePost(ctx context.Context, postID uuid.UUID, userID uuid.UUID) error {
	uc.logger.Debug(
		"Processing post undislike",
		"post_id", postID,
		"user_id", userID,
	)

	if err := uc.cache.RemoveDislike(ctx, userID, postID); err != nil {
		uc.logger.Error(
			"Failed to remove post dislike",
			"error", err,
			"post_id", postID,
			"user_id", userID,
		)
		return fmt.Errorf("remove post dislike: %w", err)
	}

	uc.logger.Info(
		"Post undisliked",
		"post_id", postID,
		"user_id", userID,
	)

	return nil
}
