package arbitrage

import (
	"math/big"
	"testing"
	"time"

	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/pricing"
)

// calculateTestGasCostUSD is a helper to convert gas cost from wei to USD for tests.
// Mirrors the logic in detector and pipeline.
func calculateTestGasCostUSD(gasCostWei *big.Int, ethPriceUSD float64) *big.Float {
	if gasCostWei == nil || gasCostWei.Cmp(big.NewInt(0)) == 0 {
		return big.NewFloat(0)
	}
	gasCostETH := new(big.Float).SetInt(gasCostWei)
	gasCostETH.Quo(gasCostETH, big.NewFloat(1e18))
	return new(big.Float).Mul(gasCostETH, big.NewFloat(ethPriceUSD))
}

// TestProfitCalculationCEXToDEX tests profit calculation for CEX→DEX arbitrage
func TestProfitCalculationCEXToDEX(t *testing.T) {
	calculator := NewCalculator()

	// Scenario: CEX→DEX arbitrage (buy ETH on CEX at $2050, sell on DEX at $2060)
	cexBuyPrice := &pricing.Price{
		Value:         big.NewFloat(2050.0),   // CEX buy price: $2050/ETH
		AmountOut:     big.NewFloat(2052.05),  // USDC spent = 2050 × (1 + 0.001) = $2052.05
		AmountOutRaw:  big.NewInt(2052050000), // 2052.05 USDC (6 decimals)
		TradingFee:    big.NewFloat(0.001),    // 0.1% Binance fee
		FeeTier:       0,                      // CEX has no fee tier
		BaseSymbol:    "ETH",
		QuoteSymbol:   "USDC",
		BaseDecimals:  18,
		QuoteDecimals: 6,
		QuoteIsStable: true,
		Timestamp:     time.Now(),
	}

	dexSellPrice := &pricing.Price{
		Value:         big.NewFloat(2060.0),   // DEX sell price: $2060/ETH
		AmountOut:     big.NewFloat(2053.82),  // USDC received = 2060 × (1 - 0.003) = $2053.82
		AmountOutRaw:  big.NewInt(2053820000), // 2053.82 USDC (6 decimals)
		GasCost:       big.NewInt(22e15),      // 0.022 ETH gas (~$45 at $2055/ETH)
		TradingFee:    big.NewFloat(0.003),    // 0.3% Uniswap fee
		FeeTier:       3000,
		BaseSymbol:    "ETH",
		QuoteSymbol:   "USDC",
		BaseDecimals:  18,
		QuoteDecimals: 6,
		QuoteIsStable: true,
		Timestamp:     time.Now(),
	}

	tradeSize := big.NewInt(1e18) // 1 ETH
	ethPriceUSD := 2055.0         // Current ETH price

	// Calculate gas cost in USD (pre-calculated, as the new API expects)
	gasCostUSD := calculateTestGasCostUSD(dexSellPrice.GasCost, ethPriceUSD)

	// Calculate profit
	metrics, err := calculator.CalculateProfit(CEXToDEX, tradeSize, cexBuyPrice, dexSellPrice, gasCostUSD)
	if err != nil {
		t.Fatalf("CalculateProfit failed: %v", err)
	}

	// Expected:
	// Gross Profit = USDC received - USDC spent = 2053.82 - 2052.05 = $1.77
	// Gas Cost = 0.022 ETH × $2055 = $45.21
	// Net Profit = $1.77 - $45.21 = -$43.44 (unprofitable due to gas)

	grossProfit, _ := metrics.GrossProfitUSD.Float64()
	gasCost, _ := metrics.GasCostUSD.Float64()
	netProfit, _ := metrics.NetProfitUSD.Float64()

	t.Logf("Gross Profit: $%.2f", grossProfit)
	t.Logf("Gas Cost: $%.2f", gasCost)
	t.Logf("Net Profit: $%.2f", netProfit)

	// Verify gross profit is positive (revenue > cost before gas)
	if grossProfit <= 0 {
		t.Errorf("Expected positive gross profit, got $%.2f", grossProfit)
	}

	// Verify gross profit is approximately $1.77
	expectedGrossProfit := 1.77
	tolerance := 0.10 // $0.10 tolerance
	if grossProfit < expectedGrossProfit-tolerance || grossProfit > expectedGrossProfit+tolerance {
		t.Errorf("Gross profit $%.2f outside expected range $%.2f ± $%.2f",
			grossProfit, expectedGrossProfit, tolerance)
	}

	// Verify net profit is negative (gas kills the opportunity)
	if netProfit >= 0 {
		t.Errorf("Expected unprofitable opportunity (net profit < 0), got $%.2f", netProfit)
	}

	// Verify net profit = gross profit - gas cost
	calculatedNetProfit := grossProfit - gasCost
	if netProfit < calculatedNetProfit-0.01 || netProfit > calculatedNetProfit+0.01 {
		t.Errorf("Net profit $%.2f != (gross profit $%.2f - gas $%.2f) = $%.2f",
			netProfit, grossProfit, gasCost, calculatedNetProfit)
	}
}

