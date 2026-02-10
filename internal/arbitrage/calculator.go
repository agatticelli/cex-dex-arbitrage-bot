package arbitrage

import (
	"fmt"
	"math/big"

	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/pricing"
)

type CalculatorConfig struct {
	MinExpectedProfitUSD float64
	MinAbsDiffUSD        float64
	MaxRelDiffTolerance  float64
}

type Calculator struct {
	config CalculatorConfig
}

func NewCalculator() *Calculator {
	return NewCalculatorWithConfig(CalculatorConfig{})
}

func NewCalculatorWithConfig(cfg CalculatorConfig) *Calculator {
	if cfg.MinExpectedProfitUSD == 0 {
		cfg.MinExpectedProfitUSD = 5.0
	}
	if cfg.MinAbsDiffUSD == 0 {
		cfg.MinAbsDiffUSD = 2.0
	}
	if cfg.MaxRelDiffTolerance == 0 {
		cfg.MaxRelDiffTolerance = 0.05
	}
	return &Calculator{config: cfg}
}

func (c *Calculator) CalculateProfit(
	direction Direction,
	tradeSize *big.Int,
	cexPrice *pricing.Price,
	dexPrice *pricing.Price,
	gasCostUSD *big.Float,
) (*ProfitMetrics, error) {
	metrics := &ProfitMetrics{}

	baseDecimals := cexPrice.BaseDecimals
	if baseDecimals == 0 {
		baseDecimals = 18
	}
	tradeSizeBase := rawToFloat(tradeSize, baseDecimals)

	if cexPrice.AmountOut == nil || dexPrice.AmountOut == nil {
		return nil, fmt.Errorf("AmountOut fields are required for accurate profit calculation")
	}

	var grossProfitUSDC *big.Float
	var usdcSpent, usdcReceived *big.Float

	if direction == CEXToDEX {
		usdcSpent = cexPrice.AmountOut
		usdcReceived = dexPrice.AmountOut
		grossProfitUSDC = new(big.Float).Sub(usdcReceived, usdcSpent)
	} else {
		usdcSpent = dexPrice.AmountOut
		usdcReceived = cexPrice.AmountOut
		grossProfitUSDC = new(big.Float).Sub(usdcReceived, usdcSpent)
	}

	// Fees are already included in AmountOut, calculate for display only
	cexTradeValue := new(big.Float).Mul(cexPrice.Value, tradeSizeBase)
	dexTradeValue := new(big.Float).Mul(dexPrice.Value, tradeSizeBase)
	cexFee := new(big.Float).Mul(cexTradeValue, cexPrice.TradingFee)
	dexFee := new(big.Float).Mul(dexTradeValue, dexPrice.TradingFee)
	metrics.TradingFeesUSD = new(big.Float).Add(cexFee, dexFee)

	if gasCostUSD == nil {
		metrics.GasCostUSD = big.NewFloat(0)
	} else {
		metrics.GasCostUSD = new(big.Float).Set(gasCostUSD)
	}

	metrics.GrossProfitUSD = grossProfitUSDC
	metrics.NetProfitUSD = new(big.Float).Sub(grossProfitUSDC, metrics.GasCostUSD)

	if metrics.NetProfitUSD.Cmp(metrics.GrossProfitUSD) > 0 {
		return nil, fmt.Errorf("FLAG_NET_PROFIT_EXCEEDS_GROSS: netProfit=%.2f > grossProfit=%.2f (impossible)",
			metrics.NetProfitUSD, metrics.GrossProfitUSD)
	}

	// Sanity check: compare flow-based profit with price-based approximation
	cexFeeFloat, _ := cexPrice.TradingFee.Float64()
	dexFeeFloat, _ := dexPrice.TradingFee.Float64()

	var expectedProfitApprox *big.Float
	if direction == CEXToDEX {
		cexCost := new(big.Float).Mul(cexPrice.Value, tradeSizeBase)
		cexCost.Mul(cexCost, big.NewFloat(1+cexFeeFloat))
		dexRevenue := new(big.Float).Mul(dexPrice.Value, tradeSizeBase)
		dexRevenue.Mul(dexRevenue, big.NewFloat(1-dexFeeFloat))
		expectedProfitApprox = new(big.Float).Sub(dexRevenue, cexCost)
	} else {
		dexCost := new(big.Float).Mul(dexPrice.Value, tradeSizeBase)
		dexCost.Mul(dexCost, big.NewFloat(1+dexFeeFloat))
		cexRevenue := new(big.Float).Mul(cexPrice.Value, tradeSizeBase)
		cexRevenue.Mul(cexRevenue, big.NewFloat(1-cexFeeFloat))
		expectedProfitApprox = new(big.Float).Sub(cexRevenue, dexCost)
	}

	profitDiff := new(big.Float).Sub(grossProfitUSDC, expectedProfitApprox)
	profitDiff.Abs(profitDiff)

	expectedAbs := new(big.Float).Set(expectedProfitApprox)
	expectedAbs.Abs(expectedAbs)

	if expectedAbs.Cmp(big.NewFloat(c.config.MinExpectedProfitUSD)) > 0 {
		relativeError := new(big.Float).Quo(profitDiff, expectedAbs)
		relativeErrorPct, _ := relativeError.Float64()

		if profitDiff.Cmp(big.NewFloat(c.config.MinAbsDiffUSD)) > 0 && relativeErrorPct > c.config.MaxRelDiffTolerance {
			metrics.ValidationWarning = fmt.Sprintf(
				"INVALID_MODEL_OUTPUT: gross profit %.2f differs from expected %.2f by %.1f%% (>%0.0f%%, >$%.2f, expected>$%.2f)",
				grossProfitUSDC,
				expectedProfitApprox,
				relativeErrorPct*100,
				c.config.MaxRelDiffTolerance*100,
				c.config.MinAbsDiffUSD,
				c.config.MinExpectedProfitUSD,
			)
		}
	}

	var buyPrice *pricing.Price
	if direction == CEXToDEX {
		buyPrice = cexPrice
	} else {
		buyPrice = dexPrice
	}

	totalInvestment := new(big.Float).Mul(tradeSizeBase, buyPrice.Value)
	if totalInvestment.Cmp(big.NewFloat(0)) > 0 {
		metrics.ProfitPct = new(big.Float).Quo(metrics.NetProfitUSD, totalInvestment)
		metrics.ProfitPct.Mul(metrics.ProfitPct, big.NewFloat(100))
	} else {
		metrics.ProfitPct = big.NewFloat(0)
	}

	metrics.DebugFields = map[string]interface{}{
		"trade_size_raw":            tradeSize.String(),
		"trade_size_base":           tradeSizeBase.Text('f', 4),
		"usdc_spent":                usdcSpent.Text('f', 2),
		"usdc_received":             usdcReceived.Text('f', 2),
		"cex_amount_out_raw":        cexPrice.AmountOutRaw.String(),
		"dex_amount_out_raw":        dexPrice.AmountOutRaw.String(),
		"cex_amount_out_normalized": cexPrice.AmountOut.Text('f', 2),
		"dex_amount_out_normalized": dexPrice.AmountOut.Text('f', 2),
		"gross_profit_usdc":         grossProfitUSDC.Text('f', 2),
		"gas_cost_usdc":             metrics.GasCostUSD.Text('f', 2),
		"trading_fees_usdc":         metrics.TradingFeesUSD.Text('f', 2),
		"net_profit_usdc":           metrics.NetProfitUSD.Text('f', 2),
		"expected_profit_approx":    expectedProfitApprox.Text('f', 2),
		"validation_warning":        metrics.ValidationWarning,
		"cex_price":                 cexPrice.Value.Text('f', 2),
		"dex_price":                 dexPrice.Value.Text('f', 2),
	}

	return metrics, nil
}

