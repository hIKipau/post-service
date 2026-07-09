package redis

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/redis/go-redis/v9"
)

type Redis struct {
	client *redis.Client
	logger *slog.Logger
}

func New(ctx context.Context, redisURL string, logger *slog.Logger) (*Redis, error) {
	logger.Info("Connecting to Redis...")

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("error parsing Redis URL: %w", err)
	}
	rdb := redis.NewClient(opts)

	if err = rdb.Ping(ctx).Err(); err != nil {
		closeErr := rdb.Close()
		if closeErr != nil {
			return nil, fmt.Errorf("error connecting to Redis: %w; close Redis client: %w", err, closeErr)
		}
		return nil, fmt.Errorf("error connecting to Redis: %w", err)
	}

	logger.Info("Successfully connected to Redis")
	return &Redis{client: rdb, logger: logger}, nil
}

func (rdb *Redis) Close() error {
	return rdb.client.Close()
}
