package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"post-service/internal/domain"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var readFeedState = redis.NewScript(`
return {
    redis.call('LRANGE', KEYS[1], 0, -1),
    redis.call('GET', KEYS[2]) or '',
    redis.call('GET', KEYS[3]) or '',
    redis.call('SMEMBERS', KEYS[4])
}
`)

// Check key types before any mutation: Lua errors do not roll back earlier writes.
// A changed revision rejects stale work without changing the queue, cursor or seen set.
var commitFeedState = redis.NewScript(`
local expectedTypes = {'list', 'string', 'string', 'set'}
for i = 1, 4 do
    local kind = redis.call('TYPE', KEYS[i]).ok
    if kind ~= 'none' and kind ~= expectedTypes[i] then
        return redis.error_reply('WRONGTYPE invalid feed state')
    end
end
if (redis.call('GET', KEYS[3]) or '') ~= ARGV[1] then
    return 0
end
local count = tonumber(ARGV[6])
redis.call('DEL', KEYS[1])
if count > 0 then
    redis.call('RPUSH', KEYS[1], unpack(ARGV, 7, 6 + count))
    redis.call('PEXPIRE', KEYS[1], ARGV[4])
end
redis.call('SET', KEYS[2], ARGV[3], 'PX', ARGV[4])
redis.call('SET', KEYS[3], ARGV[2], 'PX', ARGV[4])
if #ARGV > 6 + count then
    redis.call('SADD', KEYS[4], unpack(ARGV, 7 + count))
    redis.call('PEXPIRE', KEYS[4], ARGV[5])
end
return 1
`)

// feedKeys returns the per-user queue, cursor, revision and seen keys used by both scripts.
func feedKeys(userID uuid.UUID) []string {
	id := userID.String()
	return []string{"feed:" + id, "feed:cursor:" + id, "feed:revision:" + id, "feed:seen:" + id}
}

// GetFeedState reads a consistent snapshot without removing queued IDs or marking posts as seen.
func (cache *Cache) GetFeedState(ctx context.Context, userID uuid.UUID) (domain.FeedState, error) {
	values, err := readFeedState.Run(ctx, cache.client, feedKeys(userID)).Slice()
	if err != nil {
		return domain.FeedState{}, fmt.Errorf("read feed snapshot: %w", err)
	}
	if len(values) != 4 {
		return domain.FeedState{}, errors.New("invalid feed snapshot")
	}
	ids, err := feedUUIDs(values[0])
	if err != nil {
		return domain.FeedState{}, err
	}
	seenIDs, err := feedUUIDs(values[3])
	if err != nil {
		return domain.FeedState{}, err
	}
	cursorJSON, cursorOK := values[1].(string)
	revision, revisionOK := values[2].(string)
	if !cursorOK || !revisionOK {
		return domain.FeedState{}, errors.New("invalid feed metadata")
	}
	state := domain.FeedState{IDs: ids, Revision: revision, Seen: make(map[uuid.UUID]struct{}, len(seenIDs))}
	if cursorJSON != "" {
		if err := json.Unmarshal([]byte(cursorJSON), &state.Cursor); err != nil {
			return domain.FeedState{}, fmt.Errorf("decode feed cursor: %w", err)
		}
		if state.Cursor != nil && (state.Cursor.PostID == uuid.Nil || state.Cursor.CreatedAt.IsZero()) {
			return domain.FeedState{}, errors.New("invalid feed cursor")
		}
	}
	for _, id := range seenIDs {
		state.Seen[id] = struct{}{}
	}
	return state, nil
}

// CommitFeed atomically replaces pending IDs, advances the cursor and records only the prepared page.
// It returns false without writing when another request has already committed this revision.
func (cache *Cache) CommitFeed(ctx context.Context, userID uuid.UUID, state domain.FeedState, shown []uuid.UUID, feedTTL, seenTTL time.Duration) (bool, error) {
	if feedTTL.Milliseconds() <= 0 || seenTTL.Milliseconds() <= 0 {
		return false, errors.New("feed and seen TTL must be at least one millisecond")
	}
	cursorJSON, err := json.Marshal(state.Cursor)
	if err != nil {
		return false, fmt.Errorf("encode feed cursor: %w", err)
	}
	args := []any{state.Revision, uuid.NewString(), string(cursorJSON), feedTTL.Milliseconds(), seenTTL.Milliseconds(), len(state.IDs)}
	for _, id := range state.IDs {
		args = append(args, id.String())
	}
	for _, id := range shown {
		args = append(args, id.String())
	}
	committed, err := commitFeedState.Run(ctx, cache.client, feedKeys(userID), args...).Int()
	if err != nil {
		return false, fmt.Errorf("commit feed snapshot: %w", err)
	}
	return committed == 1, nil
}

// feedUUIDs decodes a Redis array of post IDs and rejects malformed stored data.
func feedUUIDs(value any) ([]uuid.UUID, error) {
	values, ok := value.([]any)
	if !ok {
		return nil, errors.New("invalid feed ID array")
	}
	ids := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			return nil, errors.New("invalid feed ID")
		}
		id, err := uuid.Parse(text)
		if err != nil || id == uuid.Nil {
			return nil, errors.New("invalid feed UUID")
		}
		ids = append(ids, id)
	}
	return ids, nil
}