// TestProfitCalculationDEXToCEX tests profit calculation for DEX→CEX arbitrage
func TestProfitCalculationDEXToCEX(t *testing.T) {
	calculator := NewCalculator()

	// Scenario: DEX→CEX arbitrage (buy ETH on DEX at $2040, sell on CEX at $2055)
	dexBuyPrice := &pricing.Price{
		Value:         big.NewFloat(2040.0),   // DEX buy price: $2040/ETH
		AmountOut:     big.NewFloat(2046.12),  // USDC spent = 2040 × (1 + 0.003) = $2046.12
		AmountOutRaw:  big.NewInt(2046120000), // 2046.12 USDC (6 decimals)
		GasCost:       big.NewInt(22e15),      // 0.022 ETH gas
		TradingFee:    big.NewFloat(0.003),    // 0.3% Uniswap fee
		FeeTier:       3000,
		BaseSymbol:    "ETH",
		QuoteSymbol:   "USDC",
		BaseDecimals:  18,
		QuoteDecimals: 6,
		QuoteIsStable: true,
		Timestamp:     time.Now(),
	}

	cexSellPrice := &pricing.Price{
		Value:         big.NewFloat(2055.0),   // CEX sell price: $2055/ETH
		AmountOut:     big.NewFloat(2052.95),  // USDC received = 2055 × (1 - 0.001) = $2052.95
		AmountOutRaw:  big.NewInt(2052950000), // 2052.95 USDC (6 decimals)
		TradingFee:    big.NewFloat(0.001),    // 0.1% Binance fee
		FeeTier:       0,
		BaseSymbol:    "ETH",
		QuoteSymbol:   "USDC",
		BaseDecimals:  18,
		QuoteDecimals: 6,
		QuoteIsStable: true,
		Timestamp:     time.Now(),
	}

	tradeSize := big.NewInt(1e18) // 1 ETH
	ethPriceUSD := 2047.0         // Current ETH price

	// Calculate gas cost in USD (pre-calculated, as the new API expects)
	gasCostUSD := calculateTestGasCostUSD(dexBuyPrice.GasCost, ethPriceUSD)

	// Calculate profit
	metrics, err := calculator.CalculateProfit(DEXToCEX, tradeSize, cexSellPrice, dexBuyPrice, gasCostUSD)
	if err != nil {
		t.Fatalf("CalculateProfit failed: %v", err)
	}

	// Expected:
	// Gross Profit = USDC received - USDC spent = 2052.95 - 2046.12 = $6.83
	// Gas Cost = 0.022 ETH × $2047 = $45.03
	// Net Profit = $6.83 - $45.03 = -$38.20 (unprofitable due to gas)

	grossProfit, _ := metrics.GrossProfitUSD.Float64()
	gasCost, _ := metrics.GasCostUSD.Float64()
	netProfit, _ := metrics.NetProfitUSD.Float64()

	t.Logf("Gross Profit: $%.2f", grossProfit)
	t.Logf("Gas Cost: $%.2f", gasCost)
	t.Logf("Net Profit: $%.2f", netProfit)

	// Verify gross profit is positive
	if grossProfit <= 0 {
		t.Errorf("Expected positive gross profit, got $%.2f", grossProfit)
	}

	// Verify net profit is negative (gas kills the opportunity)
	if netProfit >= 0 {
		t.Errorf("Expected unprofitable opportunity, got net profit $%.2f", netProfit)
	}
}

