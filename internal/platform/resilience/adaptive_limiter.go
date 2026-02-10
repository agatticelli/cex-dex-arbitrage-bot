package resilience

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// AdaptiveLimiter adjusts rate limits based on API response patterns.
// It backs off when seeing errors/rate limits, and gradually increases
// when things are healthy.
//
// Algorithm:
//   - Starts at base rate
//   - On rate limit error: cut rate by backoffFactor (e.g., 0.5 = halve)
//   - On consecutive successes: increase by recoveryFactor (e.g., 1.1 = +10%)
//   - Never exceeds maxRate or goes below minRate
//   - Uses exponential backoff for repeated failures
type AdaptiveLimiter struct {
	limiter *RateLimiter

	// Configuration
	baseRate       float64 // Starting rate
	minRate        float64 // Floor rate (never go below)
	maxRate        float64 // Ceiling rate (never exceed)
	backoffFactor  float64 // Multiplier on failure (e.g., 0.5)
	recoveryFactor float64 // Multiplier on success (e.g., 1.1)
	recoveryWindow int     // Consecutive successes needed to increase

	// State
	currentRate         float64
	consecutiveSuccess  int64 // Atomic counter
	consecutiveFailures int64 // Atomic counter for exponential backoff
	lastAdjustment      time.Time
	mu                  sync.RWMutex

	// Metrics
	totalRequests  int64
	rateLimitHits  int64
	adaptations    int64
	currentLevel   int32 // 0=min, 50=base, 100=max (for observability)
}

// AdaptiveLimiterConfig configures the adaptive limiter.
type AdaptiveLimiterConfig struct {
	// Base rate in requests per second (default: 1.0)
	BaseRate float64

	// Minimum rate - floor for backoff (default: 0.1)
	MinRate float64

	// Maximum rate - ceiling for recovery (default: 10.0)
	MaxRate float64

	// Burst size (default: derived from BaseRate)
	Burst int

	// BackoffFactor - rate multiplier on failure (default: 0.5)
	BackoffFactor float64

	// RecoveryFactor - rate multiplier on success (default: 1.1)
	RecoveryFactor float64

	// RecoveryWindow - consecutive successes before increasing (default: 10)
	RecoveryWindow int
}

// NewAdaptiveLimiter creates a new adaptive rate limiter.
func NewAdaptiveLimiter(cfg AdaptiveLimiterConfig) *AdaptiveLimiter {
	// Apply defaults
	if cfg.BaseRate <= 0 {
		cfg.BaseRate = 1.0
	}
	if cfg.MinRate <= 0 {
		cfg.MinRate = 0.1
	}
	if cfg.MaxRate <= 0 {
		cfg.MaxRate = 10.0
	}
	if cfg.Burst <= 0 {
		cfg.Burst = int(cfg.BaseRate * 2)
		if cfg.Burst < 1 {
			cfg.Burst = 1
		}
	}
	if cfg.BackoffFactor <= 0 || cfg.BackoffFactor >= 1 {
		cfg.BackoffFactor = 0.5
	}
	if cfg.RecoveryFactor <= 1 {
		cfg.RecoveryFactor = 1.1
	}
	if cfg.RecoveryWindow <= 0 {
		cfg.RecoveryWindow = 10
	}

	// Ensure minRate <= baseRate <= maxRate
	if cfg.MinRate > cfg.BaseRate {
		cfg.MinRate = cfg.BaseRate
	}
	if cfg.MaxRate < cfg.BaseRate {
		cfg.MaxRate = cfg.BaseRate
	}

	return &AdaptiveLimiter{
		limiter:        NewRateLimiter(cfg.BaseRate, cfg.Burst),
		baseRate:       cfg.BaseRate,
		minRate:        cfg.MinRate,
		maxRate:        cfg.MaxRate,
		backoffFactor:  cfg.BackoffFactor,
		recoveryFactor: cfg.RecoveryFactor,
		recoveryWindow: cfg.RecoveryWindow,
		currentRate:    cfg.BaseRate,
		lastAdjustment: time.Now(),
	}
}

// NewAdaptiveLimiterFromRPM creates an adaptive limiter from RPM values.
func NewAdaptiveLimiterFromRPM(baseRPM, minRPM, maxRPM int) *AdaptiveLimiter {
	return NewAdaptiveLimiter(AdaptiveLimiterConfig{
		BaseRate: float64(baseRPM) / 60.0,
		MinRate:  float64(minRPM) / 60.0,
		MaxRate:  float64(maxRPM) / 60.0,
	})
}

// Wait blocks until a token is available, then records the attempt.
func (a *AdaptiveLimiter) Wait(ctx context.Context) error {
	atomic.AddInt64(&a.totalRequests, 1)
	return a.limiter.Wait(ctx)
}

// Allow checks if a request is allowed without blocking.
func (a *AdaptiveLimiter) Allow() bool {
	atomic.AddInt64(&a.totalRequests, 1)
	return a.limiter.Allow()
}

