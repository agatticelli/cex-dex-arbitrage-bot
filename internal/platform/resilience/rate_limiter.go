package resilience

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	// ErrRateLimitExceeded is returned when rate limit is exceeded
	ErrRateLimitExceeded = errors.New("rate limit exceeded")
)

// RateLimiter implements token bucket rate limiting
type RateLimiter struct {
	rate       float64   // Tokens per second
	burst      int       // Max tokens (bucket size)
	tokens     float64   // Current tokens
	lastUpdate time.Time // Last token refill time
	mu         sync.Mutex
}

// NewRateLimiter creates a new rate limiter
// rate: number of requests per second
// burst: maximum burst size
func NewRateLimiter(rate float64, burst int) *RateLimiter {
	if rate <= 0 {
		rate = 10 // default: 10 requests/sec
	}
	if burst <= 0 {
		burst = int(rate) // default burst = rate
	}

	return &RateLimiter{
		rate:       rate,
		burst:      burst,
		tokens:     float64(burst), // Start with full bucket
		lastUpdate: time.Now(),
	}
}

// NewRateLimiterFromRPM creates a rate limiter from requests per minute
func NewRateLimiterFromRPM(requestsPerMinute int, burst int) *RateLimiter {
	rate := float64(requestsPerMinute) / 60.0
	return NewRateLimiter(rate, burst)
}

// Allow checks if a request is allowed without blocking
func (rl *RateLimiter) Allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Refill tokens based on time elapsed
	rl.refill()

	// Check if we have tokens available
	if rl.tokens >= 1.0 {
		rl.tokens -= 1.0
		return true
	}

	return false
}

// Wait blocks until a token is available or context is cancelled
func (rl *RateLimiter) Wait(ctx context.Context) error {
	for {
		// Try to acquire token
		if rl.Allow() {
			return nil
		}

		// Calculate wait time
		waitTime := rl.calculateWaitTime()

		// Wait or return if context cancelled
		select {
		case <-time.After(waitTime):
			// Continue to next iteration
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// refill adds tokens based on elapsed time (caller must hold lock)
func (rl *RateLimiter) refill() {
	now := time.Now()
	elapsed := now.Sub(rl.lastUpdate)

	// Calculate tokens to add
	tokensToAdd := elapsed.Seconds() * rl.rate

	// Add tokens (capped at burst size)
	rl.tokens += tokensToAdd
	if rl.tokens > float64(rl.burst) {
		rl.tokens = float64(rl.burst)
	}

	rl.lastUpdate = now
}

// calculateWaitTime calculates how long to wait for next token
func (rl *RateLimiter) calculateWaitTime() time.Duration {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Time to add 1 token = 1 / rate
	timePerToken := 1.0 / rl.rate

	// If we have partial tokens, we need to wait less
	tokensNeeded := 1.0 - rl.tokens
	if tokensNeeded < 0 {
		tokensNeeded = 0
	}

	waitTime := time.Duration(tokensNeeded*timePerToken) * time.Second

	// Minimum wait time to avoid busy-waiting
	if waitTime < 10*time.Millisecond {
		waitTime = 10 * time.Millisecond
	}

	return waitTime
}

// AllowN checks if N requests are allowed
func (rl *RateLimiter) AllowN(n int) bool {
	if n <= 0 {
		return true
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Refill tokens
	rl.refill()

	// Check if we have enough tokens
	if rl.tokens >= float64(n) {
		rl.tokens -= float64(n)
		return true
	}

	return false
}

// SetRate changes the rate limit (requests per second)
func (rl *RateLimiter) SetRate(rate float64) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.rate = rate
}

// SetBurst changes the burst size
func (rl *RateLimiter) SetBurst(burst int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.burst = burst

	// Cap current tokens at new burst size
	if rl.tokens > float64(burst) {
		rl.tokens = float64(burst)
	}
}

// Stats returns current rate limiter statistics
func (rl *RateLimiter) Stats() (rate float64, burst int, availableTokens float64) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.refill() // Refill before returning stats
	return rl.rate, rl.burst, rl.tokens
}

// Reset resets the rate limiter to full capacity
func (rl *RateLimiter) Reset() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.tokens = float64(rl.burst)
	rl.lastUpdate = time.Now()
}