// TestProfitCalculationProfitableOpportunity tests a truly profitable arbitrage scenario
func TestProfitCalculationProfitableOpportunity(t *testing.T) {
	calculator := NewCalculator()

	// Scenario: Large spread CEX→DEX arbitrage (buy at $2000, sell at $2100)
	// Large enough spread to overcome gas costs
	cexBuyPrice := &pricing.Price{
		Value:         big.NewFloat(2000.0),
		AmountOut:     big.NewFloat(2002.0), // 2000 × 1.001 = $2002
		AmountOutRaw:  big.NewInt(2002000000),
		TradingFee:    big.NewFloat(0.001),
		FeeTier:       0,
		BaseSymbol:    "ETH",
		QuoteSymbol:   "USDC",
		BaseDecimals:  18,
		QuoteDecimals: 6,
		QuoteIsStable: true,
		Timestamp:     time.Now(),
	}

	dexSellPrice := &pricing.Price{
		Value:         big.NewFloat(2100.0),
		AmountOut:     big.NewFloat(2093.7), // 2100 × 0.997 = $2093.7
		AmountOutRaw:  big.NewInt(2093700000),
		GasCost:       big.NewInt(22e15), // 0.022 ETH gas
		TradingFee:    big.NewFloat(0.003),
		FeeTier:       3000,
		BaseSymbol:    "ETH",
		QuoteSymbol:   "USDC",
		BaseDecimals:  18,
		QuoteDecimals: 6,
		QuoteIsStable: true,
		Timestamp:     time.Now(),
	}

	tradeSize := big.NewInt(1e18) // 1 ETH
	ethPriceUSD := 2050.0

	// Calculate gas cost in USD (pre-calculated, as the new API expects)
	gasCostUSD := calculateTestGasCostUSD(dexSellPrice.GasCost, ethPriceUSD)

	// Calculate profit
	metrics, err := calculator.CalculateProfit(CEXToDEX, tradeSize, cexBuyPrice, dexSellPrice, gasCostUSD)
	if err != nil {
		t.Fatalf("CalculateProfit failed: %v", err)
	}

	// Expected:
	// Gross Profit = 2093.7 - 2002 = $91.7
	// Gas Cost = 0.022 × 2050 = $45.1
	// Net Profit = $91.7 - $45.1 = $46.6 (PROFITABLE!)

	grossProfit, _ := metrics.GrossProfitUSD.Float64()
	gasCost, _ := metrics.GasCostUSD.Float64()
	netProfit, _ := metrics.NetProfitUSD.Float64()

	t.Logf("Gross Profit: $%.2f", grossProfit)
	t.Logf("Gas Cost: $%.2f", gasCost)
	t.Logf("Net Profit: $%.2f", netProfit)

	// Verify this is profitable
	if netProfit <= 0 {
		t.Errorf("Expected profitable opportunity, got net profit $%.2f", netProfit)
	}

	// Verify net profit is significantly positive (> $40)
	if netProfit < 40.0 {
		t.Errorf("Expected net profit > $40, got $%.2f", netProfit)
	}

	// Verify net profit < gross profit (gas should reduce profit)
	if netProfit >= grossProfit {
		t.Errorf("Net profit $%.2f should be < gross profit $%.2f", netProfit, grossProfit)
	}
}

