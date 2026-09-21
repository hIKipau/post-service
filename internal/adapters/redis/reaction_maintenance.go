package redis

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"post-service/internal/domain"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// CheckReactionMaintenance prevents API startup while an interrupted offline operation needs recovery.
func (cache *Cache) CheckReactionMaintenance(ctx context.Context) error {
	count, err := cache.client.Exists(ctx, reactionMaintenance).Result()
	if err != nil {
		return err
	}
	if count != 0 {
		return errors.New("reaction maintenance is incomplete; finish the offline operation before starting the API")
	}
	return nil
}

// BeginReactionMaintenance blocks new reaction mutations until an offline operation finishes successfully.
// The marker has no TTL: a failed restore must not silently expose partial membership to writers.
func (cache *Cache) BeginReactionMaintenance(ctx context.Context, operation string) error {
	if operation != "import" && operation != "restore" {
		return errors.New("invalid maintenance operation")
	}
	previous, err := cache.client.Get(ctx, reactionMaintenance).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	if previous != "" && previous != operation {
		return fmt.Errorf("unfinished %s operation: resume it before running %s", previous, operation)
	}
	return cache.client.Set(ctx, reactionMaintenance, operation, 0).Err()
}

// EndReactionMaintenance removes the write barrier only after all snapshots have been transferred.
func (cache *Cache) EndReactionMaintenance(ctx context.Context) error {
	return cache.client.Del(ctx, reactionMaintenance).Err()
}

// ReadReactionSnapshot reads existing Redis membership for offline import, rejecting conflicting states.
func (cache *Cache) ReadReactionSnapshot(ctx context.Context, postID uuid.UUID) ([]domain.Reaction, error) {
	result := make([]domain.Reaction, 0)
	seen := make(map[uuid.UUID]struct{})
	for _, item := range []struct {
		prefix string
		kind   domain.ReactionKind
	}{{"post:likes:", domain.ReactionLike}, {"post:dislikes:", domain.ReactionDislike}} {
		members, err := cache.client.SMembers(ctx, item.prefix+postID.String()).Result()
		if err != nil {
			return nil, err
		}
		for _, member := range members {
			id, err := uuid.Parse(member)
			if err != nil || id == uuid.Nil {
				return nil, errors.New("invalid user UUID in reaction snapshot")
			}
			if _, exists := seen[id]; exists {
				return nil, errors.New("user has both like and dislike; repair the snapshot before import")
			}
			seen[id] = struct{}{}
			result = append(result, domain.Reaction{UserID: id, Kind: item.kind})
		}
	}
	return result, nil
}

// RestoreReactionSnapshot replaces one post's Redis membership without emitting new synchronization events.
// This is an offline operation, not a cache-miss path; live API writers must be stopped.
func (cache *Cache) RestoreReactionSnapshot(ctx context.Context, postID uuid.UUID, reactions []domain.Reaction) error {
	likes, dislikes := make([]any, 0), make([]any, 0)
	seen := make(map[uuid.UUID]struct{}, len(reactions))
	for _, reaction := range reactions {
		if reaction.UserID == uuid.Nil {
			return errors.New("invalid snapshot user")
		}
		if _, exists := seen[reaction.UserID]; exists {
			return errors.New("duplicate snapshot user")
		}
		seen[reaction.UserID] = struct{}{}
		switch reaction.Kind {
		case domain.ReactionLike:
			likes = append(likes, reaction.UserID.String())
		case domain.ReactionDislike:
			dislikes = append(dislikes, reaction.UserID.String())
		default:
			return errors.New("invalid snapshot reaction kind")
		}
	}
	likeKey, dislikeKey := "post:likes:"+postID.String(), "post:dislikes:"+postID.String()
	_, err := cache.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Del(ctx, likeKey, dislikeKey)
		// Bounded commands also support snapshots larger than Lua's unpack argument limit.
		for start := 0; start < len(likes); start += 1000 {
			pipe.SAdd(ctx, likeKey, likes[start:min(start+1000, len(likes))]...)
		}
		for start := 0; start < len(dislikes); start += 1000 {
			pipe.SAdd(ctx, dislikeKey, dislikes[start:min(start+1000, len(dislikes))]...)
		}
		return nil
	})
	return err
}

// ClearReactionMembership removes only validated post reaction keys before an explicit offline restore.
// The event stream, consumer group, feed queues and all other Redis keys are preserved.
func (cache *Cache) ClearReactionMembership(ctx context.Context) error {
	for _, prefix := range []string{"post:likes:", "post:dislikes:"} {
		iter := cache.client.Scan(ctx, 0, prefix+"*", 100).Iterator()
		for iter.Next(ctx) {
			key := iter.Val()
			id, err := uuid.Parse(strings.TrimPrefix(key, prefix))
			if err != nil || id == uuid.Nil || prefix+id.String() != key {
				return fmt.Errorf("refuse to clear malformed reaction key %q", key)
			}
			if err := cache.client.Del(ctx, key).Err(); err != nil {
				return err
			}
		}
		if err := iter.Err(); err != nil {
			return err
		}
	}
	return nil
}
