package redis

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"post-service/internal/domain"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	reactionStream      = "reactions:events"
	reactionGroup       = "postgres-sync"
	reactionMaintenance = "reactions:maintenance"
	// One fixed consumer identity lets a new leader recover its predecessor's pending events first.
	reactionConsumer = "ordered-writer"
)

// Publish the event before changing membership, so a later write error never silently loses the intent.
// Validate all types first: Lua is isolated, but runtime errors do not roll back earlier writes.
var changeReaction = redis.NewScript(`
if redis.call('EXISTS', KEYS[4]) == 1 then
    return redis.error_reply('reaction maintenance in progress')
end
local kinds = {'set', 'set', 'stream'}
for i = 1, 3 do
    local kind = redis.call('TYPE', KEYS[i]).ok
    if kind ~= 'none' and kind ~= kinds[i] then
        return redis.error_reply('WRONGTYPE invalid reaction storage')
    end
end
local liked = redis.call('SISMEMBER', KEYS[1], ARGV[1])
local disliked = redis.call('SISMEMBER', KEYS[2], ARGV[1])
if liked == 1 and disliked == 1 then
    return redis.error_reply('inconsistent reaction membership')
end
local previous = 0
if liked == 1 then previous = 1 end
if disliked == 1 then previous = -1 end
local state = previous
if ARGV[4] == 'like' then state = 1
elseif ARGV[4] == 'dislike' then state = -1
elseif ARGV[4] == 'unlike' then
    if previous == 1 then state = 0 end
elseif ARGV[4] == 'undislike' then
    if previous == -1 then state = 0 end
else return redis.error_reply('invalid reaction operation') end
if state == previous then return 0 end
redis.call('XADD', KEYS[3], '*', 'event_id', ARGV[3], 'post_id', ARGV[2], 'user_id', ARGV[1], 'state', tostring(state))
if state == 1 then
    redis.call('SADD', KEYS[1], ARGV[1])
    redis.call('SREM', KEYS[2], ARGV[1])
elseif state == -1 then
    redis.call('SADD', KEYS[2], ARGV[1])
    redis.call('SREM', KEYS[1], ARGV[1])
else
    redis.call('SREM', KEYS[1], ARGV[1])
    redis.call('SREM', KEYS[2], ARGV[1])
end
return 1
`)

// mutateReaction updates membership and appends a synchronization event in one isolated Redis script.
func (cache *Cache) mutateReaction(ctx context.Context, userID, postID uuid.UUID, operation string) error {
	if userID == uuid.Nil || postID == uuid.Nil {
		return errors.New("reaction IDs must be nonzero")
	}
	err := changeReaction.Run(ctx, cache.client, []string{
		"post:likes:" + postID.String(), "post:dislikes:" + postID.String(), reactionStream, reactionMaintenance,
	}, userID.String(), postID.String(), uuid.NewString(), operation).Err()
	if err != nil {
		return fmt.Errorf("change reaction and publish event: %w", err)
	}
	return nil
}

// AddLike records a like and replaces an existing dislike.
func (cache *Cache) AddLike(ctx context.Context, userID, postID uuid.UUID) error {
	return cache.mutateReaction(ctx, userID, postID, "like")
}

// AddDislike records a dislike and replaces an existing like.
func (cache *Cache) AddDislike(ctx context.Context, userID, postID uuid.UUID) error {
	return cache.mutateReaction(ctx, userID, postID, "dislike")
}

// RemoveLike removes only an existing like; it does not remove a dislike.
func (cache *Cache) RemoveLike(ctx context.Context, userID, postID uuid.UUID) error {
	return cache.mutateReaction(ctx, userID, postID, "unlike")
}

// RemoveDislike removes only an existing dislike; it does not remove a like.
func (cache *Cache) RemoveDislike(ctx context.Context, userID, postID uuid.UUID) error {
	return cache.mutateReaction(ctx, userID, postID, "undislike")
}

// EnsureReactionGroup creates the durable consumer group from the beginning, preserving older events.
func (cache *Cache) EnsureReactionGroup(ctx context.Context) error {
	err := cache.client.XGroupCreateMkStream(ctx, reactionStream, reactionGroup, "0").Err()
	if err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP ") {
		return fmt.Errorf("create reaction consumer group: %w", err)
	}
	return nil
}

// Read pending and new entries without interleaving. A previous leader can briefly keep reading
// Redis after losing its PostgreSQL connection; separate reads could let it reserve event N while
// the new leader reserves N+1. This script always returns N to both until N is acknowledged.
var nextReactionEvent = redis.NewScript(`
local function read(offset)
    local streams = redis.call('XREADGROUP', 'GROUP', ARGV[1], ARGV[2], 'COUNT', 1, 'STREAMS', KEYS[1], offset)
    if streams and streams[1] and #streams[1][2] > 0 then
        return streams[1][2][1]
    end
    return false
end
return read('0') or read('>')
`)

// NextReactionEvent atomically returns the earliest pending event before reserving a new event.
// Only the PostgreSQL advisory-lock owner may process or acknowledge the returned event.
func (cache *Cache) NextReactionEvent(ctx context.Context) (*domain.ReactionEvent, error) {
	values, err := nextReactionEvent.Run(ctx, cache.client, []string{reactionStream}, reactionGroup, reactionConsumer).Slice()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read reaction event: %w", err)
	}
	if len(values) != 2 {
		return nil, errors.New("invalid reaction stream entry")
	}
	id, ok := values[0].(string)
	if !ok {
		return nil, errors.New("invalid reaction stream ID")
	}
	fields, ok := values[1].([]any)
	if !ok || len(fields)%2 != 0 {
		return nil, fmt.Errorf("missing or invalid pending reaction event %s", id)
	}
	message := redis.XMessage{ID: id, Values: make(map[string]any, len(fields)/2)}
	for i := 0; i < len(fields); i += 2 {
		key, ok := fields[i].(string)
		if !ok {
			return nil, fmt.Errorf("invalid field in pending reaction event %s", id)
		}
		message.Values[key] = fields[i+1]
	}
	event, err := decodeReactionEvent(message)
	if err != nil {
		return nil, fmt.Errorf("invalid pending reaction event %s: %w", id, err)
	}
	return &event, nil
}

// AckReactionEvent confirms processing only after its PostgreSQL transaction has committed.
func (cache *Cache) AckReactionEvent(ctx context.Context, streamID string) error {
	return cache.client.XAck(ctx, reactionStream, reactionGroup, streamID).Err()
}

// decodeReactionEvent validates a stored event without acknowledging malformed entries.
func decodeReactionEvent(message redis.XMessage) (domain.ReactionEvent, error) {
	var event domain.ReactionEvent
	event.StreamID = message.ID
	for key, target := range map[string]*uuid.UUID{"event_id": &event.EventID, "post_id": &event.PostID, "user_id": &event.UserID} {
		value, ok := message.Values[key].(string)
		if !ok {
			return event, fmt.Errorf("missing %s", key)
		}
		id, err := uuid.Parse(value)
		if err != nil {
			return event, fmt.Errorf("invalid %s", key)
		}
		*target = id
	}
	value, ok := message.Values["state"].(string)
	if !ok {
		return event, errors.New("missing reaction state")
	}
	state, err := strconv.ParseInt(value, 10, 16)
	if err != nil {
		return event, errors.New("invalid reaction state")
	}
	event.Kind = domain.ReactionKind(state)
	return event, event.Validate()
}
