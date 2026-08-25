package usecase

import (
	"context"
	"post-service/internal/domain"
	"time"

	"github.com/google/uuid"
)

// GetFeed returns up to limit filtered posts from the user's feed.
func (uc *Usecase) GetFeed(ctx context.Context, userID uuid.UUID) ([]domain.Post, error) {
	uc.logger.Debug(
		"Getting user feed",
		"user_id", userID,
	)

	feedLength, err := uc.cache.GetFeedLength(ctx, userID)
	if err != nil {
		uc.logger.Error(
			"Failed to get user feed length",
			"error", err,
			"user_id", userID,
		)
		return []domain.Post{}, err
	}

	uc.logger.Debug(
		"User feed length retrieved",
		"user_id", userID,
		"feed_length", feedLength,
	)

	if feedLength >= 20 {
		uc.logger.Info(
			"Getting feed from cache",
			"user_id", userID,
			"feed_length", feedLength,
		)

		feedPostsIDs, err := uc.cache.GetFeed(ctx, userID, 20)
		if err != nil {
			uc.logger.Error(
				"Failed to get feed posts from cache",
				"error", err,
				"user_id", userID,
			)
			return []domain.Post{}, err
		}

		uc.logger.Debug(
			"Feed post IDs retrieved from cache",
			"user_id", userID,
			"posts_count", len(feedPostsIDs),
		)

		feedLikesCounts, err := uc.cache.GetPostsLikes(ctx, feedPostsIDs)
		if err != nil {
			uc.logger.Error(
				"Failed to get feed post like counts",
				"error", err,
				"user_id", userID,
			)
			return []domain.Post{}, err
		}

		feedDislikesCounts, err := uc.cache.GetPostsDislikes(ctx, feedPostsIDs)
		if err != nil {
			uc.logger.Error(
				"Failed to get feed post dislike counts",
				"error", err,
				"user_id", userID,
			)
			return []domain.Post{}, err
		}

		feedRepliesCounts, err := uc.cache.GetPostsRepliesCount(ctx, feedPostsIDs)
		if err != nil {
			uc.logger.Error(
				"Failed to get feed post reply counts",
				"error", err,
				"user_id", userID,
			)
			return []domain.Post{}, err
		}

		err = uc.cache.AddSeen(
			ctx,
			userID,
			feedPostsIDs,
			time.Hour*24*3,
		)
		if err != nil {
			uc.logger.Error(
				"Failed to add feed posts to seen cache",
				"error", err,
				"user_id", userID,
				"posts_count", len(feedPostsIDs),
			)
			return []domain.Post{}, err
		}

		feed, err := uc.repo.GetPostsByIDs(ctx, feedPostsIDs)
		if err != nil {
			uc.logger.Error(
				"Failed to get feed posts from repository",
				"error", err,
				"user_id", userID,
			)
			return []domain.Post{}, err
		}

		for i := range feed {
			feed[i].LikeCount = feedLikesCounts[feed[i].ID]
			feed[i].DislikeCount = feedDislikesCounts[feed[i].ID]
			feed[i].ReplyCount = feedRepliesCounts[feed[i].ID]
		}

		uc.logger.Info(
			"User feed retrieved from cache",
			"user_id", userID,
			"posts_count", len(feed),
		)

		return feed, nil
	}

	uc.logger.Info(
		"Cached feed is insufficient, generating new feed",
		"user_id", userID,
		"feed_length", feedLength,
	)

	rawCandidates, err := uc.repo.GetFeedCandidates(ctx, userID, 100)
	if err != nil {
		uc.logger.Error(
			"Failed to get feed candidates",
			"error", err,
			"user_id", userID,
		)
		return []domain.Post{}, err
	}

	uc.logger.Debug(
		"Feed candidates retrieved",
		"user_id", userID,
		"candidates_count", len(rawCandidates),
	)

	seen, err := uc.cache.GetSeen(ctx, userID)
	if err != nil {
		uc.logger.Error(
			"Failed to get seen posts",
			"error", err,
			"user_id", userID,
		)
		return []domain.Post{}, err
	}

	uc.logger.Debug(
		"Seen posts retrieved",
		"user_id", userID,
		"seen_count", len(seen),
	)

	filtered := make([]domain.FeedCandidate, 0, len(rawCandidates))

	for _, candidate := range rawCandidates {
		if _, ok := seen[candidate.PostID]; ok {
			continue
		}

		filtered = append(filtered, candidate)
	}

	uc.logger.Debug(
		"Feed candidates filtered",
		"user_id", userID,
		"candidates_before", len(rawCandidates),
		"candidates_after", len(filtered),
	)

	realCandidates := make([]uuid.UUID, len(filtered))

	for i, candidate := range filtered {
		realCandidates[i] = candidate.PostID
	}

	feedActualLikesCounts, err := uc.cache.GetPostsLikes(ctx, realCandidates)
	if err != nil {
		uc.logger.Error(
			"Failed to get actual feed like counts",
			"error", err,
			"user_id", userID,
		)
		return []domain.Post{}, err
	}

	feedActualDislikesCounts, err := uc.cache.GetPostsDislikes(ctx, realCandidates)
	if err != nil {
		uc.logger.Error(
			"Failed to get actual feed dislike counts",
			"error", err,
			"user_id", userID,
		)
		return []domain.Post{}, err
	}

	feedActualRepliesCounts, err := uc.cache.GetPostsRepliesCount(ctx, realCandidates)
	if err != nil {
		uc.logger.Error(
			"Failed to get actual feed reply counts",
			"error", err,
			"user_id", userID,
		)
		return []domain.Post{}, err
	}

	// TODO filtering

	err = uc.cache.DeleteFeed(ctx, userID)
	if err != nil {
		uc.logger.Error(
			"Failed to delete old user feed",
			"error", err,
			"user_id", userID,
		)
		return []domain.Post{}, err
	}

	err = uc.cache.SetFeed(
		ctx,
		userID,
		realCandidates,
		time.Hour*24,
	)
	if err != nil {
		uc.logger.Error(
			"Failed to cache new user feed",
			"error", err,
			"user_id", userID,
			"posts_count", len(realCandidates),
		)
		return []domain.Post{}, err
	}

	err = uc.cache.AddSeen(
		ctx,
		userID,
		realCandidates,
		time.Hour*24*3,
	)
	if err != nil {
		uc.logger.Error(
			"Failed to add generated feed posts to seen cache",
			"error", err,
			"user_id", userID,
			"posts_count", len(realCandidates),
		)
		return []domain.Post{}, err
	}

	feed, err := uc.repo.GetPostsByIDs(ctx, realCandidates)
	if err != nil {
		uc.logger.Error(
			"Failed to get generated feed posts from repository",
			"error", err,
			"user_id", userID,
		)
		return []domain.Post{}, err
	}

	for i := range feed {
		feed[i].LikeCount = feedActualLikesCounts[feed[i].ID]
		feed[i].DislikeCount = feedActualDislikesCounts[feed[i].ID]
		feed[i].ReplyCount = feedActualRepliesCounts[feed[i].ID]
	}

	uc.logger.Info(
		"New user feed generated",
		"user_id", userID,
		"posts_count", len(feed),
	)

	return feed, nil
}
