package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type Cache struct {
	*Redis
}

func NewCache(redis *Redis) *Cache {
	return &Cache{Redis: redis}
}

//кэш хранит лайки и кол-во репли.
//кэш хранит просмотренные юзером посты(с ttl - до недели)

// SetFeed caches the user's feed from a slice of UUIDs with the specified TTL.
func (cache *Cache) SetFeed(ctx context.Context, userID uuid.UUID, postIDs []uuid.UUID, ttl time.Duration) error {
	key := "feed:" + userID.String()

	cache.logger.Debug(
		"Setting user feed in cache",
		"user_id", userID,
		"posts_count", len(postIDs),
		"ttl", ttl,
	)

	if err := cache.client.Del(ctx, key).Err(); err != nil {
		cache.logger.Error(
			"Failed to delete old user feed",
			"error", err,
			"user_id", userID,
			"key", key,
		)
		return fmt.Errorf("delete old user feed: %w", err)
	}

	values := make([]any, len(postIDs))
	for i, postID := range postIDs {
		values[i] = postID.String()
	}

	if err := cache.client.RPush(ctx, key, values...).Err(); err != nil {
		cache.logger.Error(
			"Failed to cache user feed",
			"error", err,
			"user_id", userID,
			"posts_count", len(postIDs),
		)
		return fmt.Errorf("cache user feed: %w", err)
	}

	if err := cache.client.Expire(ctx, key, ttl).Err(); err != nil {
		cache.logger.Error(
			"Failed to set feed TTL",
			"error", err,
			"user_id", userID,
			"ttl", ttl,
		)
		return fmt.Errorf("set feed TTL: %w", err)
	}

	cache.logger.Debug(
		"User feed cached successfully",
		"user_id", userID,
		"posts_count", len(postIDs),
		"ttl", ttl,
	)

	return nil
}

// GetFeed returns up to limit post UUIDs from the user's cached feed.
func (cache *Cache) GetFeed(ctx context.Context, userID uuid.UUID, limit int64) ([]uuid.UUID, error) {
	key := "feed:" + userID.String()

	cache.logger.Debug(
		"Getting user feed from cache",
		"user_id", userID,
		"limit", limit,
	)

	if limit <= 0 {
		cache.logger.Debug(
			"Feed request skipped because limit is not positive",
			"user_id", userID,
			"limit", limit,
		)
		return []uuid.UUID{}, nil
	}

	length, err := cache.client.LLen(ctx, key).Result()
	if err != nil {
		cache.logger.Error(
			"Failed to get feed length from cache",
			"error", err,
			"user_id", userID,
			"key", key,
		)
		return nil, fmt.Errorf("get feed length: %w", err)
	}

	if length == 0 {
		cache.logger.Debug(
			"User feed is empty",
			"user_id", userID,
		)
		return []uuid.UUID{}, nil
	}

	if limit > length {
		limit = length
	}

	values, err := cache.client.LRange(ctx, key, 0, limit-1).Result()
	if err != nil {
		cache.logger.Error(
			"Failed to get user feed from cache",
			"error", err,
			"user_id", userID,
			"limit", limit,
		)
		return nil, fmt.Errorf("get user feed: %w", err)
	}

	result := make([]uuid.UUID, 0, len(values))

	for _, value := range values {
		postID, err := uuid.Parse(value)
		if err != nil {
			cache.logger.Error(
				"Failed to parse post UUID from feed",
				"error", err,
				"user_id", userID,
				"value", value,
			)
			return nil, fmt.Errorf("parse feed post UUID: %w", err)
		}

		result = append(result, postID)
	}

	if err := cache.client.LTrim(ctx, key, limit, -1).Err(); err != nil {
		cache.logger.Error(
			"Failed to trim user feed",
			"error", err,
			"user_id", userID,
			"trimmed_count", limit,
		)
		return nil, fmt.Errorf("trim user feed: %w", err)
	}

	cache.logger.Debug(
		"User feed retrieved from cache",
		"user_id", userID,
		"returned_count", len(result),
		"remaining_count", length-limit,
	)

	return result, nil
}