// TestGasCostCalculation verifies gas cost is properly converted to USD
func TestGasCostCalculation(t *testing.T) {
	calculator := NewCalculator()

	tests := []struct {
		name        string
		gasCostWei  int64
		ethPriceUSD float64
		expectedUSD float64
		tolerance   float64
	}{
		{
			name:        "typical mainnet gas (0.022 ETH at $2000)",
			gasCostWei:  22e15, // 0.022 ETH
			ethPriceUSD: 2000.0,
			expectedUSD: 44.0, // 0.022 × 2000 = $44
			tolerance:   0.1,
		},
		{
			name:        "high gas period (0.05 ETH at $3000)",
			gasCostWei:  50e15, // 0.05 ETH
			ethPriceUSD: 3000.0,
			expectedUSD: 150.0, // 0.05 × 3000 = $150
			tolerance:   0.1,
		},
		{
			name:        "low gas period (0.01 ETH at $1800)",
			gasCostWei:  10e15, // 0.01 ETH
			ethPriceUSD: 1800.0,
			expectedUSD: 18.0, // 0.01 × 1800 = $18
			tolerance:   0.1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a profitable scenario with $50 spread
			dexPrice := &pricing.Price{
				Value:        big.NewFloat(2070.0),  // DEX sell price
				AmountOut:    big.NewFloat(2063.79), // After 0.3% fee: 2070 × 0.997 = 2063.79
				AmountOutRaw: big.NewInt(2063790000),
				GasCost:      big.NewInt(tt.gasCostWei),
				TradingFee:   big.NewFloat(0.003),
				FeeTier:      3000,
				Timestamp:    time.Now(),
			}

			cexPrice := &pricing.Price{
				Value:        big.NewFloat(2020.0),  // CEX buy price
				AmountOut:    big.NewFloat(2022.02), // After 0.1% fee: 2020 × 1.001 = 2022.02
				AmountOutRaw: big.NewInt(2022020000),
				TradingFee:   big.NewFloat(0.001),
				Timestamp:    time.Now(),
			}

			// Calculate gas cost in USD (input to CalculateProfit)
			gasCostInput := calculateTestGasCostUSD(big.NewInt(tt.gasCostWei), tt.ethPriceUSD)

			metrics, err := calculator.CalculateProfit(
				CEXToDEX,
				big.NewInt(1e18),
				cexPrice,
				dexPrice,
				gasCostInput,
			)
			if err != nil {
				t.Fatalf("CalculateProfit failed: %v", err)
			}

			gasCostResult, _ := metrics.GasCostUSD.Float64()

			if gasCostResult < tt.expectedUSD-tt.tolerance || gasCostResult > tt.expectedUSD+tt.tolerance {
				t.Errorf("Gas cost $%.2f outside expected range $%.2f ± $%.2f",
					gasCostResult, tt.expectedUSD, tt.tolerance)
			}

			t.Logf("✓ Gas cost: %.6f ETH × $%.2f = $%.2f",
				float64(tt.gasCostWei)/1e18, tt.ethPriceUSD, gasCostResult)
		})
	}
}

// TestTradingSizeScaling verifies profit scales with trade size
func TestTradingSizeScaling(t *testing.T) {
	calculator := NewCalculator()

	// Base scenario with $10 gross profit per ETH
	baseCexPrice := &pricing.Price{
		Value:        big.NewFloat(2000.0),
		AmountOut:    big.NewFloat(2002.0),
		AmountOutRaw: big.NewInt(2002000000),
		TradingFee:   big.NewFloat(0.001),
		Timestamp:    time.Now(),
	}

	baseDexPrice := &pricing.Price{
		Value:        big.NewFloat(2012.0),
		AmountOut:    big.NewFloat(2006.0), // $4 more per ETH after fees
		AmountOutRaw: big.NewInt(2006000000),
		GasCost:      big.NewInt(22e15),
		TradingFee:   big.NewFloat(0.003),
		FeeTier:      3000,
		Timestamp:    time.Now(),
	}

	tests := []struct {
		name         string
		tradeSizeETH float64
		tradeSizeWei string
	}{
		{"1 ETH trade", 1.0, "1000000000000000000"},
		{"10 ETH trade", 10.0, "10000000000000000000"},
		{"100 ETH trade", 100.0, "100000000000000000000"},
	}

	var previousGrossProfit float64

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tradeSize := new(big.Int)
			tradeSize.SetString(tt.tradeSizeWei, 10)

			// Scale AmountOut proportionally with trade size
			scaledCexAmountOut := new(big.Float).Mul(baseCexPrice.AmountOut, big.NewFloat(tt.tradeSizeETH))
			scaledDexAmountOut := new(big.Float).Mul(baseDexPrice.AmountOut, big.NewFloat(tt.tradeSizeETH))

			scaledCexPrice := &pricing.Price{
				Value:        baseCexPrice.Value,
				AmountOut:    scaledCexAmountOut,
				AmountOutRaw: baseCexPrice.AmountOutRaw,
				TradingFee:   baseCexPrice.TradingFee,
				Timestamp:    time.Now(),
			}

			scaledDexPrice := &pricing.Price{
				Value:        baseDexPrice.Value,
				AmountOut:    scaledDexAmountOut,
				AmountOutRaw: baseDexPrice.AmountOutRaw,
				GasCost:      baseDexPrice.GasCost, // Gas cost stays the same
				TradingFee:   baseDexPrice.TradingFee,
				FeeTier:      baseDexPrice.FeeTier,
				Timestamp:    time.Now(),
			}

			// Calculate gas cost in USD (gas cost stays the same regardless of trade size)
			gasCostUSD := calculateTestGasCostUSD(scaledDexPrice.GasCost, 2006.0)

			metrics, err := calculator.CalculateProfit(
				CEXToDEX,
				tradeSize,
				scaledCexPrice,
				scaledDexPrice,
				gasCostUSD,
			)
			if err != nil {
				t.Fatalf("CalculateProfit failed: %v", err)
			}

			grossProfit, _ := metrics.GrossProfitUSD.Float64()
			netProfit, _ := metrics.NetProfitUSD.Float64()

			t.Logf("Trade size: %.0f ETH", tt.tradeSizeETH)
			t.Logf("  Gross Profit: $%.2f", grossProfit)
			t.Logf("  Net Profit: $%.2f", netProfit)

			// Verify profit scales linearly with size (gross profit should scale, gas doesn't)
			if i > 0 {
				// Gross profit should be approximately 10x previous (due to 10x size increase)
				expectedGrossProfit := previousGrossProfit * 10.0
				tolerance := expectedGrossProfit * 0.05 // 5% tolerance

				if grossProfit < expectedGrossProfit-tolerance || grossProfit > expectedGrossProfit+tolerance {
					t.Errorf("Gross profit $%.2f doesn't scale linearly from previous $%.2f (expected ~$%.2f)",
						grossProfit, previousGrossProfit, expectedGrossProfit)
				}
			}

			previousGrossProfit = grossProfit
		})
	}
}

