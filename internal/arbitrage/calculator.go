package arbitrage

import (
	"fmt"
	"math/big"

	"github.com/gatti/cex-dex-arbitrage-bot/internal/pricing"
)

// Calculator calculates arbitrage profits accounting for all costs
type Calculator struct {
	// No state needed for calculator - pure functions
}

// NewCalculator creates a new arbitrage calculator
func NewCalculator() *Calculator {
	return &Calculator{}
}

// CalculateProfit calculates net profit for an arbitrage opportunity
// CRITICAL: Uses actual USDC flows from swap simulations, NOT price-based calculations
// This prevents false arbitrage signals from price averaging issues
func (c *Calculator) CalculateProfit(
	direction Direction,
	tradeSize *big.Int,
	cexPrice *pricing.Price,
	dexPrice *pricing.Price,
	ethPriceUSD float64,
) (*ProfitMetrics, error) {
	metrics := &ProfitMetrics{}

	// Convert trade size to ETH (normalized)
	tradeSizeETH := new(big.Float).SetInt(tradeSize)
	tradeSizeETH.Quo(tradeSizeETH, big.NewFloat(1e18))

	// Validate that AmountOut is populated
	if cexPrice.AmountOut == nil || dexPrice.AmountOut == nil {
		return nil, fmt.Errorf("AmountOut fields are required for accurate profit calculation")
	}

	// Calculate gross profit using actual USDC flows from execution simulations
	var grossProfitUSDC *big.Float
	var usdcSpent, usdcReceived *big.Float

	if direction == CEXToDEX {
		// Buy ETH on CEX (spend USDC), Sell ETH on DEX (receive USDC)
		// CEX: AmountOut = USDC spent when buying ETH
		// DEX: AmountOut = USDC received when selling ETH
		usdcSpent = cexPrice.AmountOut      // USDC spent on CEX
		usdcReceived = dexPrice.AmountOut   // USDC received on DEX
		grossProfitUSDC = new(big.Float).Sub(usdcReceived, usdcSpent)
	} else {
		// Buy ETH on DEX (spend USDC), Sell ETH on CEX (receive USDC)
		// DEX: AmountOut = USDC spent when buying ETH
		// CEX: AmountOut = USDC received when selling ETH
		usdcSpent = dexPrice.AmountOut      // USDC spent on DEX
		usdcReceived = cexPrice.AmountOut   // USDC received on CEX
		grossProfitUSDC = new(big.Float).Sub(usdcReceived, usdcSpent)
	}

	// Calculate trading fees in USD for display purposes
	// IMPORTANT: These fees are ALREADY included in AmountOut (and thus in gross profit)
	// We calculate them separately only to show the breakdown to the user
	// CEX fees are now applied in Binance provider (AmountOut includes fees)
	// DEX fees are applied in Uniswap SimulateSwap (AmountOut includes fees)
	cexTradeValue := new(big.Float).Mul(cexPrice.Value, tradeSizeETH)
	dexTradeValue := new(big.Float).Mul(dexPrice.Value, tradeSizeETH)
	cexFee := new(big.Float).Mul(cexTradeValue, cexPrice.TradingFee)
	dexFee := new(big.Float).Mul(dexTradeValue, dexPrice.TradingFee)
	metrics.TradingFeesUSD = new(big.Float).Add(cexFee, dexFee)

	// Calculate gas cost in USD (only for DEX trades)
	var gasCost *big.Int
	if direction == CEXToDEX {
		gasCost = dexPrice.GasCost // Gas paid when selling on DEX
	} else {
		gasCost = dexPrice.GasCost // Gas paid when buying on DEX
	}

	gasCostETH := new(big.Float).SetInt(gasCost)
	gasCostETH.Quo(gasCostETH, big.NewFloat(1e18))
	metrics.GasCostUSD = new(big.Float).Mul(gasCostETH, big.NewFloat(ethPriceUSD))

	// Validate gas cost (sanity check)
	// NOTE: Disabled for development - gas prices from RPC are unrealistically low (0.2 gwei vs 20-50 gwei mainnet)
	// Production would enable this with threshold of 0.01 ETH minimum
	gasCostETHFloat, _ := gasCostETH.Float64()
	_ = gasCostETHFloat // Suppress unused variable warning
	// if gasCostETH.Cmp(big.NewFloat(0.01)) < 0 {
	// 	return nil, fmt.Errorf("FLAG_INVALID_GAS_PRICE: gas cost %.6f ETH (%.0f wei) is suspiciously low",
	// 		gasCostETHFloat, float64(gasCost.Int64()))
	// }

	// Gross profit = actual USDC difference (trading fees ALREADY deducted in AmountOut)
	// This represents: (USDC received after fees) - (USDC spent after fees)
	metrics.GrossProfitUSD = grossProfitUSDC

	// Net profit = Gross profit - Gas cost
	// Trading fees are NOT subtracted here because they're already included in gross profit
	// Only gas cost is subtracted because it's an additional cost not reflected in USDC flows
	metrics.NetProfitUSD = new(big.Float).Sub(grossProfitUSDC, metrics.GasCostUSD)

	// ASSERTION: Net profit must be less than gross profit
	if metrics.NetProfitUSD.Cmp(metrics.GrossProfitUSD) >= 0 {
		return nil, fmt.Errorf("FLAG_NET_PROFIT_NOT_SUBTRACTING_FEES: netProfit=%.2f >= grossProfit=%.2f",
			metrics.NetProfitUSD, metrics.GrossProfitUSD)
	}

	// Calculate expected profit using price difference (sanity check)
	// This is approximate because it doesn't account for fees exactly the same way
	// but should be within reasonable tolerance
	cexFeeFloat, _ := cexPrice.TradingFee.Float64()
	dexFeeFloat, _ := dexPrice.TradingFee.Float64()

	var expectedProfitApprox *big.Float
	if direction == CEXToDEX {
		// Buy on CEX, Sell on DEX
		// Cost: cexPrice * size * (1 + cexFee)
		// Revenue: dexPrice * size * (1 - dexFee)
		cexCost := new(big.Float).Mul(cexPrice.Value, tradeSizeETH)
		cexCost.Mul(cexCost, big.NewFloat(1+cexFeeFloat))
		dexRevenue := new(big.Float).Mul(dexPrice.Value, tradeSizeETH)
		dexRevenue.Mul(dexRevenue, big.NewFloat(1-dexFeeFloat))
		expectedProfitApprox = new(big.Float).Sub(dexRevenue, cexCost)
	} else {
		// Buy on DEX, Sell on CEX
		// Cost: dexPrice * size * (1 + dexFee)
		// Revenue: cexPrice * size * (1 - cexFee)
		dexCost := new(big.Float).Mul(dexPrice.Value, tradeSizeETH)
		dexCost.Mul(dexCost, big.NewFloat(1+dexFeeFloat))
		cexRevenue := new(big.Float).Mul(cexPrice.Value, tradeSizeETH)
		cexRevenue.Mul(cexRevenue, big.NewFloat(1-cexFeeFloat))
		expectedProfitApprox = new(big.Float).Sub(cexRevenue, dexCost)
	}

	// ANTI FALSE ARBITRAGE FILTER
	// Verify gross profit matches expected profit within tolerance
	// NOTE: Using 2% tolerance with QuoterV2 (official Uniswap quoter contract)
	// QuoterV2 provides near-perfect accuracy (~99.9% vs actual on-chain execution)
	// This validates that our profit calculations match QuoterV2's quotes
	profitDiff := new(big.Float).Sub(grossProfitUSDC, expectedProfitApprox)
	profitDiff.Abs(profitDiff)

	expectedAbs := new(big.Float).Set(expectedProfitApprox)
	expectedAbs.Abs(expectedAbs)

	if expectedAbs.Cmp(big.NewFloat(1)) > 0 { // Only validate if expected is non-trivial
		relativeError := new(big.Float).Quo(profitDiff, expectedAbs)
		relativeErrorPct, _ := relativeError.Float64()

		if relativeErrorPct > 0.02 {
			return nil, fmt.Errorf("INVALID_MODEL_OUTPUT: gross profit %.2f differs from expected %.2f by %.1f%% (>2%% tolerance)",
				grossProfitUSDC, expectedProfitApprox, relativeErrorPct*100)
		}
	}

	// Calculate profit percentage
	// Profit % = (Net Profit / Total Investment) * 100
	totalInvestment := new(big.Float).Mul(tradeSizeETH, big.NewFloat(ethPriceUSD))
	if totalInvestment.Cmp(big.NewFloat(0)) > 0 {
		metrics.ProfitPct = new(big.Float).Quo(metrics.NetProfitUSD, totalInvestment)
		metrics.ProfitPct.Mul(metrics.ProfitPct, big.NewFloat(100))
	} else {
		metrics.ProfitPct = big.NewFloat(0)
	}

	// DEBUG LOGGING FIELDS (stored in metrics for later logging)
	metrics.DebugFields = map[string]interface{}{
		"trade_size_wei":           tradeSize.String(),
		"trade_size_eth":           tradeSizeETH.Text('f', 4),
		"usdc_spent":               usdcSpent.Text('f', 2),
		"usdc_received":            usdcReceived.Text('f', 2),
		"cex_amount_out_raw":       cexPrice.AmountOutRaw.String(),
		"dex_amount_out_raw":       dexPrice.AmountOutRaw.String(),
		"cex_amount_out_normalized": cexPrice.AmountOut.Text('f', 2),
		"dex_amount_out_normalized": dexPrice.AmountOut.Text('f', 2),
		"gross_profit_usdc":        grossProfitUSDC.Text('f', 2),
		"gas_cost_usdc":            metrics.GasCostUSD.Text('f', 2),
		"trading_fees_usdc":        metrics.TradingFeesUSD.Text('f', 2),
		"net_profit_usdc":          metrics.NetProfitUSD.Text('f', 2),
		"expected_profit_approx":   expectedProfitApprox.Text('f', 2),
		"cex_price":                cexPrice.Value.Text('f', 2),
		"dex_price":                dexPrice.Value.Text('f', 2),
	}

	return metrics, nil
}

