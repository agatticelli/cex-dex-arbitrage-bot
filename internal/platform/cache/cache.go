// Package cache provides caching implementations for the arbitrage detector.
//
// The package includes:
//   - MemoryCache: Fast in-memory LRU cache for L1 caching
//   - RedisCache: Distributed Redis cache for L2 caching
//   - LayeredCache: Two-tier cache combining L1 (memory) and L2 (Redis)
//   - Warmer: Cache warming utilities for pre-populating caches at startup
//
// Cache keys should follow the convention: "provider:type:version:params"
// Example: "dex:quote:v1:12345:0xA0b86991:0xC02aaA39:1000000000000000000:buy:500"
package cache

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrNotFound is returned when a key is not found in cache.
	// Callers should check for this error to distinguish between
	// "key not found" and other cache failures.
	ErrNotFound = errors.New("cache: key not found")

	// ErrInvalidValue is returned when the cached value cannot be
	// type-asserted to the expected type.
	ErrInvalidValue = errors.New("cache: invalid value")
)

// Cache defines the interface for cache operations.
// All implementations must be thread-safe for concurrent access.
//
// Implementations:
//   - MemoryCache: In-memory LRU cache with configurable size
//   - RedisCache: Redis-backed cache with connection pooling
//   - LayeredCache: Combines MemoryCache (L1) and RedisCache (L2)
type Cache interface {
	// Get retrieves a value from cache by key.
	// Returns ErrNotFound if the key doesn't exist or has expired.
	// The returned value should be type-asserted by the caller.
	Get(ctx context.Context, key string) (interface{}, error)

	// Set stores a value in cache with the specified TTL.
	// A TTL of 0 means the value never expires (implementation-dependent).
	// Existing values are overwritten.
	Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error

	// Delete removes a key from cache.
	// Returns nil if the key doesn't exist (idempotent).
	Delete(ctx context.Context, key string) error

	// Close releases any resources held by the cache.
	// After Close, the cache should not be used.
	Close() error
}