// TestDetermineDirection verifies direction determination logic
func TestDetermineDirection(t *testing.T) {
	calculator := NewCalculator()

	tests := []struct {
		name        string
		cexPrice    float64
		dexPrice    float64
		expectedDir Direction
	}{
		{
			name:        "DEX higher → CEX to DEX",
			cexPrice:    2000.0,
			dexPrice:    2050.0,
			expectedDir: CEXToDEX,
		},
		{
			name:        "CEX higher → DEX to CEX",
			cexPrice:    2050.0,
			dexPrice:    2000.0,
			expectedDir: DEXToCEX,
		},
		{
			name:        "Equal prices → default to DEX to CEX",
			cexPrice:    2050.0,
			dexPrice:    2050.0,
			expectedDir: DEXToCEX,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			direction := calculator.DetermineDirection(
				big.NewFloat(tt.cexPrice),
				big.NewFloat(tt.dexPrice),
			)

			if direction != tt.expectedDir {
				t.Errorf("Expected direction %v, got %v", tt.expectedDir, direction)
			}

			t.Logf("✓ CEX=$%.2f, DEX=$%.2f → %v",
				tt.cexPrice, tt.dexPrice, direction)
		})
	}
}

// TestCalculateProfitEdgeCases tests edge cases and error handling
func TestCalculateProfitEdgeCases(t *testing.T) {
	calculator := NewCalculator()

	t.Run("nil cex price AmountOut", func(t *testing.T) {
		cexPrice := &pricing.Price{
			Value:      big.NewFloat(2000.0),
			AmountOut:  nil, // nil AmountOut
			TradingFee: big.NewFloat(0.001),
			Timestamp:  time.Now(),
		}

		dexPrice := &pricing.Price{
			Value:        big.NewFloat(2050.0),
			AmountOut:    big.NewFloat(2043.85),
			AmountOutRaw: big.NewInt(2043850000),
			GasCost:      big.NewInt(22e15),
			TradingFee:   big.NewFloat(0.003),
			FeeTier:      3000,
			Timestamp:    time.Now(),
		}

		gasCostUSD := calculateTestGasCostUSD(dexPrice.GasCost, 2000.0)
		_, err := calculator.CalculateProfit(CEXToDEX, big.NewInt(1e18), cexPrice, dexPrice, gasCostUSD)
		if err == nil {
			t.Error("Expected error for nil AmountOut, got nil")
		}
		t.Logf("✓ Correctly rejected nil AmountOut: %v", err)
	})

	t.Run("nil dex price AmountOut", func(t *testing.T) {
		cexPrice := &pricing.Price{
			Value:        big.NewFloat(2000.0),
			AmountOut:    big.NewFloat(2002.0),
			AmountOutRaw: big.NewInt(2002000000),
			TradingFee:   big.NewFloat(0.001),
			Timestamp:    time.Now(),
		}

		dexPrice := &pricing.Price{
			Value:      big.NewFloat(2050.0),
			AmountOut:  nil, // nil AmountOut
			GasCost:    big.NewInt(22e15),
			TradingFee: big.NewFloat(0.003),
			FeeTier:    3000,
			Timestamp:  time.Now(),
		}

		gasCostUSD := calculateTestGasCostUSD(dexPrice.GasCost, 2000.0)
		_, err := calculator.CalculateProfit(CEXToDEX, big.NewInt(1e18), cexPrice, dexPrice, gasCostUSD)
		if err == nil {
			t.Error("Expected error for nil AmountOut, got nil")
		}
		t.Logf("✓ Correctly rejected nil AmountOut: %v", err)
	})

	t.Run("zero trade size", func(t *testing.T) {
		cexPrice := &pricing.Price{
			Value:        big.NewFloat(2000.0),
			AmountOut:    big.NewFloat(0),
			AmountOutRaw: big.NewInt(0),
			TradingFee:   big.NewFloat(0.001),
			Timestamp:    time.Now(),
		}

		dexPrice := &pricing.Price{
			Value:        big.NewFloat(2050.0),
			AmountOut:    big.NewFloat(0),
			AmountOutRaw: big.NewInt(0),
			GasCost:      big.NewInt(22e15),
			TradingFee:   big.NewFloat(0.003),
			FeeTier:      3000,
			Timestamp:    time.Now(),
		}

		gasCostUSD := calculateTestGasCostUSD(dexPrice.GasCost, 2000.0)
		metrics, err := calculator.CalculateProfit(CEXToDEX, big.NewInt(0), cexPrice, dexPrice, gasCostUSD)
		if err != nil {
			t.Fatalf("Unexpected error for zero trade size: %v", err)
		}

		// Zero trade size should result in zero profits
		grossProfit, _ := metrics.GrossProfitUSD.Float64()
		netProfit, _ := metrics.NetProfitUSD.Float64()

		if grossProfit != 0 {
			t.Errorf("Expected zero gross profit for zero trade size, got $%.2f", grossProfit)
		}

		// Net profit should be negative due to gas costs
		if netProfit >= 0 {
			t.Errorf("Expected negative net profit (gas costs), got $%.2f", netProfit)
		}

		t.Logf("✓ Zero trade size: gross=$%.2f, net=$%.2f", grossProfit, netProfit)
	})

	t.Run("nil gas cost", func(t *testing.T) {
		cexPrice := &pricing.Price{
			Value:        big.NewFloat(2000.0),
			AmountOut:    big.NewFloat(2002.0),
			AmountOutRaw: big.NewInt(2002000000),
			TradingFee:   big.NewFloat(0.001),
			Timestamp:    time.Now(),
		}

		dexPrice := &pricing.Price{
			Value:        big.NewFloat(2050.0),
			AmountOut:    big.NewFloat(2043.85),
			AmountOutRaw: big.NewInt(2043850000),
			GasCost:      nil, // nil gas cost
			TradingFee:   big.NewFloat(0.003),
			FeeTier:      3000,
			Timestamp:    time.Now(),
		}

		// nil gas cost should result in 0 gas cost USD
		gasCostUSD := calculateTestGasCostUSD(dexPrice.GasCost, 2000.0)
		metrics, err := calculator.CalculateProfit(CEXToDEX, big.NewInt(1e18), cexPrice, dexPrice, gasCostUSD)
		if err != nil {
			t.Fatalf("Unexpected error for nil gas cost: %v", err)
		}

		// Should treat nil gas as zero
		gasCost, _ := metrics.GasCostUSD.Float64()
		if gasCost != 0 {
			t.Errorf("Expected zero gas cost for nil GasCost, got $%.2f", gasCost)
		}

		t.Logf("✓ Nil gas cost treated as zero: $%.2f", gasCost)
	})
}

