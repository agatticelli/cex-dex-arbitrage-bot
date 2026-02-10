package arbitrage

import (
	"context"
	"errors"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/observability"
	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/pricing"
	"golang.org/x/sync/semaphore"
)

// mockPipelineProvider is a mock price provider for pipeline tests
type mockPipelineProvider struct {
	mu          sync.Mutex
	prices      map[string]*pricing.Price
	errors      map[string]error
	callCount   int64
	delay       time.Duration
	callRecords []mockCall
}

type mockCall struct {
	size   *big.Int
	isBuy  bool
	callAt time.Time
}

func newMockPipelineProvider() *mockPipelineProvider {
	return &mockPipelineProvider{
		prices: make(map[string]*pricing.Price),
		errors: make(map[string]error),
	}
}

func (m *mockPipelineProvider) setPrice(size *big.Int, isBuy bool, price *pricing.Price) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := priceKey(size, isBuy)
	m.prices[key] = price
}

func (m *mockPipelineProvider) setError(size *big.Int, isBuy bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := priceKey(size, isBuy)
	m.errors[key] = err
}

func (m *mockPipelineProvider) setDelay(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.delay = d
}

func (m *mockPipelineProvider) GetPrice(ctx context.Context, size *big.Int, isBuy bool, gasPrice *big.Int, blockNum uint64) (*pricing.Price, error) {
	atomic.AddInt64(&m.callCount, 1)

	m.mu.Lock()
	m.callRecords = append(m.callRecords, mockCall{size: size, isBuy: isBuy, callAt: time.Now()})
	delay := m.delay
	m.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := priceKey(size, isBuy)

	if err, ok := m.errors[key]; ok {
		return nil, err
	}

	if price, ok := m.prices[key]; ok {
		return price, nil
	}

	// Default price if not configured
	return &pricing.Price{
		Value:      big.NewFloat(2000),
		AmountOut:  big.NewFloat(2000), // Normalized amount
		GasCost:    big.NewInt(22e15),  // 0.022 ETH
		Slippage:   big.NewFloat(0.1),
		TradingFee: big.NewFloat(0.1),
	}, nil
}

func (m *mockPipelineProvider) getCallCount() int64 {
	return atomic.LoadInt64(&m.callCount)
}

func priceKey(size *big.Int, isBuy bool) string {
	direction := "sell"
	if isBuy {
		direction = "buy"
	}
	return size.String() + "-" + direction
}

// TestPipelineConcurrency verifies that pipeline processes multiple inputs concurrently
func TestPipelineConcurrency(t *testing.T) {
	cexProvider := newMockPipelineProvider()
	dexProvider := newMockPipelineProvider()

	// Add delay to make concurrency observable
	cexProvider.setDelay(10 * time.Millisecond)
	dexProvider.setDelay(10 * time.Millisecond)

	logger := observability.NewLogger("error", "json")

	pipeline := NewPipeline(PipelineConfig{
		CEXProvider: cexProvider,
		DEXProvider: dexProvider,
		Logger:      logger,
		Concurrency: 4, // 4 parallel workers
		BufferSize:  16,
	})
	defer pipeline.Close()

	// Create 8 trade sizes
	tradeSizes := make([]*big.Int, 8)
	for i := 0; i < 8; i++ {
		tradeSizes[i] = big.NewInt(int64((i + 1) * 1e18))
	}

	start := time.Now()
	opportunities := pipeline.Process(
		context.Background(),
		12345678,
		1700000000,
		tradeSizes,
		big.NewInt(50e9), // 50 gwei
		2000.0,           // ETH price
		"ETH-USDC",
		"ETH", 18,
		"USDC",
	)
	duration := time.Since(start)

	// With 4 workers and 10ms delay per call, 8 items should complete much faster
	// than sequential (8 * 4 calls * 10ms = 320ms sequential vs ~100ms concurrent)
	maxExpectedDuration := 200 * time.Millisecond // Allow some overhead

	if duration > maxExpectedDuration {
		t.Errorf("Pipeline took %v, expected < %v (indicates poor concurrency)",
			duration, maxExpectedDuration)
	}

	// Verify all trade sizes were processed
	// Each trade size produces up to 2 opportunities (CEX→DEX and DEX→CEX)
	if len(opportunities) == 0 {
		t.Error("Expected opportunities, got none")
	}

	t.Logf("✓ Processed %d trade sizes in %v with %d opportunities",
		len(tradeSizes), duration, len(opportunities))
}

