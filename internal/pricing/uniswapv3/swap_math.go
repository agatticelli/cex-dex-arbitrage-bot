package uniswapv3

import (
	"math/big"
)

// SwapStepResult holds the result of a single swap computation step
type SwapStepResult struct {
	SqrtRatioNextX96 *big.Int // The sqrt price after the swap step
	AmountIn         *big.Int // Amount of input token consumed
	AmountOut        *big.Int // Amount of output token produced
	FeeAmount        *big.Int // Fee amount charged
}

// ComputeSwapStep computes the result of swapping within a single price range
// This is the core swap calculation for Uniswap V3
// Ported from Uniswap V3 SwapMath.sol
func ComputeSwapStep(
	sqrtRatioCurrentX96 *big.Int, // Current sqrt price
	sqrtRatioTargetX96 *big.Int, // Target sqrt price (next tick or input limit)
	liquidity *big.Int, // Available liquidity
	amountRemaining *big.Int, // Amount remaining to be swapped
	feePips int, // Fee in pips (e.g., 3000 = 0.3%)
) *SwapStepResult {
	result := &SwapStepResult{
		SqrtRatioNextX96: big.NewInt(0),
		AmountIn:         big.NewInt(0),
		AmountOut:        big.NewInt(0),
		FeeAmount:        big.NewInt(0),
	}

	zeroForOne := sqrtRatioCurrentX96.Cmp(sqrtRatioTargetX96) >= 0
	exactIn := amountRemaining.Cmp(big.NewInt(0)) >= 0

	if exactIn {
		// Calculate amount in after fee
		amountRemainingLessFee := new(big.Int).Mul(amountRemaining, big.NewInt(int64(1000000-feePips)))
		amountRemainingLessFee.Div(amountRemainingLessFee, big.NewInt(1000000))

		var amountIn *big.Int
		if zeroForOne {
			amountIn = GetAmount0Delta(sqrtRatioTargetX96, sqrtRatioCurrentX96, liquidity, true)
		} else {
			amountIn = GetAmount1Delta(sqrtRatioCurrentX96, sqrtRatioTargetX96, liquidity, true)
		}

		// Check if we can reach the target price
		if amountRemainingLessFee.Cmp(amountIn) >= 0 {
			// We can reach the target price
			result.SqrtRatioNextX96 = sqrtRatioTargetX96
			result.AmountIn = amountIn
		} else {
			// We can't reach the target, calculate where we'll end up
			result.SqrtRatioNextX96, _ = GetNextSqrtPriceFromInput(
				sqrtRatioCurrentX96,
				liquidity,
				amountRemainingLessFee,
				zeroForOne,
			)
			result.AmountIn = amountRemainingLessFee
		}

		// Calculate amount out
		if result.SqrtRatioNextX96.Cmp(sqrtRatioTargetX96) == 0 {
			// Reached target price
			if zeroForOne {
				result.AmountOut = GetAmount1Delta(result.SqrtRatioNextX96, sqrtRatioCurrentX96, liquidity, false)
			} else {
				result.AmountOut = GetAmount0Delta(sqrtRatioCurrentX96, result.SqrtRatioNextX96, liquidity, false)
			}
		} else {
			// Didn't reach target price
			if zeroForOne {
				result.AmountOut = GetAmount1Delta(result.SqrtRatioNextX96, sqrtRatioCurrentX96, liquidity, false)
			} else {
				result.AmountOut = GetAmount0Delta(sqrtRatioCurrentX96, result.SqrtRatioNextX96, liquidity, false)
			}
		}

		// Calculate fee: feeAmount = ceil(amountIn * feePips / (1000000 - feePips))
		result.FeeAmount = new(big.Int).Sub(amountRemaining, result.AmountIn)
		if result.FeeAmount.Cmp(big.NewInt(0)) < 0 {
			result.FeeAmount = big.NewInt(0)
		}

	} else {
		// Exact output case
		var amountOut *big.Int
		if zeroForOne {
			amountOut = GetAmount1Delta(sqrtRatioTargetX96, sqrtRatioCurrentX96, liquidity, false)
		} else {
			amountOut = GetAmount0Delta(sqrtRatioCurrentX96, sqrtRatioTargetX96, liquidity, false)
		}

		// amountRemaining is negative for exact output
		amountRemainingAbs := new(big.Int).Neg(amountRemaining)

		if amountRemainingAbs.Cmp(amountOut) >= 0 {
			// We can provide the full output
			result.SqrtRatioNextX96 = sqrtRatioTargetX96
			result.AmountOut = amountOut
		} else {
			// We can't provide full output
			result.SqrtRatioNextX96, _ = GetNextSqrtPriceFromOutput(
				sqrtRatioCurrentX96,
				liquidity,
				amountRemainingAbs,
				zeroForOne,
			)
			result.AmountOut = amountRemainingAbs
		}

		// Calculate amount in
		if result.SqrtRatioNextX96.Cmp(sqrtRatioTargetX96) == 0 {
			if zeroForOne {
				result.AmountIn = GetAmount0Delta(result.SqrtRatioNextX96, sqrtRatioCurrentX96, liquidity, true)
			} else {
				result.AmountIn = GetAmount1Delta(sqrtRatioCurrentX96, result.SqrtRatioNextX96, liquidity, true)
			}
		} else {
			if zeroForOne {
				result.AmountIn = GetAmount0Delta(result.SqrtRatioNextX96, sqrtRatioCurrentX96, liquidity, true)
			} else {
				result.AmountIn = GetAmount1Delta(sqrtRatioCurrentX96, result.SqrtRatioNextX96, liquidity, true)
			}
		}

		// Calculate fee
		numerator := new(big.Int).Mul(result.AmountIn, big.NewInt(int64(feePips)))
		result.FeeAmount = mulDivRoundingUp(numerator, big.NewInt(1), big.NewInt(int64(1000000-feePips)))
	}

	return result
}