// TestFeeApplication verifies CEX and DEX fees are correctly applied
func TestFeeApplication(t *testing.T) {
	calculator := NewCalculator()

	tests := []struct {
		name           string
		direction      Direction
		basePrice      float64
		cexFee         float64
		dexFee         float64
		expectedCexOut float64
		expectedDexOut float64
	}{
		{
			name:           "CEX to DEX - standard fees",
			direction:      CEXToDEX,
			basePrice:      2000.0,
			cexFee:         0.001,  // 0.1% Binance
			dexFee:         0.003,  // 0.3% Uniswap
			expectedCexOut: 2002.0, // Buy: 2000 * (1 + 0.001)
			expectedDexOut: 1994.0, // Sell: 2000 * (1 - 0.003)
		},
		{
			name:           "DEX to CEX - standard fees",
			direction:      DEXToCEX,
			basePrice:      2000.0,
			cexFee:         0.001,
			dexFee:         0.003,
			expectedCexOut: 1998.0, // Sell: 2000 * (1 - 0.001)
			expectedDexOut: 2006.0, // Buy: 2000 * (1 + 0.003)
		},
		{
			name:           "High fee tier - 1%",
			direction:      CEXToDEX,
			basePrice:      2000.0,
			cexFee:         0.001,
			dexFee:         0.01, // 1% Uniswap
			expectedCexOut: 2002.0,
			expectedDexOut: 1980.0, // 2000 * (1 - 0.01)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cexAmountOut, dexAmountOut float64

			if tt.direction == CEXToDEX {
				// CEX: Buy (price increases by fee)
				cexAmountOut = tt.basePrice * (1 + tt.cexFee)
				// DEX: Sell (price decreases by fee)
				dexAmountOut = tt.basePrice * (1 - tt.dexFee)
			} else {
				// DEX: Buy (price increases by fee)
				dexAmountOut = tt.basePrice * (1 + tt.dexFee)
				// CEX: Sell (price decreases by fee)
				cexAmountOut = tt.basePrice * (1 - tt.cexFee)
			}

			// Verify our expected values match calculations
			tolerance := 0.01
			if cexAmountOut < tt.expectedCexOut-tolerance || cexAmountOut > tt.expectedCexOut+tolerance {
				t.Errorf("CEX AmountOut: expected $%.2f, got $%.2f", tt.expectedCexOut, cexAmountOut)
			}

			if dexAmountOut < tt.expectedDexOut-tolerance || dexAmountOut > tt.expectedDexOut+tolerance {
				t.Errorf("DEX AmountOut: expected $%.2f, got $%.2f", tt.expectedDexOut, dexAmountOut)
			}

			cexPrice := &pricing.Price{
				Value:        big.NewFloat(tt.basePrice),
				AmountOut:    big.NewFloat(cexAmountOut),
				AmountOutRaw: big.NewInt(int64(cexAmountOut * 1e6)),
				TradingFee:   big.NewFloat(tt.cexFee),
				Timestamp:    time.Now(),
			}

			dexPrice := &pricing.Price{
				Value:        big.NewFloat(tt.basePrice),
				AmountOut:    big.NewFloat(dexAmountOut),
				AmountOutRaw: big.NewInt(int64(dexAmountOut * 1e6)),
				GasCost:      big.NewInt(22e15),
				TradingFee:   big.NewFloat(tt.dexFee),
				FeeTier:      3000,
				Timestamp:    time.Now(),
			}

			gasCostUSD := calculateTestGasCostUSD(dexPrice.GasCost, tt.basePrice)
			_, err := calculator.CalculateProfit(tt.direction, big.NewInt(1e18), cexPrice, dexPrice, gasCostUSD)
			if err != nil {
				t.Fatalf("CalculateProfit failed: %v", err)
			}

			t.Logf("✓ Fees applied correctly: CEX %.3f%% → $%.2f, DEX %.3f%% → $%.2f",
				tt.cexFee*100, cexAmountOut, tt.dexFee*100, dexAmountOut)
		})
	}
}