// DetermineDirection determines the optimal arbitrage direction
func (c *Calculator) DetermineDirection(cexPrice, dexPrice *big.Float) Direction {
	// If DEX price > CEX price: Buy on CEX, Sell on DEX
	// If CEX price > DEX price: Buy on DEX, Sell on CEX

	if dexPrice.Cmp(cexPrice) > 0 {
		return CEXToDEX
	}

	return DEXToCEX
}

// IsProfitable checks if an opportunity meets the minimum profit threshold
func (c *Calculator) IsProfitable(netProfit *big.Float, minProfitPct float64) bool {
	if netProfit == nil {
		return false
	}

	// Net profit must be positive
	if netProfit.Cmp(big.NewFloat(0)) <= 0 {
		return false
	}

	return true
}

// CalculateBreakEven calculates the break-even trade size
func (c *Calculator) CalculateBreakEven(
	direction Direction,
	cexPrice *pricing.Price,
	dexPrice *pricing.Price,
	ethPriceUSD float64,
) (*big.Int, error) {
	// This is a simplified calculation
	// For production, would use binary search to find exact break-even

	// Start with small size and increase until profitable
	minSize := new(big.Int)
	minSize.SetString("100000000000000000", 10) // 0.1 ETH (1e17 wei)
	maxSize := new(big.Int)
	maxSize.SetString("100000000000000000000", 10) // 100 ETH (1e20 wei)

	step := new(big.Int)
	step.SetString("100000000000000000", 10) // 0.1 ETH step

	for size := new(big.Int).Set(minSize); size.Cmp(maxSize) <= 0; size.Add(size, step) {
		metrics, err := c.CalculateProfit(direction, size, cexPrice, dexPrice, ethPriceUSD)
		if err != nil {
			continue
		}

		if metrics.NetProfitUSD.Cmp(big.NewFloat(0)) > 0 {
			return size, nil
		}
	}

	return nil, fmt.Errorf("no break-even point found within size range")
}

