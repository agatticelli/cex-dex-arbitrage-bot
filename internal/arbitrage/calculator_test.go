package arbitrage

import (
	"math/big"
	"testing"
	"time"

	"github.com/gatti/cex-dex-arbitrage-bot/internal/pricing"
)

// TestProfitCalculationCEXToDEX tests profit calculation for CEX→DEX arbitrage
func TestProfitCalculationCEXToDEX(t *testing.T) {
	calculator := NewCalculator()

	// Scenario: CEX→DEX arbitrage (buy ETH on CEX at $2050, sell on DEX at $2060)
	cexBuyPrice := &pricing.Price{
		Value:        big.NewFloat(2050.0),           // CEX buy price: $2050/ETH
		AmountOut:    big.NewFloat(2052.05),          // USDC spent = 2050 × (1 + 0.001) = $2052.05
		AmountOutRaw: big.NewInt(2052050000),         // 2052.05 USDC (6 decimals)
		TradingFee:   big.NewFloat(0.001),            // 0.1% Binance fee
		FeeTier:      0,                              // CEX has no fee tier
		Timestamp:    time.Now(),
	}

	dexSellPrice := &pricing.Price{
		Value:        big.NewFloat(2060.0),           // DEX sell price: $2060/ETH
		AmountOut:    big.NewFloat(2053.82),          // USDC received = 2060 × (1 - 0.003) = $2053.82
		AmountOutRaw: big.NewInt(2053820000),         // 2053.82 USDC (6 decimals)
		GasCost:      big.NewInt(22e15),              // 0.022 ETH gas (~$45 at $2055/ETH)
		TradingFee:   big.NewFloat(0.003),            // 0.3% Uniswap fee
		FeeTier:      3000,
		Timestamp:    time.Now(),
	}

	tradeSize := big.NewInt(1e18)  // 1 ETH
	ethPriceUSD := 2055.0          // Current ETH price

	// Calculate profit
	metrics, err := calculator.CalculateProfit(CEXToDEX, tradeSize, cexBuyPrice, dexSellPrice, ethPriceUSD)
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
		Value:        big.NewFloat(2040.0),           // DEX buy price: $2040/ETH
		AmountOut:    big.NewFloat(2046.12),          // USDC spent = 2040 × (1 + 0.003) = $2046.12
		AmountOutRaw: big.NewInt(2046120000),         // 2046.12 USDC (6 decimals)
		GasCost:      big.NewInt(22e15),              // 0.022 ETH gas
		TradingFee:   big.NewFloat(0.003),            // 0.3% Uniswap fee
		FeeTier:      3000,
		Timestamp:    time.Now(),
	}

	cexSellPrice := &pricing.Price{
		Value:        big.NewFloat(2055.0),           // CEX sell price: $2055/ETH
		AmountOut:    big.NewFloat(2052.95),          // USDC received = 2055 × (1 - 0.001) = $2052.95
		AmountOutRaw: big.NewInt(2052950000),         // 2052.95 USDC (6 decimals)
		TradingFee:   big.NewFloat(0.001),            // 0.1% Binance fee
		FeeTier:      0,
		Timestamp:    time.Now(),
	}

	tradeSize := big.NewInt(1e18)  // 1 ETH
	ethPriceUSD := 2047.0          // Current ETH price

	// Calculate profit
	metrics, err := calculator.CalculateProfit(DEXToCEX, tradeSize, cexSellPrice, dexBuyPrice, ethPriceUSD)
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
		Value:        big.NewFloat(2000.0),
		AmountOut:    big.NewFloat(2002.0),           // 2000 × 1.001 = $2002
		AmountOutRaw: big.NewInt(2002000000),
		TradingFee:   big.NewFloat(0.001),
		FeeTier:      0,
		Timestamp:    time.Now(),
	}

	dexSellPrice := &pricing.Price{
		Value:        big.NewFloat(2100.0),
		AmountOut:    big.NewFloat(2093.7),           // 2100 × 0.997 = $2093.7
		AmountOutRaw: big.NewInt(2093700000),
		GasCost:      big.NewInt(22e15),              // 0.022 ETH gas
		TradingFee:   big.NewFloat(0.003),
		FeeTier:      3000,
		Timestamp:    time.Now(),
	}

	tradeSize := big.NewInt(1e18)  // 1 ETH
	ethPriceUSD := 2050.0

	// Calculate profit
	metrics, err := calculator.CalculateProfit(CEXToDEX, tradeSize, cexBuyPrice, dexSellPrice, ethPriceUSD)
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
		name         string
		gasCostWei   int64
		ethPriceUSD  float64
		expectedUSD  float64
		tolerance    float64
	}{
		{
			name:        "typical mainnet gas (0.022 ETH at $2000)",
			gasCostWei:  22e15,  // 0.022 ETH
			ethPriceUSD: 2000.0,
			expectedUSD: 44.0,   // 0.022 × 2000 = $44
			tolerance:   0.1,
		},
		{
			name:        "high gas period (0.05 ETH at $3000)",
			gasCostWei:  50e15,  // 0.05 ETH
			ethPriceUSD: 3000.0,
			expectedUSD: 150.0,  // 0.05 × 3000 = $150
			tolerance:   0.1,
		},
		{
			name:        "low gas period (0.01 ETH at $1800)",
			gasCostWei:  10e15,  // 0.01 ETH
			ethPriceUSD: 1800.0,
			expectedUSD: 18.0,   // 0.01 × 1800 = $18
			tolerance:   0.1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a profitable scenario with $50 spread
			dexPrice := &pricing.Price{
				Value:        big.NewFloat(2070.0),           // DEX sell price
				AmountOut:    big.NewFloat(2063.79),          // After 0.3% fee: 2070 × 0.997 = 2063.79
				AmountOutRaw: big.NewInt(2063790000),
				GasCost:      big.NewInt(tt.gasCostWei),
				TradingFee:   big.NewFloat(0.003),
				FeeTier:      3000,
				Timestamp:    time.Now(),
			}

			cexPrice := &pricing.Price{
				Value:        big.NewFloat(2020.0),           // CEX buy price
				AmountOut:    big.NewFloat(2022.02),          // After 0.1% fee: 2020 × 1.001 = 2022.02
				AmountOutRaw: big.NewInt(2022020000),
				TradingFee:   big.NewFloat(0.001),
				Timestamp:    time.Now(),
			}

			metrics, err := calculator.CalculateProfit(
				CEXToDEX,
				big.NewInt(1e18),
				cexPrice,
				dexPrice,
				tt.ethPriceUSD,
			)
			if err != nil {
				t.Fatalf("CalculateProfit failed: %v", err)
			}

			gasCostUSD, _ := metrics.GasCostUSD.Float64()

			if gasCostUSD < tt.expectedUSD-tt.tolerance || gasCostUSD > tt.expectedUSD+tt.tolerance {
				t.Errorf("Gas cost $%.2f outside expected range $%.2f ± $%.2f",
					gasCostUSD, tt.expectedUSD, tt.tolerance)
			}

			t.Logf("✓ Gas cost: %.6f ETH × $%.2f = $%.2f",
				float64(tt.gasCostWei)/1e18, tt.ethPriceUSD, gasCostUSD)
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

			metrics, err := calculator.CalculateProfit(
				CEXToDEX,
				tradeSize,
				scaledCexPrice,
				scaledDexPrice,
				2006.0,
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