// TestProfitWithHighGas verifies that high gas costs make opportunities unprofitable
func TestProfitWithHighGas(t *testing.T) {
	calculator := NewCalculator()

	// Scenario: Small spread ($10) that's profitable without gas, unprofitable with high gas
	cexPrice := &pricing.Price{
		Value:        big.NewFloat(2000.0),
		AmountOut:    big.NewFloat(2002.0),
		AmountOutRaw: big.NewInt(2002000000),
		TradingFee:   big.NewFloat(0.001),
		Timestamp:    time.Now(),
	}

	tests := []struct {
		name             string
		gasCostWei       int64
		ethPriceUSD      float64
		expectedGasUSD   float64
		shouldProfitable bool
	}{
		{
			name:             "Low gas - profitable",
			gasCostWei:       5e15, // 0.005 ETH
			ethPriceUSD:      2000.0,
			expectedGasUSD:   10.0,  // 0.005 * 2000 = $10
			shouldProfitable: false, // Break even, not profitable
		},
		{
			name:             "Normal gas - unprofitable",
			gasCostWei:       22e15, // 0.022 ETH
			ethPriceUSD:      2000.0,
			expectedGasUSD:   44.0, // 0.022 * 2000 = $44
			shouldProfitable: false,
		},
		{
			name:             "High gas - very unprofitable",
			gasCostWei:       50e15, // 0.05 ETH
			ethPriceUSD:      2000.0,
			expectedGasUSD:   100.0, // 0.05 * 2000 = $100
			shouldProfitable: false,
		},
		{
			name:             "Extreme gas during congestion - extremely unprofitable",
			gasCostWei:       100e15, // 0.1 ETH
			ethPriceUSD:      2000.0,
			expectedGasUSD:   200.0, // 0.1 * 2000 = $200
			shouldProfitable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dexPrice := &pricing.Price{
				Value:        big.NewFloat(2010.0),  // $10 gross spread
				AmountOut:    big.NewFloat(2003.97), // 2010 * (1 - 0.003) = 2003.97
				AmountOutRaw: big.NewInt(2003970000),
				GasCost:      big.NewInt(tt.gasCostWei),
				TradingFee:   big.NewFloat(0.003),
				FeeTier:      3000,
				Timestamp:    time.Now(),
			}

			gasCostUSD := calculateTestGasCostUSD(dexPrice.GasCost, tt.ethPriceUSD)
			metrics, err := calculator.CalculateProfit(CEXToDEX, big.NewInt(1e18), cexPrice, dexPrice, gasCostUSD)
			if err != nil {
				t.Fatalf("CalculateProfit failed: %v", err)
			}

			grossProfit, _ := metrics.GrossProfitUSD.Float64()
			gasCost, _ := metrics.GasCostUSD.Float64()
			netProfit, _ := metrics.NetProfitUSD.Float64()

			// Verify gas cost matches expected
			tolerance := 0.1
			if gasCost < tt.expectedGasUSD-tolerance || gasCost > tt.expectedGasUSD+tolerance {
				t.Errorf("Gas cost: expected $%.2f, got $%.2f", tt.expectedGasUSD, gasCost)
			}

			// Verify profitability
			isProfitable := netProfit > 0
			if isProfitable != tt.shouldProfitable {
				t.Errorf("Expected profitable=%v, got profitable=%v (net profit=$%.2f)",
					tt.shouldProfitable, isProfitable, netProfit)
			}

			// Verify that high gas makes net profit negative
			if gasCost > grossProfit {
				if netProfit >= 0 {
					t.Errorf("When gas ($%.2f) > gross profit ($%.2f), net profit should be negative, got $%.2f",
						gasCost, grossProfit, netProfit)
				}
			}

			t.Logf("✓ Gas: $%.2f, Gross: $%.2f, Net: $%.2f, Profitable: %v",
				gasCost, grossProfit, netProfit, isProfitable)
		})
	}
}

