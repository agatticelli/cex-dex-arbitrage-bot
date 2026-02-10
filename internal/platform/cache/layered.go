package cache

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// LayeredCache implements a two-tier cache (L1: memory, L2: Redis)
type LayeredCache struct {
	l1        Cache        // Fast in-memory cache
	l2        Cache        // Slower but persistent Redis cache
	logger    *slog.Logger // Optional logger for debugging L1 errors
	l1MaxTTL  time.Duration // Maximum TTL for L1 cache entries (default: 1 minute)
}

// DefaultL1MaxTTL is the default maximum TTL for L1 cache entries
const DefaultL1MaxTTL = 1 * time.Minute

// NewLayeredCache creates a new layered cache with default L1 TTL (1 minute)
func NewLayeredCache(l1, l2 Cache) *LayeredCache {
	return &LayeredCache{
		l1:       l1,
		l2:       l2,
		l1MaxTTL: DefaultL1MaxTTL,
	}
}

// NewLayeredCacheWithLogger creates a new layered cache with optional logging
// When logger is provided, L1 errors (other than ErrNotFound) are logged for debugging
func NewLayeredCacheWithLogger(l1, l2 Cache, logger *slog.Logger) *LayeredCache {
	return &LayeredCache{
		l1:       l1,
		l2:       l2,
		logger:   logger,
		l1MaxTTL: DefaultL1MaxTTL,
	}
}

// LayeredCacheConfig holds configuration for creating a LayeredCache
type LayeredCacheConfig struct {
	L1       Cache         // Fast in-memory cache (required)
	L2       Cache         // Slower persistent cache (optional)
	Logger   *slog.Logger  // Optional logger for debugging
	L1MaxTTL time.Duration // Maximum TTL for L1 entries (default: 1 minute)
}

// NewLayeredCacheWithConfig creates a new layered cache with full configuration
func NewLayeredCacheWithConfig(cfg LayeredCacheConfig) *LayeredCache {
	l1MaxTTL := cfg.L1MaxTTL
	if l1MaxTTL == 0 {
		l1MaxTTL = DefaultL1MaxTTL
	}
	return &LayeredCache{
		l1:       cfg.L1,
		l2:       cfg.L2,
		logger:   cfg.Logger,
		l1MaxTTL: l1MaxTTL,
	}
}

// Get retrieves a value from cache (L1 → L2 → miss)
// Error handling:
//   - ErrNotFound from L1: continue to L2 (normal cache miss)
//   - Other L1 errors: log warning and continue to L2 (graceful degradation)
//   - ErrNotFound from L2: return ErrNotFound
//   - Other L2 errors: return the error (L2 is the source of truth)
func (lc *LayeredCache) Get(ctx context.Context, key string) (interface{}, error) {
	// Try L1 cache first
	if lc.l1 != nil {
		val, err := lc.l1.Get(ctx, key)
		if err == nil {
			return val, nil
		}
		// For L1, we continue to L2 on any error (including non-ErrNotFound)
		// This provides graceful degradation if L1 has issues
		// Log non-ErrNotFound errors for debugging (L1 issues shouldn't be silent)
		if !errors.Is(err, ErrNotFound) && lc.logger != nil {
			lc.logger.Warn("L1 cache error, falling back to L2",
				"key", key,
				"error", err.Error(),
			)
		}
	}

	// Try L2 cache
	if lc.l2 != nil {
		val, err := lc.l2.Get(ctx, key)
		if err == nil {
			// Backfill L1 cache on L2 hit
			if lc.l1 != nil {
				// Use the configured L1 max TTL for backfill
				// Note: We don't know the remaining TTL from L2, so we use a conservative L1 TTL
				// This ensures L1 entries expire quickly and get refreshed from L2
				if backfillErr := lc.l1.Set(ctx, key, val, lc.l1MaxTTL); backfillErr != nil && lc.logger != nil {
					lc.logger.Debug("L1 cache backfill failed",
						"key", key,
						"error", backfillErr.Error(),
					)
				}
			}
			return val, nil
		}
		// For L2, propagate non-ErrNotFound errors (L2 is the source of truth)
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}

	return nil, ErrNotFound
}

// Set stores a value in both cache layers
func (lc *LayeredCache) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	// Write to both caches (write-through strategy)
	var l1Err, l2Err error

	if lc.l1 != nil {
		// L1 gets shorter TTL (capped at l1MaxTTL)
		l1TTL := ttl
		if ttl > lc.l1MaxTTL {
			l1TTL = lc.l1MaxTTL
		}
		l1Err = lc.l1.Set(ctx, key, value, l1TTL)
	}

	if lc.l2 != nil {
		l2Err = lc.l2.Set(ctx, key, value, ttl)
	}

	// Log L1 errors when L2 succeeds (L1 issues shouldn't be completely silent)
	if l1Err != nil && l2Err == nil && lc.logger != nil {
		lc.logger.Warn("L1 cache set failed, L2 succeeded",
			"key", key,
			"error", l1Err.Error(),
		)
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
