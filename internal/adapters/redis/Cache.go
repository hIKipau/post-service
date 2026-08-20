package redis

import (
	"context"
	"errors"
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

//кэш хранит лайки и кол-во репли.
//кэш хранит ленту пользователя в виде айдишников
//кэш хранит просмотренные юзером посты(с ttl - до недели)

// SetFeed generates and caches the user's feed with the specified TTL.
func (cache *Cache) SetFeed(ctx context.Context, userID uuid.UUID, postIDs []uuid.UUID, ttl time.Duration) error {
	key := "feed:" + userID.String()

}

// GetFeed returns up to limit post UUIDs from the user's cached feed starting at the given offset.
func (cache *Cache) GetFeed(ctx context.Context, userID uuid.UUID, offset int64, limit int64) ([]uuid.UUID, error) {
	key := "feed:" + userID.String()
	value, err := cache.client.LRange(ctx, key, offset, limit).Result()

	if errors.Is(err, redis.Nil) {
		cache.logger.Error("Cant get feed from cache", "error", err, "userId", userID)
		return []uuid.UUID{}, errors.New("cache Get Feed Error")
	}

	result := make([]uuid.UUID, 0, len(value))

	for _, v := range value {
		id, err := uuid.Parse(v)
		if err != nil {
			cache.logger.Error("Cant parse posts uuid from feed", "error", err, "userId", userID)
			return []uuid.UUID{}, errors.New("cache Get Feed Parse uuids's Error")
		}
		result = append(result, id)
	}
	return result, nil
}

func (cache *Cache) TrimFeed(ctx context.Context, userID uuid.UUID, count int64) error {

}

func (cache *Cache) DeleteFeed(ctx context.Context, userID uuid.UUID) error {

}

func (cache *Cache) GetSeen(ctx context.Context, userID uuid.UUID) (map[uuid.UUID]struct{}, error) {

}

func (cache *Cache) AddSeen(ctx context.Context, userID uuid.UUID, postIDs []uuid.UUID, ttl time.Duration) error {

}
