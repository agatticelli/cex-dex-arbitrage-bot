package cache

import (
	"context"
	"time"
)

// LayeredCache implements a two-tier cache (L1: memory, L2: Redis)
type LayeredCache struct {
	l1 Cache // Fast in-memory cache
	l2 Cache // Slower but persistent Redis cache
}

// NewLayeredCache creates a new layered cache
func NewLayeredCache(l1, l2 Cache) *LayeredCache {
	return &LayeredCache{
		l1: l1,
		l2: l2,
	}
}

// Get retrieves a value from cache (L1 → L2 → miss)
func (lc *LayeredCache) Get(ctx context.Context, key string) (interface{}, error) {
	// Try L1 cache first
	if lc.l1 != nil {
		if val, err := lc.l1.Get(ctx, key); err == nil {
			return val, nil
		}
	}

	// Try L2 cache
	if lc.l2 != nil {
		val, err := lc.l2.Get(ctx, key)
		if err == nil {
			// Backfill L1 cache on L2 hit
			if lc.l1 != nil {
				// Use a shorter TTL for L1 (1 minute)
				_ = lc.l1.Set(ctx, key, val, 1*time.Minute)
			}
			return val, nil
		}
	}

	return nil, ErrNotFound
}

// Set stores a value in both cache layers
func (lc *LayeredCache) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	// Write to both caches (write-through strategy)
	var l1Err, l2Err error

	if lc.l1 != nil {
		// L1 gets shorter TTL (typically 1 minute)
		l1TTL := ttl
		if ttl > 1*time.Minute {
			l1TTL = 1 * time.Minute
		}
		l1Err = lc.l1.Set(ctx, key, value, l1TTL)
	}

	if lc.l2 != nil {
		l2Err = lc.l2.Set(ctx, key, value, ttl)
	}

	// Return error only if both failed
	if l1Err != nil && l2Err != nil {
		return l2Err
	}

	return nil
}

// Delete removes a key from both cache layers
func (lc *LayeredCache) Delete(ctx context.Context, key string) error {
	var l1Err, l2Err error

	if lc.l1 != nil {
		l1Err = lc.l1.Delete(ctx, key)
	}

	if lc.l2 != nil {
		l2Err = lc.l2.Delete(ctx, key)
	}

	// Return error if at least one failed
	if l1Err != nil {
		return l1Err
	}
	if l2Err != nil {
		return l2Err
	}

	return nil
}

// Close closes both cache layers
func (lc *LayeredCache) Close() error {
	var l1Err, l2Err error

	if lc.l1 != nil {
		l1Err = lc.l1.Close()
	}

	if lc.l2 != nil {
		l2Err = lc.l2.Close()
	}

	if l1Err != nil {
		return l1Err
	}
	if l2Err != nil {
		return l2Err
	}

	return nil
}

// InvalidateL1 invalidates only L1 cache for a key
// Useful when you want to force a read from L2
func (lc *LayeredCache) InvalidateL1(ctx context.Context, key string) error {
	if lc.l1 != nil {
		return lc.l1.Delete(ctx, key)
	}
	return nil
}