// RecordSuccess indicates a successful API call.
// After enough consecutive successes, the rate is increased.
func (a *AdaptiveLimiter) RecordSuccess() {
	// Reset failure counter
	atomic.StoreInt64(&a.consecutiveFailures, 0)

	// Increment success counter
	successes := atomic.AddInt64(&a.consecutiveSuccess, 1)

	// Check if we should recover
	if int(successes) >= a.recoveryWindow {
		a.tryRecover()
	}
}

// RecordRateLimitError indicates we hit a rate limit.
// Immediately backs off to reduce pressure.
func (a *AdaptiveLimiter) RecordRateLimitError() {
	atomic.AddInt64(&a.rateLimitHits, 1)
	atomic.StoreInt64(&a.consecutiveSuccess, 0)
	failures := atomic.AddInt64(&a.consecutiveFailures, 1)

	a.backoff(int(failures))
}

// RecordError indicates a non-rate-limit error.
// Doesn't trigger backoff but resets success counter.
func (a *AdaptiveLimiter) RecordError() {
	atomic.StoreInt64(&a.consecutiveSuccess, 0)
}

// backoff reduces the rate based on failure count.
func (a *AdaptiveLimiter) backoff(failureCount int) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Exponential backoff: rate * (backoffFactor ^ failureCount)
	// Capped at 5 consecutive failures to avoid extreme slowdown
	if failureCount > 5 {
		failureCount = 5
	}

	multiplier := 1.0
	for i := 0; i < failureCount; i++ {
		multiplier *= a.backoffFactor
	}

	newRate := a.currentRate * multiplier
	if newRate < a.minRate {
		newRate = a.minRate
	}

	if newRate != a.currentRate {
		a.currentRate = newRate
		a.limiter.SetRate(newRate)
		a.lastAdjustment = time.Now()
		atomic.AddInt64(&a.adaptations, 1)
		a.updateLevel()
	}
}

// tryRecover attempts to increase the rate after consecutive successes.
func (a *AdaptiveLimiter) tryRecover() {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Reset success counter
	atomic.StoreInt64(&a.consecutiveSuccess, 0)

	// Don't increase if already at max
	if a.currentRate >= a.maxRate {
		return
	}

	// Don't increase too quickly - minimum 1 second between increases
	if time.Since(a.lastAdjustment) < time.Second {
		return
	}

	newRate := a.currentRate * a.recoveryFactor
	if newRate > a.maxRate {
		newRate = a.maxRate
	}

	if newRate != a.currentRate {
		a.currentRate = newRate
		a.limiter.SetRate(newRate)
		a.lastAdjustment = time.Now()
		atomic.AddInt64(&a.adaptations, 1)
		a.updateLevel()
	}
}

// updateLevel calculates the current level (0-100) for observability.
func (a *AdaptiveLimiter) updateLevel() {
	// Calculate where current rate falls between min and max
	// 0 = at min, 50 = at base, 100 = at max
	var level int32
	if a.currentRate <= a.baseRate {
		// Between min and base
		ratio := (a.currentRate - a.minRate) / (a.baseRate - a.minRate)
		level = int32(ratio * 50)
	} else {
		// Between base and max
		ratio := (a.currentRate - a.baseRate) / (a.maxRate - a.baseRate)
		level = 50 + int32(ratio*50)
	}
	atomic.StoreInt32(&a.currentLevel, level)
}

// Reset restores the limiter to its base rate.
func (a *AdaptiveLimiter) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.currentRate = a.baseRate
	a.limiter.SetRate(a.baseRate)
	atomic.StoreInt64(&a.consecutiveSuccess, 0)
	atomic.StoreInt64(&a.consecutiveFailures, 0)
	a.lastAdjustment = time.Now()
	a.updateLevel()
}

// Stats returns current limiter statistics.
type AdaptiveLimiterStats struct {
	CurrentRate     float64 // Current rate in req/sec
	BaseRate        float64 // Base rate
	MinRate         float64 // Floor rate
	MaxRate         float64 // Ceiling rate
	Level           int     // 0-100 (0=min, 50=base, 100=max)
	TotalRequests   int64
	RateLimitHits   int64
	Adaptations     int64
	AvailableTokens float64
}

// Stats returns current statistics.
func (a *AdaptiveLimiter) Stats() AdaptiveLimiterStats {
	a.mu.RLock()
	currentRate := a.currentRate
	a.mu.RUnlock()

	_, _, tokens := a.limiter.Stats()

	return AdaptiveLimiterStats{
		CurrentRate:     currentRate,
		BaseRate:        a.baseRate,
		MinRate:         a.minRate,
		MaxRate:         a.maxRate,
		Level:           int(atomic.LoadInt32(&a.currentLevel)),
		TotalRequests:   atomic.LoadInt64(&a.totalRequests),
		RateLimitHits:   atomic.LoadInt64(&a.rateLimitHits),
		Adaptations:     atomic.LoadInt64(&a.adaptations),
		AvailableTokens: tokens,
	}
}

// CurrentRate returns the current rate in requests per second.
func (a *AdaptiveLimiter) CurrentRate() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.currentRate
}

// IsThrottled returns true if we're operating below base rate.
func (a *AdaptiveLimiter) IsThrottled() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.currentRate < a.baseRate
}
