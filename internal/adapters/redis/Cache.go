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

// Both keys must be sets (or absent). Check before writing because Lua errors
// do not roll back earlier writes. Redis executes the script without interleaving.
var switchReaction = redis.NewScript(`
for i = 1, 2 do
    local kind = redis.call("TYPE", KEYS[i]).ok
    if kind ~= "none" and kind ~= "set" then
        return redis.error_reply("WRONGTYPE reaction key must be a set")
    end
end
redis.call("SADD", KEYS[1], ARGV[1])
redis.call("SREM", KEYS[2], ARGV[1])
return 1
`)

// AddLike atomically adds a like and removes the user's dislike.
func (cache *Cache) AddLike(ctx context.Context, userID uuid.UUID, postID uuid.UUID) error {
	key := "post:likes:" + postID.String()

	cache.logger.Debug(
		"Adding like to post",
		"user_id", userID,
		"post_id", postID,
	)

	if err := switchReaction.Run(ctx, cache.client,
		[]string{key, "post:dislikes:" + postID.String()}, userID.String()).Err(); err != nil {
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

// AddDislike atomically adds a dislike and removes the user's like.
func (cache *Cache) AddDislike(ctx context.Context, userID uuid.UUID, postID uuid.UUID) error {
	key := "post:dislikes:" + postID.String()

	cache.logger.Debug(
		"Adding dislike to post",
		"user_id", userID,
		"post_id", postID,
	)

	if err := switchReaction.Run(ctx, cache.client,
		[]string{key, "post:likes:" + postID.String()}, userID.String()).Err(); err != nil {
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

// IsLiked checks whether the user has liked the specified post.
func (cache *Cache) IsLiked(
	ctx context.Context,
	userID uuid.UUID,
	postID uuid.UUID,
) (bool, error) {
	key := "post:likes:" + postID.String()

	cache.logger.Debug(
		"Checking if user liked post",
		"user_id", userID,
		"post_id", postID,
	)

	liked, err := cache.client.SIsMember(
		ctx,
		key,
		userID.String(),
	).Result()
	if err != nil {
		cache.logger.Error(
			"Failed to check post like",
			"error", err,
			"user_id", userID,
			"post_id", postID,
		)

		return false, fmt.Errorf("check post like: %w", err)
	}

	cache.logger.Debug(
		"Post like status retrieved",
		"user_id", userID,
		"post_id", postID,
		"liked", liked,
	)

	return liked, nil
}

// IsDisliked checks whether the user has disliked the specified post.
func (cache *Cache) IsDisliked(
	ctx context.Context,
	userID uuid.UUID,
	postID uuid.UUID,
) (bool, error) {
	key := "post:dislikes:" + postID.String()

	cache.logger.Debug(
		"Checking if user disliked post",
		"user_id", userID,
		"post_id", postID,
	)

	disliked, err := cache.client.SIsMember(
		ctx,
		key,
		userID.String(),
	).Result()
	if err != nil {
		cache.logger.Error(
			"Failed to check post dislike",
			"error", err,
			"user_id", userID,
			"post_id", postID,
		)

		return false, fmt.Errorf("check post dislike: %w", err)
	}

	cache.logger.Debug(
		"Post dislike status retrieved",
		"user_id", userID,
		"post_id", postID,
		"disliked", disliked,
	)

	return disliked, nil
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

// GetPostsRepliesCount returns cached reply counts. Missing keys are omitted,
// so callers can distinguish a cache miss from a cached zero.
func (cache *Cache) GetPostsRepliesCount(
	ctx context.Context,
	postIDs []uuid.UUID,
) (map[uuid.UUID]int64, error) {
	cache.logger.Debug(
		"Getting post replies counts from cache",
		"posts_count", len(postIDs),
	)

	if len(postIDs) == 0 {
		return map[uuid.UUID]int64{}, nil
	}

	result := make(map[uuid.UUID]int64, len(postIDs))
	pipe := cache.client.Pipeline()

	cmds := make(map[uuid.UUID]*redis.StringCmd, len(postIDs))

	for _, postID := range postIDs {
		key := "post:replies:count:" + postID.String()
		cmds[postID] = pipe.Get(ctx, key)
	}

	_, err := pipe.Exec(ctx)
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("get post replies counts: %w", err)
	}

	for postID, cmd := range cmds {
		value, err := cmd.Result()

		if errors.Is(err, redis.Nil) {
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
