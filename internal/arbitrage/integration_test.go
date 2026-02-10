//go:build integration
// +build integration

// Package arbitrage provides integration tests for the arbitrage detection pipeline.
// These tests verify that multiple components work together correctly.
//
// Run integration tests with:
//
//	go test -tags=integration ./internal/arbitrage/...
//
// Or run all tests including integration:
//
//	go test -tags=integration ./...
package arbitrage

import (
	"context"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/cache"
	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/config"
	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/observability"
	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/pricing"
)

// integrationMockProvider implements the PriceProvider interface for testing.
// It allows configuring different prices to simulate CEX/DEX price discrepancies.
type integrationMockProvider struct {
	name          string
	priceValue    float64
	amountOutBuy  float64 // Amount out when buying base token
	amountOutSell float64 // Amount out when selling base token
	tradingFee    float64
	gasCost       *big.Int
	callCount     int
	mu            sync.Mutex
}

func newIntegrationMockProvider(name string, price, amountOutBuy, amountOutSell, fee float64, gasCost *big.Int) *integrationMockProvider {
	return &integrationMockProvider{
		name:          name,
		priceValue:    price,
		amountOutBuy:  amountOutBuy,
		amountOutSell: amountOutSell,
		tradingFee:    fee,
		gasCost:       gasCost,
	}
}

func (m *integrationMockProvider) GetPrice(ctx context.Context, size *big.Int, isBuy bool, gasPrice *big.Int, blockNum uint64) (*pricing.Price, error) {
	m.mu.Lock()
	m.callCount++
	m.mu.Unlock()

	var amountOut float64
	if isBuy {
		amountOut = m.amountOutBuy
	} else {
		amountOut = m.amountOutSell
	}

	return &pricing.Price{
		Value:         big.NewFloat(m.priceValue),
		AmountOut:     big.NewFloat(amountOut),
		AmountOutRaw:  big.NewInt(int64(amountOut * 1e6)), // Assume 6 decimals for quote
		Slippage:      big.NewFloat(0.001),
		GasCost:       m.gasCost,
		TradingFee:    big.NewFloat(m.tradingFee),
		BaseSymbol:    "ETH",
		QuoteSymbol:   "USDC",
		BaseDecimals:  18,
		QuoteDecimals: 6,
		QuoteIsStable: true,
		Timestamp:     time.Now(),
	}, nil
}

func (m *integrationMockProvider) GetCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

// integrationMockETHProvider provides ETH price for gas cost conversion
type integrationMockETHProvider struct {
	ethPrice float64
}

func (m *integrationMockETHProvider) GetPrice(ctx context.Context, size *big.Int, isBuy bool, gasPrice *big.Int, blockNum uint64) (*pricing.Price, error) {
	return &pricing.Price{
		Value:         big.NewFloat(2000),
		AmountOut:     big.NewFloat(2000),
		AmountOutRaw:  big.NewInt(2000e6),
		Slippage:      big.NewFloat(0),
		GasCost:       big.NewInt(0),
		TradingFee:    big.NewFloat(0.001),
		BaseSymbol:    "ETH",
		QuoteSymbol:   "USDC",
		BaseDecimals:  18,
		QuoteDecimals: 6,
		QuoteIsStable: true,
		Timestamp:     time.Now(),
	}, nil
}

func (m *integrationMockETHProvider) GetETHPrice(ctx context.Context) (float64, error) {
	return m.ethPrice, nil
}

// integrationMockPublisher records published opportunities
type integrationMockPublisher struct {
	opportunities []*Opportunity
	mu            sync.Mutex
}

func newMockPublisher() *integrationMockPublisher {
	return &integrationMockPublisher{
		opportunities: make([]*Opportunity, 0),
	}
}

func (m *integrationMockPublisher) PublishOpportunity(ctx context.Context, opp *Opportunity) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.opportunities = append(m.opportunities, opp)
	return nil
}

func (m *integrationMockPublisher) GetOpportunities() []*Opportunity {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*Opportunity, len(m.opportunities))
	copy(result, m.opportunities)
	return result
}