// SimulateSwap simulates a full swap across multiple ticks
// This is a simplified version that assumes infinite liquidity at current price
// For production, you would need to walk through ticks and handle liquidity changes
func SimulateSwap(
	sqrtPriceX96 *big.Int, // Current pool sqrt price
	liquidity *big.Int, // Current pool liquidity
	amountIn *big.Int, // Amount to swap in
	zeroForOne bool, // Direction: token0 -> token1
	feePips int, // Fee in pips (e.g., 3000 = 0.3%)
) (*big.Int, *big.Int, error) {
	// Simplified: assume we don't cross any ticks
	// Calculate next price after consuming amountIn

	// Remove fee from amount in
	amountInAfterFee := new(big.Int).Mul(amountIn, big.NewInt(int64(1000000-feePips)))
	amountInAfterFee.Div(amountInAfterFee, big.NewInt(1000000))

	// Calculate next sqrt price
	sqrtPriceNextX96, err := GetNextSqrtPriceFromInput(sqrtPriceX96, liquidity, amountInAfterFee, zeroForOne)
	if err != nil {
		return nil, nil, err
	}

	// DEBUG: Log swap math calculations
	// fmt.Printf("DEBUG SimulateSwap:\n")
	// fmt.Printf("  zeroForOne: %v\n", zeroForOne)
	// fmt.Printf("  amountIn: %s\n", amountIn.String())
	// fmt.Printf("  amountInAfterFee: %s\n", amountInAfterFee.String())
	// fmt.Printf("  sqrtPriceBefore: %s\n", sqrtPriceX96.String())
	// fmt.Printf("  sqrtPriceAfter: %s\n", sqrtPriceNextX96.String())
	// fmt.Printf("  liquidity: %s\n", liquidity.String())

	// Calculate amount out
	var amountOut *big.Int
	if zeroForOne {
		amountOut = GetAmount1Delta(sqrtPriceNextX96, sqrtPriceX96, liquidity, false)
	} else {
		amountOut = GetAmount0Delta(sqrtPriceX96, sqrtPriceNextX96, liquidity, false)
	}

	// fmt.Printf("  amountOut: %s\n\n", amountOut.String())

	return amountOut, sqrtPriceNextX96, nil
}

// CalculatePriceImpact calculates the price impact of a swap
// Returns the price impact as a percentage
func CalculatePriceImpact(sqrtPriceBefore, sqrtPriceAfter *big.Int, zeroForOne bool) float64 {
	priceBefore := Q96ToFloat(sqrtPriceBefore)
	priceAfter := Q96ToFloat(sqrtPriceAfter)

	var impact float64
	if zeroForOne {
		// Price of token1 in terms of token0 increases (price goes down in normal terms)
		impact = (priceAfter - priceBefore) / priceBefore
	} else {
		// Price of token1 in terms of token0 decreases (price goes up in normal terms)
		impact = (priceBefore - priceAfter) / priceBefore
	}

	return impact * 100 // Convert to percentage
}

// GetAmountOutForAmountIn calculates the output amount for a given input amount
// This is a high-level helper function for quote calculations
func GetAmountOutForAmountIn(
	sqrtPriceX96 *big.Int,
	liquidity *big.Int,
	amountIn *big.Int,
	zeroForOne bool,
	feePips int,
) (*big.Int, error) {
	amountOut, _, err := SimulateSwap(sqrtPriceX96, liquidity, amountIn, zeroForOne, feePips)
	return amountOut, err
}
