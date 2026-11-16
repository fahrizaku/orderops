package audit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Limiter decides whether one more request from a key is allowed.
type Limiter interface {
	Allow(ctx context.Context, key string) (bool, error)
}

// RedisLimiter is a fixed-window rate limiter: each key gets at most
// limit requests per minute. The counter lives in Redis, so the limit is
// shared correctly if the service is ever scaled to several instances.
type RedisLimiter struct {
	client *redis.Client
	limit  int64
}

func NewRedisLimiter(client *redis.Client, limit int64) *RedisLimiter {
	return &RedisLimiter{client: client, limit: limit}
}

func (l *RedisLimiter) Allow(ctx context.Context, key string) (bool, error) {
	bucket := fmt.Sprintf("ratelimit:%s:%d", key, time.Now().Unix()/60)
	count, err := l.client.Incr(ctx, bucket).Result()
	if err != nil {
		return false, err
	}
	if count == 1 {
		// First hit in this window: make the counter expire with it.
		_ = l.client.Expire(ctx, bucket, 70*time.Second).Err()
	}
	return count <= l.limit, nil
}