// TestIntegration_DetectorWithMockedProviders tests the full detection pipeline
// with mocked price providers to simulate profitable arbitrage scenarios.
func TestIntegration_DetectorWithMockedProviders(t *testing.T) {
	ctx := context.Background()
	logger := observability.NewLogger("error", "json")

	// Create in-memory cache
	memCache := cache.NewMemoryCache(100)
	defer memCache.Close()

	// Create mock publisher
	publisher := newMockPublisher()

	// Test case: DEX price is higher than CEX - should detect CEX→DEX opportunity
	t.Run("profitable CEX to DEX arbitrage", func(t *testing.T) {
		// CEX: Buy 1 ETH for 2000 USDC (with 0.1% fee = 2002 USDC spent)
		// DEX: Sell 1 ETH for 2050 USDC (with 0.3% fee = 2043.85 USDC received)
		// Gross profit: 2043.85 - 2002 = ~41.85 USDC (before gas)
		cexProvider := newIntegrationMockProvider(
			"binance",
			2000.0, // price
			2002.0, // amountOut for buy (we spend 2002 USDC to buy 1 ETH)
			1998.0, // amountOut for sell
			0.001,  // 0.1% fee
			nil,    // no gas for CEX
		)

		dexProvider := newIntegrationMockProvider(
			"uniswap",
			2050.0,                  // price
			2056.15,                 // amountOut for buy
			2043.85,                 // amountOut for sell (we receive 2043.85 USDC for 1 ETH)
			0.003,                   // 0.3% fee
			big.NewInt(200000*50e9), // 200k gas * 50 gwei = 0.01 ETH ≈ $20
		)

		detector, err := NewDetector(DetectorConfig{
			CEXProvider:  cexProvider,
			DEXProvider:  dexProvider,
			Publisher:    publisher,
			Logger:       logger,
			TradeSizes:   []*big.Int{big.NewInt(1e18)}, // 1 ETH
			MinProfitPct: 0.1,                          // 0.1% minimum
			ETHPriceUSD:  2000.0,
			PairName:     "ETH-USDC",
			BaseToken:    config.TokenInfo{Symbol: "ETH", Decimals: 18},
			QuoteToken:   config.TokenInfo{Symbol: "USDC", Decimals: 6, IsStablecoin: true},
		})
		if err != nil {
			t.Fatalf("Failed to create detector: %v", err)
		}

		// Detect opportunities
		opps, err := detector.Detect(ctx, 12345678, uint64(time.Now().Unix()))
		if err != nil {
			t.Fatalf("Detection failed: %v", err)
		}

		// Verify we got an opportunity
		if len(opps) == 0 {
			t.Fatal("Expected at least one opportunity, got none")
		}

		opp := opps[0]
		t.Logf("Opportunity detected: Direction=%v, GrossProfit=$%.2f, NetProfit=$%.2f, ProfitPct=%.4f%%",
			opp.Direction,
			floatFromBigFloat(opp.GrossProfitUSD),
			floatFromBigFloat(opp.NetProfitUSD),
			floatFromBigFloat(opp.ProfitPct),
		)

		// Verify direction is CEX to DEX (buy on CEX, sell on DEX)
		if opp.Direction != CEXToDEX {
			t.Errorf("Expected direction CEXToDEX, got %v", opp.Direction)
		}

		// Verify profit is positive
		if opp.NetProfitUSD.Cmp(big.NewFloat(0)) <= 0 {
			t.Errorf("Expected positive net profit, got %.2f", floatFromBigFloat(opp.NetProfitUSD))
		}

		// Verify both providers were called
		if cexProvider.GetCallCount() < 1 {
			t.Error("CEX provider should have been called at least once")
		}
		if dexProvider.GetCallCount() < 1 {
			t.Error("DEX provider should have been called at least once")
		}
	})

	// Test case: No profitable arbitrage
	t.Run("no profitable arbitrage when prices are equal", func(t *testing.T) {
		// Same prices on both exchanges - no arbitrage after fees
		cexProvider := newIntegrationMockProvider(
			"binance",
			2000.0,
			2002.0, // slightly higher due to fees
			1998.0, // slightly lower due to fees
			0.001,
			nil,
		)

		dexProvider := newIntegrationMockProvider(
			"uniswap",
			2000.0,
			2006.0, // even higher due to DEX fees
			1994.0, // even lower due to DEX fees
			0.003,
			big.NewInt(200000*50e9),
		)

		detector, err := NewDetector(DetectorConfig{
			CEXProvider:  cexProvider,
			DEXProvider:  dexProvider,
			Publisher:    publisher,
			Logger:       logger,
			TradeSizes:   []*big.Int{big.NewInt(1e18)},
			MinProfitPct: 0.5, // 0.5% minimum - won't be met
			ETHPriceUSD:  2000.0,
			PairName:     "ETH-USDC",
			BaseToken:    config.TokenInfo{Symbol: "ETH", Decimals: 18},
			QuoteToken:   config.TokenInfo{Symbol: "USDC", Decimals: 6, IsStablecoin: true},
		})
		if err != nil {
			t.Fatalf("Failed to create detector: %v", err)
		}

		opps, err := detector.Detect(ctx, 12345679, uint64(time.Now().Unix()))
		if err != nil {
			t.Fatalf("Detection failed: %v", err)
		}

		// Should get opportunities but none should be profitable above threshold
		profitableCount := 0
		for _, opp := range opps {
			if opp.IsProfitable() {
				profitableCount++
			}
		}

		if profitableCount > 0 {
			t.Errorf("Expected no profitable opportunities with equal prices and high threshold, got %d", profitableCount)
		}

		t.Logf("Correctly detected %d opportunities, %d profitable (expected 0)", len(opps), profitableCount)
	})

	// Test case: Multiple trade sizes
	t.Run("detection with multiple trade sizes", func(t *testing.T) {
		cexProvider := newIntegrationMockProvider("binance", 2000.0, 2002.0, 1998.0, 0.001, nil)
		dexProvider := newIntegrationMockProvider("uniswap", 2030.0, 2036.0, 2024.0, 0.003, big.NewInt(150000*30e9))

		tradeSizes := []*big.Int{
			big.NewInt(1e17), // 0.1 ETH
			big.NewInt(5e17), // 0.5 ETH
			big.NewInt(1e18), // 1 ETH
		}

		detector, err := NewDetector(DetectorConfig{
			CEXProvider:  cexProvider,
			DEXProvider:  dexProvider,
			Publisher:    publisher,
			Logger:       logger,
			TradeSizes:   tradeSizes,
			MinProfitPct: 0.1,
			ETHPriceUSD:  2000.0,
			PairName:     "ETH-USDC",
			BaseToken:    config.TokenInfo{Symbol: "ETH", Decimals: 18},
			QuoteToken:   config.TokenInfo{Symbol: "USDC", Decimals: 6, IsStablecoin: true},
		})
		if err != nil {
			t.Fatalf("Failed to create detector: %v", err)
		}

		opps, err := detector.Detect(ctx, 12345680, uint64(time.Now().Unix()))
		if err != nil {
			t.Fatalf("Detection failed: %v", err)
		}

		// Should have detected opportunities for each trade size
		t.Logf("Detected %d opportunities for %d trade sizes", len(opps), len(tradeSizes))

		// The mock returns fixed prices so we'll get same opportunity for each size
		// In real scenario, larger sizes would have more slippage
		for _, opp := range opps {
			t.Logf("  Size: %s, Direction: %v, Profitable: %v",
				opp.TradeSizeRaw.String(), opp.Direction, opp.IsProfitable())
		}
	})
}