// DeleteFeed deletes the user's feed from the cache.
func (cache *Cache) DeleteFeed(ctx context.Context, userID uuid.UUID) error {
	key := "feed:" + userID.String()

	cache.logger.Debug(
		"Deleting user feed from cache",
		"user_id", userID,
		"key", key,
	)

	if err := cache.client.Del(ctx, key).Err(); err != nil {
		cache.logger.Error(
			"Failed to delete user feed from cache",
			"error", err,
			"user_id", userID,
			"key", key,
		)

		return fmt.Errorf("delete user feed from cache: %w", err)
	}

	cache.logger.Debug(
		"User feed deleted from cache",
		"user_id", userID,
	)

	return nil
}

// GetSeen returns a set of post UUIDs already seen by the user.
func (cache *Cache) GetSeen(ctx context.Context, userID uuid.UUID) (map[uuid.UUID]struct{}, error) {
	key := "feed:seen:" + userID.String()

	cache.logger.Debug(
		"Getting seen posts from cache",
		"user_id", userID,
		"key", key,
	)

	values, err := cache.client.SMembers(ctx, key).Result()
	if err != nil {
		cache.logger.Error(
			"Failed to get seen posts from cache",
			"error", err,
			"user_id", userID,
			"key", key,
		)

		return nil, fmt.Errorf("get seen posts from cache: %w", err)
	}

	result := make(map[uuid.UUID]struct{}, len(values))

	for _, value := range values {
		postID, err := uuid.Parse(value)
		if err != nil {
			cache.logger.Error(
				"Failed to parse seen post UUID",
				"error", err,
				"user_id", userID,
				"value", value,
			)

			return nil, fmt.Errorf("parse seen post UUID %q: %w", value, err)
		}

		result[postID] = struct{}{}
	}

	cache.logger.Debug(
		"Seen posts retrieved from cache",
		"user_id", userID,
		"count", len(result),
	)

	return result, nil
}

// AddSeen adds post UUIDs to the user's seen posts set and refreshes its TTL.
func (cache *Cache) AddSeen(ctx context.Context, userID uuid.UUID, postIDs []uuid.UUID, ttl time.Duration) error {
	key := "feed:seen:" + userID.String()

	if len(postIDs) == 0 {
		return nil
	}

	values := make([]any, len(postIDs))
	for i, postID := range postIDs {
		values[i] = postID.String()
	}

	cache.logger.Debug(
		"Adding seen posts to cache",
		"user_id", userID,
		"posts_count", len(postIDs),
	)

	if err := cache.client.SAdd(ctx, key, values...).Err(); err != nil {
		cache.logger.Error(
			"Failed to add seen posts to cache",
			"error", err,
			"user_id", userID,
			"posts_count", len(postIDs),
		)
		return fmt.Errorf("add seen posts to cache: %w", err)
	}

	if err := cache.client.Expire(ctx, key, ttl).Err(); err != nil {
		cache.logger.Error(
			"Failed to set seen posts TTL",
			"error", err,
			"user_id", userID,
			"ttl", ttl,
		)
		return fmt.Errorf("set seen posts TTL: %w", err)
	}

	cache.logger.Debug(
		"Seen posts added to cache",
		"user_id", userID,
		"posts_count", len(postIDs),
		"ttl", ttl,
	)

	return nil
}

func (cache *Cache) SetLike(ctx context.Context, userID uuid.UUID, postID uuid.UUID) error {

}
func (cache *Cache) RemoveLike(ctx context.Context, userID uuid.UUID, postID uuid.UUID) error {

}

func (cache *Cache) SetDislike(ctx context.Context, userID uuid.UUID, postID uuid.UUID) error {

}

func (cache *Cache) RemoveDislike(ctx context.Context, userID uuid.UUID, postID uuid.UUID) error {

}

func (cache *Cache) GetLikesByIDs(ctx context.Context, userID uuid.UUID, postID uuid.UUID)(map[uuid.UUID]struct{}, error) {

}
func (cache *Cache) GetDislikesByIDs(ctx context.Context, userID uuid.UUID, postID uuid.UUID)(map[uuid.UUID]struct{}, error) {

}
