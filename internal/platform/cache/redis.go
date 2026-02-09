package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisCache implements a Redis-backed cache
type RedisCache struct {
	client *redis.Client
}

// NewRedisCache creates a new Redis cache
func NewRedisCache(addr, password string, db int) (*RedisCache, error) {
	client := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           db,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     10,
		MinIdleConns: 5,
	})

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return &RedisCache{
		client: client,
	}, nil
}

// Get retrieves a value from Redis cache
func (r *RedisCache) Get(ctx context.Context, key string) (interface{}, error) {
	val, err := r.client.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("redis get error: %w", err)
	}

	// Deserialize JSON
	var result interface{}
	if err := json.Unmarshal([]byte(val), &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal value: %w", err)
	}

	return result, nil
}

// Set stores a value in Redis cache with TTL
func (r *RedisCache) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	// Serialize to JSON
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal value: %w", err)
	}

	if err := r.client.Set(ctx, key, data, ttl).Err(); err != nil {
		return fmt.Errorf("redis set error: %w", err)
	}

	return nil
}

// Delete removes a key from Redis cache
func (r *RedisCache) Delete(ctx context.Context, key string) error {
	if err := r.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("redis delete error: %w", err)
	}
	return nil
}

// Close closes the Redis connection
func (r *RedisCache) Close() error {
	return r.client.Close()
}

// Ping checks if Redis is reachable
func (r *RedisCache) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}