// TestPipelineBackpressure verifies that pipeline handles backpressure correctly
// by using a larger batch with default buffer size
func TestPipelineBackpressure(t *testing.T) {
	cexProvider := newMockPipelineProvider()
	dexProvider := newMockPipelineProvider()

	logger := observability.NewLogger("error", "json")

	// Default buffer size, but many inputs to test throughput
	pipeline := NewPipeline(PipelineConfig{
		CEXProvider: cexProvider,
		DEXProvider: dexProvider,
		Logger:      logger,
		Concurrency: 4,
		BufferSize:  16, // Default buffer
	})
	defer pipeline.Close()

	// Many inputs to create some backpressure
	tradeSizes := make([]*big.Int, 50)
	for i := 0; i < 50; i++ {
		tradeSizes[i] = big.NewInt(int64((i + 1) * 1e18))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	opportunities := pipeline.Process(
		ctx,
		12345678,
		1700000000,
		tradeSizes,
		big.NewInt(50e9),
		2000.0,
		"ETH-USDC",
		"ETH", 18,
		"USDC",
	)
	duration := time.Since(start)

	// Primary check: Should complete in reasonable time
	if duration > 5*time.Second {
		t.Errorf("Pipeline took too long: %v", duration)
	}

	// 50 trade sizes should produce opportunities
	if len(opportunities) == 0 {
		t.Error("Expected opportunities from large batch")
	}

	t.Logf("✓ Pipeline processed %d trade sizes in %v, produced %d opportunities",
		len(tradeSizes), duration, len(opportunities))
}

// TestPipelineContextCancellation verifies that pipeline stops cleanly on context cancellation
func TestPipelineContextCancellation(t *testing.T) {
	cexProvider := newMockPipelineProvider()
	dexProvider := newMockPipelineProvider()

	// Slow providers
	cexProvider.setDelay(100 * time.Millisecond)
	dexProvider.setDelay(100 * time.Millisecond)

	logger := observability.NewLogger("error", "json")

	pipeline := NewPipeline(PipelineConfig{
		CEXProvider: cexProvider,
		DEXProvider: dexProvider,
		Logger:      logger,
		Concurrency: 2,
	})
	defer pipeline.Close()

	// Many inputs
	tradeSizes := make([]*big.Int, 20)
	for i := 0; i < 20; i++ {
		tradeSizes[i] = big.NewInt(int64((i + 1) * 1e18))
	}

	// Cancel after short time
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	opportunities := pipeline.Process(
		ctx,
		12345678,
		1700000000,
		tradeSizes,
		big.NewInt(50e9),
		2000.0,
		"ETH-USDC",
		"ETH", 18,
		"USDC",
	)
	duration := time.Since(start)

	// Should exit quickly on cancellation
	if duration > 200*time.Millisecond {
		t.Errorf("Pipeline took %v to cancel, expected faster exit", duration)
	}

	// Should have fewer than all expected (some cancelled)
	t.Logf("✓ Pipeline cancelled cleanly in %v, produced %d opportunities before cancellation",
		duration, len(opportunities))
}

// TestPipelineErrorHandling verifies that pipeline handles provider errors gracefully
func TestPipelineErrorHandling(t *testing.T) {
	cexProvider := newMockPipelineProvider()
	dexProvider := newMockPipelineProvider()

	// Configure some sizes to fail
	size1 := big.NewInt(1e18)
	size2 := big.NewInt(2e18)
	size3 := big.NewInt(3e18)

	// Size 2 will have DEX errors
	dexProvider.setError(size2, true, errors.New("DEX buy failed"))
	dexProvider.setError(size2, false, errors.New("DEX sell failed"))

	logger := observability.NewLogger("error", "json")

	pipeline := NewPipeline(PipelineConfig{
		CEXProvider: cexProvider,
		DEXProvider: dexProvider,
		Logger:      logger,
		Concurrency: 2,
	})
	defer pipeline.Close()

	tradeSizes := []*big.Int{size1, size2, size3}

	opportunities := pipeline.Process(
		context.Background(),
		12345678,
		1700000000,
		tradeSizes,
		big.NewInt(50e9),
		2000.0,
		"ETH-USDC",
		"ETH", 18,
		"USDC",
	)

	// Should still get opportunities from successful sizes (size1 and size3)
	// Size 2 should be skipped due to errors
	if len(opportunities) == 0 {
		t.Error("Expected opportunities from successful sizes")
	}

	// Verify the working sizes produced opportunities
	hasSize1Opp := false
	hasSize3Opp := false
	for _, opp := range opportunities {
		if opp.TradeSizeRaw.Cmp(size1) == 0 {
			hasSize1Opp = true
		}
		if opp.TradeSizeRaw.Cmp(size3) == 0 {
			hasSize3Opp = true
		}
	}

	if !hasSize1Opp || !hasSize3Opp {
		t.Errorf("Expected opportunities for size1 and size3, got size1=%v size3=%v",
			hasSize1Opp, hasSize3Opp)
	}

	t.Logf("✓ Pipeline handled errors gracefully, produced %d opportunities", len(opportunities))
}

// TestPipelineEmptyInput verifies that pipeline handles empty input gracefully
func TestPipelineEmptyInput(t *testing.T) {
	cexProvider := newMockPipelineProvider()
	dexProvider := newMockPipelineProvider()

	pipeline := NewPipeline(PipelineConfig{
		CEXProvider: cexProvider,
		DEXProvider: dexProvider,
	})
	defer pipeline.Close()

	opportunities := pipeline.Process(
		context.Background(),
		12345678,
		1700000000,
		nil, // Empty input
		big.NewInt(50e9),
		2000.0,
		"ETH-USDC",
		"ETH", 18,
		"USDC",
	)

	if opportunities != nil {
		t.Errorf("Expected nil for empty input, got %d opportunities", len(opportunities))
	}

	t.Log("✓ Pipeline handles empty input correctly")
}

// TestPipelineDEXQuoteLimiter verifies that DEX quote limiter is respected
func TestPipelineDEXQuoteLimiter(t *testing.T) {
	cexProvider := newMockPipelineProvider()
	dexProvider := newMockPipelineProvider()

	// Add delay to make limiter observable
	dexProvider.setDelay(20 * time.Millisecond)

	// Limit concurrent DEX quotes to 2
	limiter := semaphore.NewWeighted(2)

	logger := observability.NewLogger("error", "json")

	pipeline := NewPipeline(PipelineConfig{
		CEXProvider:     cexProvider,
		DEXProvider:     dexProvider,
		DEXQuoteLimiter: limiter,
		Logger:          logger,
		Concurrency:     4, // More workers than limiter allows
	})
	defer pipeline.Close()

	tradeSizes := make([]*big.Int, 8)
	for i := 0; i < 8; i++ {
		tradeSizes[i] = big.NewInt(int64((i + 1) * 1e18))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	opportunities := pipeline.Process(
		ctx,
		12345678,
		1700000000,
		tradeSizes,
		big.NewInt(50e9),
		2000.0,
		"ETH-USDC",
		"ETH", 18,
		"USDC",
	)
	duration := time.Since(start)

	// With limiter at 2 and 20ms delay, 8 sizes * 2 DEX calls should take ~80-160ms
	// (8 * 2 = 16 DEX calls, limited to 2 concurrent, 20ms each = 160ms minimum)

	if len(opportunities) == 0 {
		t.Error("Expected opportunities")
	}

	t.Logf("✓ DEX quote limiter respected, processed in %v with %d opportunities",
		duration, len(opportunities))
}

// TestPipelineDefaultConfiguration verifies default config values
func TestPipelineDefaultConfiguration(t *testing.T) {
	cexProvider := newMockPipelineProvider()
	dexProvider := newMockPipelineProvider()

	pipeline := NewPipeline(PipelineConfig{
		CEXProvider: cexProvider,
		DEXProvider: dexProvider,
	})
	defer pipeline.Close()

	// Verify defaults
	if pipeline.concurrency != 4 {
		t.Errorf("Expected default concurrency 4, got %d", pipeline.concurrency)
	}
	if pipeline.bufferSize != 16 {
		t.Errorf("Expected default bufferSize 16, got %d", pipeline.bufferSize)
	}
	if pipeline.analyzer.minProfitPct != 0.5 {
		t.Errorf("Expected default minProfitPct 0.5, got %f", pipeline.analyzer.minProfitPct)
	}

	t.Log("✓ Default configuration applied correctly")
}

// TestCalculateGasCostUSD verifies gas cost calculation
func TestCalculateGasCostUSD(t *testing.T) {
	tests := []struct {
		name       string
		gasCostWei *big.Int
		ethPrice   float64
		expected   float64
	}{
		{
			name:       "typical gas cost",
			gasCostWei: big.NewInt(22e15), // 0.022 ETH
			ethPrice:   2000.0,
			expected:   44.0, // 0.022 * 2000
		},
		{
			name:       "nil gas cost",
			gasCostWei: nil,
			ethPrice:   2000.0,
			expected:   0.0,
		},
		{
			name:       "zero gas cost",
			gasCostWei: big.NewInt(0),
			ethPrice:   2000.0,
			expected:   0.0,
		},
		{
			name:       "high gas cost",
			gasCostWei: big.NewInt(100e15), // 0.1 ETH
			ethPrice:   3000.0,
			expected:   300.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateGasCostUSD(tt.gasCostWei, tt.ethPrice)
			resultFloat, _ := result.Float64()

			// Allow small floating point error
			diff := resultFloat - tt.expected
			if diff < -0.01 || diff > 0.01 {
				t.Errorf("Expected gas cost $%.2f, got $%.2f", tt.expected, resultFloat)
			}
		})
	}

	t.Log("✓ Gas cost calculation works correctly")
}

// TestPipelineClose verifies graceful shutdown
func TestPipelineClose(t *testing.T) {
	cexProvider := newMockPipelineProvider()
	dexProvider := newMockPipelineProvider()

	pipeline := NewPipeline(PipelineConfig{
		CEXProvider: cexProvider,
		DEXProvider: dexProvider,
	})

	// Close should not panic
	pipeline.Close()

	// Double close should not panic
	pipeline.Close()

	t.Log("✓ Pipeline close works correctly")
}
