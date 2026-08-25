package redis

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Cache struct {
	*Redis
}

func NewCache(redis *Redis) *Cache {
	return &Cache{Redis: redis}
}

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

// GetFeedLength returns length of the user's cached feed.
func (cache *Cache) GetFeedLength(ctx context.Context, userID uuid.UUID) (int64, error) {
	key := "feed:" + userID.String()
	cache.logger.Debug(
		"Getting user feed length from cache",
		"user_id", userID,
		"key", key,
	)

	length, err := cache.client.LLen(ctx, key).Result()
	if err != nil {
		cache.logger.Error(
			"Failed to get feed length from cache",
			"error", err,
			"user_id", userID,
			"key", key,
		)
		return -1, fmt.Errorf("get feed length: %w", err)
	}

	return length, nil
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

// AddLike adds the user's UUID to the post's likes set.
func (cache *Cache) AddLike(ctx context.Context, userID uuid.UUID, postID uuid.UUID) error {
	key := "post:likes:" + postID.String()

	cache.logger.Debug(
		"Adding like to post",
		"user_id", userID,
		"post_id", postID,
	)

	if err := cache.client.SAdd(ctx, key, userID.String()).Err(); err != nil {
		cache.logger.Error(
			"Failed to add like to post",
			"error", err,
			"user_id", userID,
			"post_id", postID,
		)

		return fmt.Errorf("add like to post: %w", err)
	}

	cache.logger.Debug(
		"Like added to post",
		"user_id", userID,
		"post_id", postID,
	)

	return nil
}

// RemoveLike removes the user's UUID from the post's likes set.
func (cache *Cache) RemoveLike(ctx context.Context, userID uuid.UUID, postID uuid.UUID) error {
	key := "post:likes:" + postID.String()

	cache.logger.Debug(
		"Removing like from post",
		"user_id", userID,
		"post_id", postID,
	)

	if err := cache.client.SRem(ctx, key, userID.String()).Err(); err != nil {
		cache.logger.Error(
			"Failed to remove like from post",
			"error", err,
			"user_id", userID,
			"post_id", postID,
		)

		return fmt.Errorf("remove like from post: %w", err)
	}

	return nil
}

// AddDislike adds the user's UUID to the post's dislikes set.
func (cache *Cache) AddDislike(ctx context.Context, userID uuid.UUID, postID uuid.UUID) error {
	key := "post:dislikes:" + postID.String()

	cache.logger.Debug(
		"Adding dislike to post",
		"user_id", userID,
		"post_id", postID,
	)

	if err := cache.client.SAdd(ctx, key, userID.String()).Err(); err != nil {
		cache.logger.Error(
			"Failed to add dislike to post",
			"error", err,
			"user_id", userID,
			"post_id", postID,
		)

		return fmt.Errorf("add dislike to post: %w", err)
	}

	cache.logger.Debug(
		"Dislike added to post",
		"user_id", userID,
		"post_id", postID,
	)

	return nil
}

// RemoveDislike removes the user's UUID from the post's dislikes set.
func (cache *Cache) RemoveDislike(ctx context.Context, userID uuid.UUID, postID uuid.UUID) error {
	key := "post:dislikes:" + postID.String()

	cache.logger.Debug(
		"Removing dislike from post",
		"user_id", userID,
		"post_id", postID,
	)

	if err := cache.client.SRem(ctx, key, userID.String()).Err(); err != nil {
		cache.logger.Error(
			"Failed to remove dislike from post",
			"error", err,
			"user_id", userID,
			"post_id", postID,
		)

		return fmt.Errorf("remove dislike from post: %w", err)
	}

	cache.logger.Debug(
		"Dislike removed from post",
		"user_id", userID,
		"post_id", postID,
	)

	return nil
}

// GetPostsLikes returns the like count for each specified post.
func (cache *Cache) GetPostsLikes(ctx context.Context, postIDs []uuid.UUID) (map[uuid.UUID]int64, error) {
	cache.logger.Debug(
		"Getting post like counts from cache",
		"posts_count", len(postIDs),
	)

	result := make(map[uuid.UUID]int64, len(postIDs))
	pipe := cache.client.Pipeline()

	cmds := make(map[uuid.UUID]*redis.IntCmd, len(postIDs))

	for _, postID := range postIDs {
		key := "post:likes:" + postID.String()
		cmds[postID] = pipe.SCard(ctx, key)
	}

	cache.logger.Debug(
		"Executing Redis pipeline for post like counts",
		"commands_count", len(cmds),
	)

	if _, err := pipe.Exec(ctx); err != nil {
		cache.logger.Error(
			"Failed to get post like counts",
			"error", err,
			"posts_count", len(postIDs),
		)
		return nil, fmt.Errorf("get post like counts: %w", err)
	}

	for postID, cmd := range cmds {
		result[postID] = cmd.Val()
	}

	cache.logger.Debug(
		"Post like counts retrieved from cache",
		"posts_count", len(result),
	)

	return result, nil
}

// GetPostsDislikes returns the dislike count for each specified post.
func (cache *Cache) GetPostsDislikes(ctx context.Context, postIDs []uuid.UUID) (map[uuid.UUID]int64, error) {
	cache.logger.Debug(
		"Getting post dislike counts from cache",
		"posts_count", len(postIDs),
	)

	pipe := cache.client.Pipeline()
	result := make(map[uuid.UUID]int64, len(postIDs))
	cmds := make(map[uuid.UUID]*redis.IntCmd, len(postIDs))

	for _, postID := range postIDs {
		key := "post:dislikes:" + postID.String()
		cmds[postID] = pipe.SCard(ctx, key)
	}

	if _, err := pipe.Exec(ctx); err != nil {
		cache.logger.Error(
			"Failed to get post dislike counts",
			"error", err,
			"posts_count", len(postIDs),
		)
		return nil, fmt.Errorf("get post dislike counts: %w", err)
	}

	for postID, cmd := range cmds {
		result[postID] = cmd.Val()
	}

	cache.logger.Debug(
		"Post dislike counts retrieved from cache",
		"posts_count", len(result),
	)

	return result, nil
}

// SetPostRepliesCount caches the reply count for the specified post.
func (cache *Cache) SetPostRepliesCount(
	ctx context.Context,
	postID uuid.UUID,
	replyCount int64,
	ttl time.Duration,
) error {
	key := "post:replies:count:" + postID.String()

	cache.logger.Debug(
		"Setting post replies count in cache",
		"post_id", postID,
		"reply_count", replyCount,
		"ttl", ttl,
	)

	if err := cache.client.Set(ctx, key, replyCount, ttl).Err(); err != nil {
		cache.logger.Error(
			"Failed to set post replies count in cache",
			"error", err,
			"post_id", postID,
			"reply_count", replyCount,
		)

		return fmt.Errorf("set post replies count: %w", err)
	}

	cache.logger.Debug(
		"Post replies count cached successfully",
		"post_id", postID,
		"reply_count", replyCount,
	)

	return nil
}

// GetPostsRepliesCount returns the cached reply count for each specified post.
func (cache *Cache) GetPostsRepliesCount(
	ctx context.Context,
	postIDs []uuid.UUID,
) (map[uuid.UUID]int64, error) {
	cache.logger.Debug(
		"Getting post replies counts from cache",
		"posts_count", len(postIDs),
	)

	result := make(map[uuid.UUID]int64, len(postIDs))
	pipe := cache.client.Pipeline()

	cmds := make(map[uuid.UUID]*redis.StringCmd, len(postIDs))

	for _, postID := range postIDs {
		key := "post:replies:count:" + postID.String()
		cmds[postID] = pipe.Get(ctx, key)
	}

	if _, err := pipe.Exec(ctx); err != nil {
		cache.logger.Error(
			"Failed to get post replies counts from cache",
			"error", err,
			"posts_count", len(postIDs),
		)

		return nil, fmt.Errorf("get post replies counts: %w", err)
	}

	for postID, cmd := range cmds {
		value, err := cmd.Result()

		if errors.Is(err, redis.Nil) {
			result[postID] = 0
			continue
		}

		if err != nil {
			cache.logger.Error(
				"Failed to get post replies count",
				"error", err,
				"post_id", postID,
			)

			return nil, fmt.Errorf(
				"get post replies count for %s: %w",
				postID,
				err,
			)
		}

		count, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			cache.logger.Error(
				"Failed to parse post replies count",
				"error", err,
				"post_id", postID,
				"value", value,
			)

			return nil, fmt.Errorf(
				"parse post replies count for %s: %w",
				postID,
				err,
			)
		}

		result[postID] = count
	}

	cache.logger.Debug(
		"Post replies counts retrieved from cache",
		"posts_count", len(result),
	)

	return result, nil
}
