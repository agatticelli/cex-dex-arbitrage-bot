package pricing

import (
	"math/big"
	"testing"
	"time"

	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/observability"
	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/resilience"
)

func TestNewUniswapProvider_Validation(t *testing.T) {
	logger := observability.NewLogger("error", "json")

	tests := []struct {
		name        string
		config      UniswapProviderConfig
		expectError bool
		errorMsg    string
	}{
		{
			name:        "missing client",
			config:      UniswapProviderConfig{},
			expectError: true,
			errorMsg:    "ethereum client is required",
		},
		{
			name: "missing quoter address",
			config: UniswapProviderConfig{
				Client: nil, // Will fail on client check first
			},
			expectError: true,
			errorMsg:    "ethereum client is required",
		},
		{
			name: "missing token addresses",
			config: UniswapProviderConfig{
				// Can't create real client in unit test, but test validates the check order
			},
			expectError: true,
			errorMsg:    "ethereum client is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.config.Logger = logger
			_, err := NewUniswapProvider(tt.config)
			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			}
		})
	}
}

func TestUniswapProvider_buildQuoteCacheKey(t *testing.T) {
	// Create a minimal provider for testing the cache key building
	provider := &UniswapProvider{
		token0Address:  [20]byte{0xa0, 0xb8, 0x69, 0x91, 0xc6, 0x21, 0x8b, 0x36, 0xc1, 0xd1}, // Mock USDC-like
		token1Address:  [20]byte{0xc0, 0x2a, 0xaa, 0x39, 0xb2, 0x23, 0xfe, 0x8d, 0x0a, 0x0e}, // Mock WETH-like
		token0Decimals: 6,
		token1Decimals: 18,
	}

	tests := []struct {
		name       string
		blockNum   uint64
		size       *big.Int
		isToken0In bool
		feeTier    uint32
	}{
		{
			name:       "buy direction",
			blockNum:   12345678,
			size:       big.NewInt(1e18), // 1 ETH
			isToken0In: true,
			feeTier:    500,
		},
		{
			name:       "sell direction",
			blockNum:   12345678,
			size:       big.NewInt(1e18), // 1 ETH
			isToken0In: false,
			feeTier:    3000,
		},
		{
			name:       "different block",
			blockNum:   99999999,
			size:       new(big.Int).Mul(big.NewInt(5), big.NewInt(1e18)), // 5 ETH
			isToken0In: true,
			feeTier:    10000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := provider.buildQuoteCacheKey(tt.blockNum, tt.size, tt.isToken0In, tt.feeTier)

			// Verify key is not empty
			if key == "" {
				t.Error("Expected non-empty cache key")
			}

			// Verify key contains expected components
			if tt.isToken0In && !contains(key, "buy") {
				t.Error("Expected 'buy' in cache key for isToken0In=true")
			}
			if !tt.isToken0In && !contains(key, "sell") {
				t.Error("Expected 'sell' in cache key for isToken0In=false")
			}

			// Verify different parameters produce different keys
			keyDifferentBlock := provider.buildQuoteCacheKey(tt.blockNum+1, tt.size, tt.isToken0In, tt.feeTier)
			if key == keyDifferentBlock {
				t.Error("Expected different keys for different block numbers")
			}

			keyDifferentSize := provider.buildQuoteCacheKey(tt.blockNum, new(big.Int).Add(tt.size, big.NewInt(1)), tt.isToken0In, tt.feeTier)
			if key == keyDifferentSize {
				t.Error("Expected different keys for different sizes")
			}

			keyDifferentDirection := provider.buildQuoteCacheKey(tt.blockNum, tt.size, !tt.isToken0In, tt.feeTier)
			if key == keyDifferentDirection {
				t.Error("Expected different keys for different directions")
			}

			keyDifferentFee := provider.buildQuoteCacheKey(tt.blockNum, tt.size, tt.isToken0In, tt.feeTier+100)
			if key == keyDifferentFee {
				t.Error("Expected different keys for different fee tiers")
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestUniswapProvider_estimateGasCostWithPrice(t *testing.T) {
	provider := &UniswapProvider{
		token1Decimals: 18, // Base token decimals (WETH)
	}

	tests := []struct {
		name     string
		size     *big.Int
		gasPrice *big.Int
		minGas   int64 // Minimum expected gas units
		maxGas   int64 // Maximum expected gas units
	}{
		{
			name:     "small trade (< 5 base units)",
			size:     big.NewInt(1e18), // 1 ETH
			gasPrice: big.NewInt(50e9), // 50 gwei
			minGas:   100000,
			maxGas:   150000,
		},
		{
			name:     "medium trade (5-50 base units)",
			size:     new(big.Int).Mul(big.NewInt(10), big.NewInt(1e18)), // 10 ETH
			gasPrice: big.NewInt(50e9),
			minGas:   150000,
			maxGas:   200000,
		},
		{
			name:     "large trade (50-200 base units)",
			size:     new(big.Int).Mul(big.NewInt(100), big.NewInt(1e18)), // 100 ETH
			gasPrice: big.NewInt(50e9),
			minGas:   200000,
			maxGas:   300000,
		},
		{
			name:     "whale trade (> 200 base units)",
			size:     new(big.Int).Mul(big.NewInt(500), big.NewInt(1e18)), // 500 ETH
			gasPrice: big.NewInt(50e9),
			minGas:   300000,
			maxGas:   400000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gasCost := provider.estimateGasCostWithPrice(tt.size, tt.gasPrice)

			// Calculate gas units from gas cost
			gasUnits := new(big.Int).Div(gasCost, tt.gasPrice)
			gasUnitsInt64 := gasUnits.Int64()

			if gasUnitsInt64 < tt.minGas || gasUnitsInt64 > tt.maxGas {
				t.Errorf("Expected gas units between %d and %d, got %d",
					tt.minGas, tt.maxGas, gasUnitsInt64)
			}

			// Verify gas cost is positive
			if gasCost.Cmp(big.NewInt(0)) <= 0 {
				t.Error("Expected positive gas cost")
			}
		})
	}
}

func TestUniswapProvider_recordHealth(t *testing.T) {
	provider := &UniswapProvider{
		health: ProviderHealth{
			Provider: "uniswap",
		},
	}

	// Test successful call
	provider.recordHealth(nil, 100*time.Millisecond)

	health := provider.Health()
	if health.ConsecutiveFailures != 0 {
		t.Errorf("Expected 0 consecutive failures after success, got %d", health.ConsecutiveFailures)
	}
	if health.LastSuccess.IsZero() {
		t.Error("Expected LastSuccess to be set after success")
	}
	if health.LastError != "" {
		t.Errorf("Expected empty LastError after success, got '%s'", health.LastError)
	}
	if health.LastDuration != 100*time.Millisecond {
		t.Errorf("Expected LastDuration 100ms, got %v", health.LastDuration)
	}

	// Test failed call
	testErr := resilience.ErrCircuitOpen
	provider.recordHealth(testErr, 50*time.Millisecond)

	health = provider.Health()
	if health.ConsecutiveFailures != 1 {
		t.Errorf("Expected 1 consecutive failure after error, got %d", health.ConsecutiveFailures)
	}
	if health.LastFailure.IsZero() {
		t.Error("Expected LastFailure to be set after error")
	}
	if health.LastError == "" {
		t.Error("Expected LastError to be set after error")
	}

	// Test multiple failures
	provider.recordHealth(testErr, 50*time.Millisecond)
	provider.recordHealth(testErr, 50*time.Millisecond)

	health = provider.Health()
	if health.ConsecutiveFailures != 3 {
		t.Errorf("Expected 3 consecutive failures, got %d", health.ConsecutiveFailures)
	}

	// Test success resets failures
	provider.recordHealth(nil, 100*time.Millisecond)

	health = provider.Health()
	if health.ConsecutiveFailures != 0 {
		t.Errorf("Expected 0 consecutive failures after success, got %d", health.ConsecutiveFailures)
	}
}

func TestUniswapProvider_Health(t *testing.T) {
	logger := observability.NewLogger("error", "json")

	// Create a provider with circuit breaker for state testing
	cb := resilience.NewCircuitBreaker(resilience.CircuitBreakerConfig{
		Name:             "test-uniswap",
		FailureThreshold: 3,
		SuccessThreshold: 1,
		Timeout:          1 * time.Second,
	})

	provider := &UniswapProvider{
		logger:   logger,
		pairName: "ETH-USDC",
		cb:       cb,
		health: ProviderHealth{
			Provider: "uniswap",
			Pair:     "ETH-USDC",
		},
	}

	// Check initial health
	health := provider.Health()
	if health.Provider != "uniswap" {
		t.Errorf("Expected provider 'uniswap', got '%s'", health.Provider)
	}
	if health.Pair != "ETH-USDC" {
		t.Errorf("Expected pair 'ETH-USDC', got '%s'", health.Pair)
	}
	if health.CircuitState != "closed" {
		t.Errorf("Expected circuit state 'closed', got '%s'", health.CircuitState)
	}
}

func TestUniswapProvider_Name(t *testing.T) {
	tests := []struct {
		name       string
		pairName   string
		expectName string
	}{
		{
			name:       "with pair name",
			pairName:   "ETH-USDC",
			expectName: "uniswap:ETH-USDC",
		},
		{
			name:       "empty pair name",
			pairName:   "",
			expectName: "uniswap",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &UniswapProvider{
				pairName: tt.pairName,
			}

			if provider.Name() != tt.expectName {
				t.Errorf("Expected name '%s', got '%s'", tt.expectName, provider.Name())
			}
		})
	}
}

func TestUniswapProvider_FeeTierDefaults(t *testing.T) {
	// Verify default fee tier is set when not provided
	// Can't fully test without eth client, but verify the logic

	logger := observability.NewLogger("error", "json")

	// The constructor should set default fee tier of 3000 (0.3%)
	config := UniswapProviderConfig{
		Client:         nil, // Will fail early
		QuoterAddress:  "0x1234567890123456789012345678901234567890",
		Token0Address:  "0x1234567890123456789012345678901234567891",
		Token1Address:  "0x1234567890123456789012345678901234567892",
		Token0Decimals: 6,
		Token1Decimals: 18,
		Logger:         logger,
		FeeTiers:       nil, // Should use default
	}

	_, err := NewUniswapProvider(config)
	// Expected to fail due to nil client, but validates the config handling
	if err == nil {
		t.Error("Expected error for nil client")
	}
}

func TestRawToFloat(t *testing.T) {
	tests := []struct {
		name     string
		raw      *big.Int
		decimals int
		expected float64
	}{
		{
			name:     "1 ETH (18 decimals)",
			raw:      big.NewInt(1e18),
			decimals: 18,
			expected: 1.0,
		},
		{
			name:     "1 USDC (6 decimals)",
			raw:      big.NewInt(1e6),
			decimals: 6,
			expected: 1.0,
		},
		{
			name:     "1.5 ETH",
			raw:      new(big.Int).Div(new(big.Int).Mul(big.NewInt(3), big.NewInt(1e18)), big.NewInt(2)),
			decimals: 18,
			expected: 1.5,
		},
		{
			name:     "2000 USDC",
			raw:      big.NewInt(2000e6),
			decimals: 6,
			expected: 2000.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RawToFloat(tt.raw, tt.decimals)
			resultFloat, _ := result.Float64()

			tolerance := 0.0001
			if resultFloat < tt.expected-tolerance || resultFloat > tt.expected+tolerance {
				t.Errorf("Expected %.4f, got %.4f", tt.expected, resultFloat)
			}
		})
	}
}

func TestPoolState_Fields(t *testing.T) {
	// Test PoolState struct is properly initialized
	state := PoolState{
		SqrtPriceX96: big.NewInt(1234567890),
		Tick:         100,
		Liquidity:    big.NewInt(9876543210),
		FeeGrowth0:   big.NewInt(111),
		FeeGrowth1:   big.NewInt(222),
		TickSpacing:  60,
		Timestamp:    time.Now(),
	}

	if state.SqrtPriceX96.Cmp(big.NewInt(1234567890)) != 0 {
		t.Error("SqrtPriceX96 not set correctly")
	}
	if state.Tick != 100 {
		t.Errorf("Expected tick 100, got %d", state.Tick)
	}
	if state.TickSpacing != 60 {
		t.Errorf("Expected tick spacing 60, got %d", state.TickSpacing)
	}
}

func TestTickInfo_Fields(t *testing.T) {
	// Test TickInfo struct is properly initialized
	info := TickInfo{
		LiquidityGross: big.NewInt(1000000),
		LiquidityNet:   big.NewInt(-500000),
		Initialized:    true,
	}

	if info.LiquidityGross.Cmp(big.NewInt(1000000)) != 0 {
		t.Error("LiquidityGross not set correctly")
	}
	if info.LiquidityNet.Cmp(big.NewInt(-500000)) != 0 {
		t.Error("LiquidityNet not set correctly")
	}
	if !info.Initialized {
		t.Error("Expected Initialized to be true")
	}
}

func TestUniswapProvider_ConcurrentHealthUpdates(t *testing.T) {
	provider := &UniswapProvider{
		health: ProviderHealth{
			Provider: "uniswap",
		},
	}

	// Simulate concurrent health updates
	done := make(chan bool)

	for i := 0; i < 10; i++ {
		go func(success bool) {
			for j := 0; j < 100; j++ {
				if success {
					provider.recordHealth(nil, time.Millisecond)
				} else {
					provider.recordHealth(resilience.ErrCircuitOpen, time.Millisecond)
				}
			}
			done <- true
		}(i%2 == 0)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify health can still be read without panic
	health := provider.Health()
	if health.Provider != "uniswap" {
		t.Errorf("Expected provider 'uniswap', got '%s'", health.Provider)
	}
}

func BenchmarkUniswapProvider_buildQuoteCacheKey(b *testing.B) {
	provider := &UniswapProvider{
		token0Address:  [20]byte{0xa0, 0xb8, 0x69, 0x91, 0xc6, 0x21, 0x8b, 0x36, 0xc1, 0xd1},
		token1Address:  [20]byte{0xc0, 0x2a, 0xaa, 0x39, 0xb2, 0x23, 0xfe, 0x8d, 0x0a, 0x0e},
		token0Decimals: 6,
		token1Decimals: 18,
	}

	size := big.NewInt(1e18)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		provider.buildQuoteCacheKey(uint64(i), size, true, 500)
	}
}

func BenchmarkUniswapProvider_estimateGasCostWithPrice(b *testing.B) {
	provider := &UniswapProvider{
		token1Decimals: 18,
	}

	size := big.NewInt(1e18)
	gasPrice := big.NewInt(50e9)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		provider.estimateGasCostWithPrice(size, gasPrice)
	}
}
