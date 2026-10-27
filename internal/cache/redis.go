package cache

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/redis/go-redis/v9"
)

const opTimeout = 500 * time.Millisecond

// RedisCache is a JSON cache with TTL jitter and a short per-operation timeout.
type RedisCache struct {
	client *redis.Client
	ttl    time.Duration
	jitter float64 // fraction of ttl, e.g. 0.1 => +/-10%
}

func NewRedisCache(addr string, ttl time.Duration) *RedisCache {
	return &RedisCache{
		client: redis.NewClient(&redis.Options{
			Addr:         addr,
			DialTimeout:  2 * time.Second,
			ReadTimeout:  2 * time.Second,
			WriteTimeout: 2 * time.Second,
		}),
		ttl:    ttl,
		jitter: 0.1,
	}
}

func (c *RedisCache) Get(ctx context.Context, key string, dest any) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()

	data, err := c.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(data, dest); err != nil {
		return false, err
	}
	return true, nil
}

func (c *RedisCache) Set(ctx context.Context, key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	return c.client.Set(ctx, key, data, c.jitteredTTL()).Err()
}

// DeletePrefix removes keys with the given prefix using SCAN (never KEYS).
func (c *RedisCache) DeletePrefix(ctx context.Context, prefix string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	iter := c.client.Scan(ctx, 0, prefix+"*", 100).Iterator()
	var keys []string
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	if err := iter.Err(); err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}
	return c.client.Del(ctx, keys...).Err()
}

func (c *RedisCache) jitteredTTL() time.Duration {
	if c.jitter <= 0 {
		return c.ttl
	}
	delta := (rand.Float64()*2 - 1) * c.jitter * float64(c.ttl)
	return c.ttl + time.Duration(delta)
}

func (c *RedisCache) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return c.client.Ping(ctx).Err()
}

func (c *RedisCache) Close() error { return c.client.Close() }