func (c *Calculator) DetermineDirection(cexPrice, dexPrice *big.Float) Direction {
	if dexPrice.Cmp(cexPrice) > 0 {
		return CEXToDEX
	}
	return DEXToCEX
}

func (c *Calculator) IsProfitable(profitPct *big.Float, minProfitPct float64) bool {
	if profitPct == nil {
		return false
	}
	return profitPct.Cmp(big.NewFloat(minProfitPct)) >= 0
}

func (c *Calculator) CalculateBreakEven(
	direction Direction,
	cexPrice *pricing.Price,
	dexPrice *pricing.Price,
	gasCostUSD *big.Float,
) (*big.Int, error) {
	baseDecimals := cexPrice.BaseDecimals
	if baseDecimals == 0 {
		baseDecimals = 18
	}

	minSize := floatToRaw(0.1, baseDecimals)
	maxSize := floatToRaw(100.0, baseDecimals)
	step := floatToRaw(0.1, baseDecimals)

	for size := new(big.Int).Set(minSize); size.Cmp(maxSize) <= 0; size.Add(size, step) {
		metrics, err := c.CalculateProfit(direction, size, cexPrice, dexPrice, gasCostUSD)
		if err != nil {
			continue
		}

		if metrics.NetProfitUSD.Cmp(big.NewFloat(0)) > 0 {
			return size, nil
		}
	}

	return nil, fmt.Errorf("no break-even point found within size range")
}

func (c *Calculator) CalculateMaxProfitableSize(
	direction Direction,
	cexPrice *pricing.Price,
	dexPrice *pricing.Price,
	gasCostUSD *big.Float,
	maxSize *big.Int,
) (*big.Int, *big.Float, error) {
	bestSize := big.NewInt(0)
	maxProfit := big.NewFloat(0)

	step := new(big.Int).Div(maxSize, big.NewInt(10))
	baseDecimals := cexPrice.BaseDecimals
	if baseDecimals == 0 {
		baseDecimals = 18
	}
	minStep := floatToRaw(0.1, baseDecimals)
	if step.Cmp(minStep) < 0 {
		step = minStep
	}

	for size := new(big.Int).Set(step); size.Cmp(maxSize) <= 0; size.Add(size, step) {
		metrics, err := c.CalculateProfit(direction, size, cexPrice, dexPrice, gasCostUSD)
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

type ProfitMetrics struct {
	GrossProfitUSD    *big.Float
	GasCostUSD        *big.Float
	TradingFeesUSD    *big.Float
	NetProfitUSD      *big.Float
	ProfitPct         *big.Float
	ValidationWarning string
	DebugFields       map[string]interface{}
}

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
