// Package cache provides caching functionality with warming support.
package cache

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/observability"
)

// WarmupProvider defines the interface for providers that can warm the cache.
// Implementations should fetch their relevant data and populate the cache.
type WarmupProvider interface {
	// Name returns a human-readable name for logging purposes
	Name() string

	// Warmup pre-populates the cache with initial data.
	// It should be idempotent and safe to call multiple times.
	Warmup(ctx context.Context) error
}

// WarmupConfig configures the cache warming behavior.
type WarmupConfig struct {
	// Timeout is the maximum duration to wait for all providers to complete
	Timeout time.Duration

	// ContinueOnError determines whether to continue warming if a provider fails
	ContinueOnError bool

	// Parallel determines whether to warm providers in parallel
	Parallel bool
}

// DefaultWarmupConfig returns sensible defaults for cache warming.
func DefaultWarmupConfig() WarmupConfig {
	return WarmupConfig{
		Timeout:         30 * time.Second,
		ContinueOnError: true,
		Parallel:        true,
	}
}

// WarmupResult contains the result of warming a single provider.
type WarmupResult struct {
	Provider string
	Duration time.Duration
	Err      error
}

// WarmupResults contains the aggregate results of cache warming.
type WarmupResults struct {
	Results   []WarmupResult
	TotalTime time.Duration
	Errors    int
}

// HasErrors returns true if any provider failed during warmup.
func (wr *WarmupResults) HasErrors() bool {
	return wr.Errors > 0
}

// Warmer handles cache warming operations.
type Warmer struct {
	providers []WarmupProvider
	logger    *observability.Logger
	config    WarmupConfig
}

// NewWarmer creates a new cache warmer.
func NewWarmer(logger *observability.Logger, config WarmupConfig) *Warmer {
	return &Warmer{
		providers: make([]WarmupProvider, 0),
		logger:    logger,
		config:    config,
	}
}

// RegisterProvider adds a warmup provider to the warmer.
func (w *Warmer) RegisterProvider(provider WarmupProvider) {
	w.providers = append(w.providers, provider)
}

// Warmup executes all registered warmup providers.
// Returns aggregate results including timing and errors.
func (w *Warmer) Warmup(ctx context.Context) *WarmupResults {
	start := time.Now()
	results := &WarmupResults{
		Results: make([]WarmupResult, 0, len(w.providers)),
	}

	if len(w.providers) == 0 {
		results.TotalTime = time.Since(start)
		return results
	}

	// Apply timeout
	warmupCtx, cancel := context.WithTimeout(ctx, w.config.Timeout)
	defer cancel()

	if w.config.Parallel {
		results.Results = w.warmupParallel(warmupCtx)
	} else {
		results.Results = w.warmupSequential(warmupCtx)
	}

	// Count errors
	for _, r := range results.Results {
		if r.Err != nil {
			results.Errors++
		}
	}

	results.TotalTime = time.Since(start)

	// Log summary
	if results.Errors > 0 {
		w.logger.LogWarn(ctx, fmt.Sprintf("Cache warmup completed with %d/%d errors in %v",
			results.Errors, len(w.providers), results.TotalTime))
	} else {
		w.logger.LogInfo(ctx, fmt.Sprintf("Cache warmup completed successfully (%d providers) in %v",
			len(w.providers), results.TotalTime))
	}

	return results
}

// warmupParallel warms all providers concurrently.
func (w *Warmer) warmupParallel(ctx context.Context) []WarmupResult {
	var wg sync.WaitGroup
	resultsCh := make(chan WarmupResult, len(w.providers))

	for _, provider := range w.providers {
		wg.Add(1)
		go func(p WarmupProvider) {
			defer wg.Done()
			resultsCh <- w.warmupProvider(ctx, p)
		}(provider)
	}

	// Wait for all to complete
	wg.Wait()
	close(resultsCh)

	// Collect results
	results := make([]WarmupResult, 0, len(w.providers))
	for r := range resultsCh {
		results = append(results, r)
	}

	return results
}

// warmupSequential warms providers one at a time.
func (w *Warmer) warmupSequential(ctx context.Context) []WarmupResult {
	results := make([]WarmupResult, 0, len(w.providers))

	for _, provider := range w.providers {
		result := w.warmupProvider(ctx, provider)
		results = append(results, result)

		// Stop on first error if not configured to continue
		if result.Err != nil && !w.config.ContinueOnError {
			break
		}
	}

	return results
}

// warmupProvider warms a single provider and returns the result.
func (w *Warmer) warmupProvider(ctx context.Context, provider WarmupProvider) WarmupResult {
	start := time.Now()
	name := provider.Name()

	w.logger.LogDebug(ctx, fmt.Sprintf("Warming cache: %s", name))

	err := provider.Warmup(ctx)
	duration := time.Since(start)

	if err != nil {
		w.logger.LogWarn(ctx, fmt.Sprintf("Cache warmup failed for %s: %v (took %v)", name, err, duration))
	} else {
		w.logger.LogDebug(ctx, fmt.Sprintf("Cache warmup completed for %s in %v", name, duration))
	}

	return WarmupResult{
		Provider: name,
		Duration: duration,
		Err:      err,
	}
}