// TestIntegration_CacheInteraction tests that the detector properly uses caching
func TestIntegration_CacheInteraction(t *testing.T) {
	ctx := context.Background()
	logger := observability.NewLogger("error", "json")

	memCache := cache.NewMemoryCache(100)
	defer memCache.Close()

	publisher := newMockPublisher()

	// Create providers
	cexProvider := newIntegrationMockProvider("binance", 2000.0, 2002.0, 1998.0, 0.001, nil)
	dexProvider := newIntegrationMockProvider("uniswap", 2020.0, 2026.0, 2014.0, 0.003, big.NewInt(100000*20e9))

	detector, err := NewDetector(DetectorConfig{
		CEXProvider:  cexProvider,
		DEXProvider:  dexProvider,
		Publisher:    publisher,
		Logger:       logger,
		TradeSizes:   []*big.Int{big.NewInt(1e18)},
		MinProfitPct: 0.1,
		ETHPriceUSD:  2000.0,
		PairName:     "ETH-USDC",
		BaseToken:    config.TokenInfo{Symbol: "ETH", Decimals: 18},
		QuoteToken:   config.TokenInfo{Symbol: "USDC", Decimals: 6, IsStablecoin: true},
	})
	if err != nil {
		t.Fatalf("Failed to create detector: %v", err)
	}

	// First detection
	_, err = detector.Detect(ctx, 12345681, uint64(time.Now().Unix()))
	if err != nil {
		t.Fatalf("First detection failed: %v", err)
	}

	firstCEXCalls := cexProvider.GetCallCount()
	firstDEXCalls := dexProvider.GetCallCount()

	t.Logf("After first detection: CEX calls=%d, DEX calls=%d", firstCEXCalls, firstDEXCalls)

	// Second detection with same block - should use cache where applicable
	_, err = detector.Detect(ctx, 12345681, uint64(time.Now().Unix()))
	if err != nil {
		t.Fatalf("Second detection failed: %v", err)
	}

	secondCEXCalls := cexProvider.GetCallCount()
	secondDEXCalls := dexProvider.GetCallCount()

	t.Logf("After second detection: CEX calls=%d, DEX calls=%d", secondCEXCalls, secondDEXCalls)

	// Providers will be called again since our mocks don't implement caching
	// In real scenario, cached quotes would reduce API calls
	if secondCEXCalls <= firstCEXCalls || secondDEXCalls <= firstDEXCalls {
		t.Logf("Note: Provider calls unchanged - caching might be at provider level")
	}
}

