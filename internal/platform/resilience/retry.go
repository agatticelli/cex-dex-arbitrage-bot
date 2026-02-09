package resilience

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"
)

// RetryConfig holds retry configuration
type RetryConfig struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	Jitter      float64 // 0.0 to 1.0
}

// DefaultRetryConfig returns default retry configuration
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Second,
		MaxDelay:    30 * time.Second,
		Jitter:      0.1,
	}
}

// Retry executes a function with exponential backoff retry logic
func Retry(ctx context.Context, cfg RetryConfig, fn func(context.Context) error) error {
	var lastErr error

	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		// Execute function
		err := fn(ctx)
		if err == nil {
			return nil
		}

		lastErr = err

		// Check if context is cancelled
		if ctx.Err() != nil {
			return fmt.Errorf("retry cancelled: %w", ctx.Err())
		}

		// Don't sleep after last attempt
		if attempt == cfg.MaxAttempts-1 {
			break
		}

		// Calculate backoff with exponential delay and jitter
		delay := calculateBackoff(attempt, cfg.BaseDelay, cfg.MaxDelay, cfg.Jitter)

		// Wait before next retry
		select {
		case <-time.After(delay):
			// Continue to next attempt
		case <-ctx.Done():
			return fmt.Errorf("retry cancelled during backoff: %w", ctx.Err())
		}
	}

	return fmt.Errorf("max retry attempts reached: %w", lastErr)
}

// RetryWithResult executes a function with retry and returns a result
func RetryWithResult[T any](ctx context.Context, cfg RetryConfig, fn func(context.Context) (T, error)) (T, error) {
	var result T
	var lastErr error

	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		// Execute function
		res, err := fn(ctx)
		if err == nil {
			return res, nil
		}

		lastErr = err

		// Check if context is cancelled
		if ctx.Err() != nil {
			return result, fmt.Errorf("retry cancelled: %w", ctx.Err())
		}

		// Don't sleep after last attempt
		if attempt == cfg.MaxAttempts-1 {
			break
		}

		// Calculate backoff
		delay := calculateBackoff(attempt, cfg.BaseDelay, cfg.MaxDelay, cfg.Jitter)

		// Wait before next retry
		select {
		case <-time.After(delay):
			// Continue to next attempt
		case <-ctx.Done():
			return result, fmt.Errorf("retry cancelled during backoff: %w", ctx.Err())
		}
	}

	return result, fmt.Errorf("max retry attempts reached: %w", lastErr)
}

// calculateBackoff calculates delay with exponential backoff and jitter
func calculateBackoff(attempt int, baseDelay, maxDelay time.Duration, jitter float64) time.Duration {
	// Exponential backoff: baseDelay * 2^attempt
	delay := float64(baseDelay) * math.Pow(2, float64(attempt))

	// Cap at max delay
	if delay > float64(maxDelay) {
		delay = float64(maxDelay)
	}

	// Add jitter: randomize delay by ±jitter percent
	if jitter > 0 {
		jitterAmount := delay * jitter
		jitterRange := jitterAmount * 2
		delay = delay - jitterAmount + (rand.Float64() * jitterRange)
	}

	return time.Duration(delay)
}

// IsRetryable determines if an error is retryable
// Default implementation - can be customized per use case
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, ErrCircuitOpen) {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}

	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "execution reverted") || strings.Contains(msg, "revert") {
		return false
	}
	if strings.Contains(msg, "invalid argument") {
		return false
	}
	if strings.Contains(msg, "status code 4") && !strings.Contains(msg, "status code 429") {
		return false
	}

	return true
}

// RetryIf executes a function with retry only if error is retryable
func RetryIf(ctx context.Context, cfg RetryConfig, isRetryable func(error) bool, fn func(context.Context) error) error {
	var lastErr error

	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		err := fn(ctx)
		if err == nil {
			return nil
		}

		lastErr = err

		// Check if error is retryable
		if !isRetryable(err) {
			return fmt.Errorf("non-retryable error: %w", err)
		}

		// Check if context is cancelled
		if ctx.Err() != nil {
			return fmt.Errorf("retry cancelled: %w", ctx.Err())
		}

		// Don't sleep after last attempt
		if attempt == cfg.MaxAttempts-1 {
			break
		}

		// Calculate backoff
		delay := calculateBackoff(attempt, cfg.BaseDelay, cfg.MaxDelay, cfg.Jitter)

		// Wait before next retry
		select {
		case <-time.After(delay):
			// Continue to next attempt
		case <-ctx.Done():
			return fmt.Errorf("retry cancelled during backoff: %w", ctx.Err())
		}
	}

	return fmt.Errorf("max retry attempts reached: %w", lastErr)
}

// RetryIfWithResult executes a function with retry (returning a result) only if error is retryable
func RetryIfWithResult[T any](ctx context.Context, cfg RetryConfig, isRetryable func(error) bool, fn func(context.Context) (T, error)) (T, error) {
	var result T
	var lastErr error

	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		res, err := fn(ctx)
		if err == nil {
			return res, nil
		}

		lastErr = err

		// Check if error is retryable
		if !isRetryable(err) {
			return result, fmt.Errorf("non-retryable error: %w", err)
		}

		// Check if context is cancelled
		if ctx.Err() != nil {
			return result, fmt.Errorf("retry cancelled: %w", ctx.Err())
		}

		// Don't sleep after last attempt
		if attempt == cfg.MaxAttempts-1 {
			break
		}

		// Calculate backoff
		delay := calculateBackoff(attempt, cfg.BaseDelay, cfg.MaxDelay, cfg.Jitter)

		// Wait before next retry
		select {
		case <-time.After(delay):
			// Continue to next attempt
		case <-ctx.Done():
			return result, fmt.Errorf("retry cancelled during backoff: %w", ctx.Err())
		}
	}

	return result, fmt.Errorf("max retry attempts reached: %w", lastErr)
}
