package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// typedValue wraps a cached value with its type information for proper deserialization
type typedValue struct {
	TypeName string          `json:"type"`
	Data     json.RawMessage `json:"data"`
}

// typeRegistry maps type names to their reflect.Type for proper deserialization
var (
	typeRegistry   = make(map[string]reflect.Type)
	typeRegistryMu sync.RWMutex
)

// RegisterCacheType registers a type for proper Redis cache deserialization.
// Call this during initialization for any pointer types you want to cache.
// Example: cache.RegisterCacheType((*pricing.Orderbook)(nil))
func RegisterCacheType(prototype interface{}) {
	t := reflect.TypeOf(prototype)
	if t == nil {
		return
	}
	// Store the element type for pointer types
	typeName := t.String()

	typeRegistryMu.Lock()
	defer typeRegistryMu.Unlock()
	typeRegistry[typeName] = t
}

// getRegisteredType returns the registered type for a type name
func getRegisteredType(typeName string) (reflect.Type, bool) {
	typeRegistryMu.RLock()
	defer typeRegistryMu.RUnlock()
	t, ok := typeRegistry[typeName]
	return t, ok
}

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
// If the value was stored with type information (via Set), it will be deserialized
// to the correct type. Otherwise, it falls back to generic JSON unmarshaling.
func (r *RedisCache) Get(ctx context.Context, key string) (interface{}, error) {
	val, err := r.client.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("redis get error: %w", err)
	}

	// Try to unmarshal as typed value first
	var tv typedValue
	if err := json.Unmarshal([]byte(val), &tv); err == nil && tv.TypeName != "" {
		// We have type information, try to deserialize to the registered type
		if registeredType, ok := getRegisteredType(tv.TypeName); ok {
			// Create a new instance of the registered type
			var result interface{}
			if registeredType.Kind() == reflect.Ptr {
				// For pointer types, create a new instance of the pointed-to type
				result = reflect.New(registeredType.Elem()).Interface()
			} else {
				result = reflect.New(registeredType).Interface()
			}

			if err := json.Unmarshal(tv.Data, result); err != nil {
				return nil, fmt.Errorf("failed to unmarshal typed value: %w", err)
			}

			// For non-pointer registered types, return the value not the pointer
			if registeredType.Kind() != reflect.Ptr {
				return reflect.ValueOf(result).Elem().Interface(), nil
			}
			return result, nil
		}
		// Type not registered, fall back to raw data as interface{}
		var result interface{}
		if err := json.Unmarshal(tv.Data, &result); err != nil {
			return nil, fmt.Errorf("failed to unmarshal value: %w", err)
		}
		return result, nil
	}

	// Legacy format: plain JSON without type wrapper
	var result interface{}
	if err := json.Unmarshal([]byte(val), &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal value: %w", err)
	}

	return result, nil
}

// Set stores a value in Redis cache with TTL
// The value is wrapped with type information to enable proper deserialization on Get.
func (r *RedisCache) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	// Get the type name for the value
	t := reflect.TypeOf(value)
	typeName := ""
	if t != nil {
		typeName = t.String()
	}

	// Serialize the value data
	valueData, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal value: %w", err)
	}

	// Wrap with type information
	tv := typedValue{
		TypeName: typeName,
		Data:     valueData,
	}

	data, err := json.Marshal(tv)
	if err != nil {
		return fmt.Errorf("failed to marshal typed value: %w", err)
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