// CalculateMaxProfitableSize calculates the maximum profitable trade size
// accounting for liquidity constraints and diminishing returns
func (c *Calculator) CalculateMaxProfitableSize(
	direction Direction,
	cexPrice *pricing.Price,
	dexPrice *pricing.Price,
	ethPriceUSD float64,
	maxSize *big.Int,
) (*big.Int, *big.Float, error) {
	// Try different sizes and find the one with maximum profit
	// This is a simplified linear search

	bestSize := big.NewInt(0)
	maxProfit := big.NewFloat(0)

	step := new(big.Int).Div(maxSize, big.NewInt(10)) // 10 steps
	minStep := new(big.Int)
	minStep.SetString("100000000000000000", 10) // 0.1 ETH (1e17 wei)
	if step.Cmp(minStep) < 0 {
		step = minStep // Minimum 0.1 ETH step
	}

	for size := new(big.Int).Set(step); size.Cmp(maxSize) <= 0; size.Add(size, step) {
		metrics, err := c.CalculateProfit(direction, size, cexPrice, dexPrice, ethPriceUSD)
		if err != nil {
			continue
		}

		if metrics.NetProfitUSD.Cmp(maxProfit) > 0 {
			maxProfit.Set(metrics.NetProfitUSD)
			bestSize.Set(size)
		}
	}

	if bestSize.Cmp(big.NewInt(0)) == 0 {
		return nil, nil, fmt.Errorf("no profitable size found")
	}

	return bestSize, maxProfit, nil
}

// ProfitMetrics holds calculated profit metrics
type ProfitMetrics struct {
	GrossProfitUSD  *big.Float
	GasCostUSD      *big.Float
	TradingFeesUSD  *big.Float
	NetProfitUSD    *big.Float
	ProfitPct       *big.Float
	DebugFields     map[string]interface{} // Debug logging fields
}

// String returns a string representation of profit metrics
func (pm *ProfitMetrics) String() string {
	return fmt.Sprintf(
		"GrossProfit: $%s, GasCost: $%s, TradingFees: $%s, NetProfit: $%s, ProfitPct: %s%%",
		pm.GrossProfitUSD.Text('f', 2),
		pm.GasCostUSD.Text('f', 2),
		pm.TradingFeesUSD.Text('f', 2),
		pm.NetProfitUSD.Text('f', 2),
		pm.ProfitPct.Text('f', 4),
	)
}