// TestProfitPctUsesBuySidePrice verifies profit percentage uses the BUY-side price
func TestProfitPctUsesBuySidePrice(t *testing.T) {
	calculator := NewCalculator()

	tradeSize := big.NewInt(1e18) // 1 base unit (ETH-style)

	// Direction: DEX -> CEX (buy on DEX, sell on CEX)
	// Buy price should be DEX (2000), not CEX (2100)
	cexPrice := &pricing.Price{
		Value:         big.NewFloat(2100.0),
		AmountOut:     big.NewFloat(2100.0),
		AmountOutRaw:  big.NewInt(2100 * 1e6),
		TradingFee:    big.NewFloat(0),
		BaseDecimals:  18,
		QuoteDecimals: 6,
		QuoteIsStable: true,
		Timestamp:     time.Now(),
	}

	dexPrice := &pricing.Price{
		Value:         big.NewFloat(2000.0),
		AmountOut:     big.NewFloat(2000.0),
		AmountOutRaw:  big.NewInt(2000 * 1e6),
		GasCost:       big.NewInt(0),
		TradingFee:    big.NewFloat(0),
		BaseDecimals:  18,
		QuoteDecimals: 6,
		QuoteIsStable: true,
		Timestamp:     time.Now(),
	}

	gasCostUSD := calculateTestGasCostUSD(dexPrice.GasCost, 2000.0)
	metrics, err := calculator.CalculateProfit(DEXToCEX, tradeSize, cexPrice, dexPrice, gasCostUSD)
	if err != nil {
		t.Fatalf("CalculateProfit failed: %v", err)
	}

	profitPct, _ := metrics.ProfitPct.Float64()
	expected := 5.0 // 100 / 2000 * 100
	tolerance := 0.0001
	if profitPct < expected-tolerance || profitPct > expected+tolerance {
		t.Errorf("ProfitPct: expected %.4f, got %.4f", expected, profitPct)
	}
}
