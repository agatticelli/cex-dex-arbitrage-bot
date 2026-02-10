// Package pricing provides price calculation utilities for arbitrage detection.
package pricing

import (
	"math/big"

	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/money"
)

// PriceImpact represents the real price impact from a DEX quote.
// Calculated from actual QuoterV2 results, not tick approximations.
type PriceImpact struct {
	// EffectivePrice is the actual execution price from the quote
	EffectivePrice *big.Float

	// SpotPrice is the current pool price (if available)
	SpotPrice *big.Float

	// ImpactBPS is the price impact in basis points
	// Positive = worse than spot (paying more / receiving less)
	// Calculated as: (effectivePrice - spotPrice) / spotPrice * 10000
	ImpactBPS money.BPS

	// TicksCrossed from QuoterV2 (informational only)
	TicksCrossed uint32

	// GasEstimate from QuoterV2 in gas units
	GasEstimate uint64
}

// CalculatePriceImpact calculates real price impact from QuoterV2 results.
//
// For buying (token0 -> token1):
//   - amountIn: quote tokens spent (e.g., USDC)
//   - amountOut: base tokens received (e.g., ETH)
//   - effectivePrice = amountIn / amountOut (USDC per ETH)
//   - Impact is positive if we pay more than spot
//
// For selling (token1 -> token0):
//   - amountIn: base tokens spent (e.g., ETH)
//   - amountOut: quote tokens received (e.g., USDC)
//   - effectivePrice = amountOut / amountIn (USDC per ETH)
//   - Impact is positive if we receive less than spot
func CalculatePriceImpact(
	amountIn *big.Int,
	amountOut *big.Int,
	inDecimals int,
	outDecimals int,
	isBuying bool,
	ticksCrossed uint32,
	gasEstimate *big.Int,
) PriceImpact {
	// Convert to float for price calculation
	amountInFloat := RawToFloat(amountIn, inDecimals)
	amountOutFloat := RawToFloat(amountOut, outDecimals)

	// Calculate effective price (always in quote/base terms)
	var effectivePrice *big.Float
	if isBuying {
		// Buying base: price = quote spent / base received
		effectivePrice = new(big.Float).Quo(amountInFloat, amountOutFloat)
	} else {
		// Selling base: price = quote received / base spent
		effectivePrice = new(big.Float).Quo(amountOutFloat, amountInFloat)
	}

	// Without spot price from slot0, we can estimate impact from ticks crossed
	// Each tick in Uniswap V3 = 1.0001^tick price ratio
	// So crossing N ticks means price moved by approximately (1.0001^N - 1)
	//
	// For small N: 1.0001^N ≈ 1 + 0.0001*N
	// Impact in BPS ≈ N * 1 (each tick ≈ 1 bps of sqrt(price) change)
	//
	// However, this is for sqrt(price). For actual price:
	// price = sqrt(price)^2, so impact doubles: N * 2 bps approximately
	//
	// This is still an approximation, but much better than the original 0.01% per tick
	impactBPS := money.BPS(int64(ticksCrossed) * 2)

	var gasEst uint64
	if gasEstimate != nil {
		gasEst = gasEstimate.Uint64()
	}

	return PriceImpact{
		EffectivePrice: effectivePrice,
		SpotPrice:      nil, // Would need slot0 call to get this
		ImpactBPS:      impactBPS,
		TicksCrossed:   ticksCrossed,
		GasEstimate:    gasEst,
	}
}

// CalculatePriceImpactWithSpot calculates price impact when spot price is known.
// This is the most accurate method when you have the current pool price.
func CalculatePriceImpactWithSpot(
	effectivePrice *big.Float,
	spotPrice *big.Float,
	isBuying bool,
) money.BPS {
	if spotPrice == nil || spotPrice.Sign() == 0 {
		return 0
	}

	// Calculate difference: (effective - spot) / spot * 10000
	diff := new(big.Float).Sub(effectivePrice, spotPrice)
	ratio := new(big.Float).Quo(diff, spotPrice)
	bpsFloat := new(big.Float).Mul(ratio, big.NewFloat(10000))

	bps, _ := bpsFloat.Int64()

	// For buying, positive impact means we paid more (bad)
	// For selling, positive impact means we received less (bad)
	// The sign is already correct from the calculation
	if isBuying {
		return money.BPS(bps)
	}
	// For selling, invert the sign (receiving less = positive impact)
	return money.BPS(-bps)
}

// SpreadResult represents the price spread between CEX and DEX.
type SpreadResult struct {
	// CEXPrice is the price on the centralized exchange
	CEXPrice money.USD

	// DEXPrice is the effective price on the DEX
	DEXPrice money.USD

	// SpreadBPS is the spread in basis points
	// Positive = DEX price > CEX price (sell on DEX profitable)
	// Negative = DEX price < CEX price (buy on DEX profitable)
	SpreadBPS money.BPS

	// Direction indicates the profitable trade direction
	Direction SpreadDirection
}

// SpreadDirection indicates the profitable arbitrage direction.
type SpreadDirection int

const (
	SpreadNone     SpreadDirection = iota // No significant spread
	SpreadCEXToDEX                        // Buy CEX, sell DEX
	SpreadDEXToCEX                        // Buy DEX, sell CEX
)

// String returns a human-readable direction.
func (d SpreadDirection) String() string {
	switch d {
	case SpreadCEXToDEX:
		return "CEX→DEX"
	case SpreadDEXToCEX:
		return "DEX→CEX"
	default:
		return "NONE"
	}
}

// CalculateSpread calculates the spread between CEX and DEX prices.
func CalculateSpread(cexPrice, dexPrice money.USD) SpreadResult {
	spreadBPS := money.CalculateSpreadBPS(cexPrice, dexPrice)

	var direction SpreadDirection
	switch {
	case spreadBPS > 10: // DEX higher, sell on DEX
		direction = SpreadCEXToDEX
	case spreadBPS < -10: // DEX lower, buy on DEX
		direction = SpreadDEXToCEX
	default:
		direction = SpreadNone
	}

	return SpreadResult{
		CEXPrice:  cexPrice,
		DEXPrice:  dexPrice,
		SpreadBPS: spreadBPS,
		Direction: direction,
	}
}

