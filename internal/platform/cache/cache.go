package cache

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrNotFound is returned when a key is not found in cache
	ErrNotFound = errors.New("cache: key not found")

	// ErrInvalidValue is returned when cache value is invalid
	ErrInvalidValue = errors.New("cache: invalid value")
)

// Cache defines the interface for cache operations
type Cache interface {
	// Get retrieves a value from cache
	Get(ctx context.Context, key string) (interface{}, error)

	// Set stores a value in cache with TTL
	Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error

	// Delete removes a key from cache
	Delete(ctx context.Context, key string) error

	// Close closes the cache connection
	Close() error
}