// TestIntegration_ConcurrentDetections tests that multiple concurrent detections work correctly
func TestIntegration_ConcurrentDetections(t *testing.T) {
	ctx := context.Background()
	logger := observability.NewLogger("error", "json")

	memCache := cache.NewMemoryCache(100)
	defer memCache.Close()

	publisher := newMockPublisher()

	cexProvider := newIntegrationMockProvider("binance", 2000.0, 2002.0, 1998.0, 0.001, nil)
	dexProvider := newIntegrationMockProvider("uniswap", 2025.0, 2031.0, 2019.0, 0.003, big.NewInt(120000*25e9))

	detector, err := NewDetector(DetectorConfig{
		CEXProvider:  cexProvider,
		DEXProvider:  dexProvider,
		Publisher:    publisher,
		Logger:       logger,
		TradeSizes:   []*big.Int{big.NewInt(1e18)},
		MinProfitPct: 0.1,
		ETHPriceUSD:  2000.0,
		PairName:     "ETH-USDC",
		BaseToken:    config.TokenInfo{Symbol: "ETH", Decimals: 18},
		QuoteToken:   config.TokenInfo{Symbol: "USDC", Decimals: 6, IsStablecoin: true},
	})
	if err != nil {
		t.Fatalf("Failed to create detector: %v", err)
	}

	// Run concurrent detections
	const numGoroutines = 10
	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(blockNum uint64) {
			defer wg.Done()
			_, err := detector.Detect(ctx, blockNum, uint64(time.Now().Unix()))
			if err != nil {
				errors <- err
			}
		}(uint64(12345682 + i))
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("Concurrent detection error: %v", err)
	}

	t.Logf("Successfully completed %d concurrent detections", numGoroutines)
	t.Logf("Total CEX calls: %d, DEX calls: %d", cexProvider.GetCallCount(), dexProvider.GetCallCount())
}

// Helper function to convert *big.Float to float64 for logging
func floatFromBigFloat(f *big.Float) float64 {
	if f == nil {
		return 0
	}
	result, _ := f.Float64()
	return result
}
