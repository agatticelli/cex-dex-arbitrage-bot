package uniswapv3

import (
	"errors"
	"math/big"
)

var (
	ErrInvalidLiquidity     = errors.New("liquidity must be positive")
	ErrInvalidPrice         = errors.New("invalid sqrt price")
	ErrInsufficientLiquidity = errors.New("insufficient liquidity")
)

// GetAmount0Delta calculates amount0 delta for a liquidity change
// amount0 = liquidity * (sqrt(upper) - sqrt(lower)) / (sqrt(upper) * sqrt(lower))
// Ported from Uniswap V3 SqrtPriceMath.sol
func GetAmount0Delta(sqrtRatioAX96, sqrtRatioBX96, liquidity *big.Int, roundUp bool) *big.Int {
	if sqrtRatioAX96.Cmp(sqrtRatioBX96) > 0 {
		sqrtRatioAX96, sqrtRatioBX96 = sqrtRatioBX96, sqrtRatioAX96
	}

	numerator1 := new(big.Int).Lsh(liquidity, 96)
	numerator2 := new(big.Int).Sub(sqrtRatioBX96, sqrtRatioAX96)

	if roundUp {
		return mulDivRoundingUp(mulDivRoundingUp(numerator1, numerator2, sqrtRatioBX96), big.NewInt(1), sqrtRatioAX96)
	}

	return new(big.Int).Div(new(big.Int).Div(new(big.Int).Mul(numerator1, numerator2), sqrtRatioBX96), sqrtRatioAX96)
}

// GetAmount1Delta calculates amount1 delta for a liquidity change
// amount1 = liquidity * (sqrt(upper) - sqrt(lower))
// Ported from Uniswap V3 SqrtPriceMath.sol
func GetAmount1Delta(sqrtRatioAX96, sqrtRatioBX96, liquidity *big.Int, roundUp bool) *big.Int {
	if sqrtRatioAX96.Cmp(sqrtRatioBX96) > 0 {
		sqrtRatioAX96, sqrtRatioBX96 = sqrtRatioBX96, sqrtRatioAX96
	}

	diff := new(big.Int).Sub(sqrtRatioBX96, sqrtRatioAX96)

	if roundUp {
		return mulDivRoundingUp(liquidity, diff, new(big.Int).Lsh(big.NewInt(1), 96))
	}

	return new(big.Int).Div(new(big.Int).Mul(liquidity, diff), new(big.Int).Lsh(big.NewInt(1), 96))
}

// GetNextSqrtPriceFromAmount0RoundingUp calculates next sqrt price given amount0 delta
// Used when adding liquidity or swapping token1 for token0
func GetNextSqrtPriceFromAmount0RoundingUp(sqrtPX96, liquidity, amount *big.Int, add bool) (*big.Int, error) {
	if amount.Cmp(big.NewInt(0)) == 0 {
		return sqrtPX96, nil
	}

	numerator1 := new(big.Int).Lsh(liquidity, 96)

	if add {
		// If adding: liquidity / ((liquidity / sqrtPX96) + amount)
		denominator := new(big.Int).Add(
			new(big.Int).Div(numerator1, sqrtPX96),
			amount,
		)
		return mulDivRoundingUp(numerator1, sqrtPX96, denominator), nil
	}

	// If removing: liquidity / ((liquidity / sqrtPX96) - amount)
	product := new(big.Int).Mul(amount, sqrtPX96)
	denominator := new(big.Int).Sub(numerator1, product)

	if denominator.Cmp(big.NewInt(0)) <= 0 {
		return nil, ErrInsufficientLiquidity
	}

	return mulDivRoundingUp(numerator1, sqrtPX96, denominator), nil
}

// GetNextSqrtPriceFromAmount1RoundingDown calculates next sqrt price given amount1 delta
// Used when adding liquidity or swapping token0 for token1
func GetNextSqrtPriceFromAmount1RoundingDown(sqrtPX96, liquidity, amount *big.Int, add bool) *big.Int {
	if add {
		// If adding: sqrt(price) + (amount / liquidity)
		quotient := new(big.Int).Lsh(amount, 96)
		quotient.Div(quotient, liquidity)
		return new(big.Int).Add(sqrtPX96, quotient)
	}

	// If removing: sqrt(price) - (amount / liquidity)
	quotient := new(big.Int).Lsh(amount, 96)
	quotient = mulDivRoundingUp(quotient, big.NewInt(1), liquidity)
	result := new(big.Int).Sub(sqrtPX96, quotient)

	if result.Cmp(big.NewInt(0)) < 0 {
		return big.NewInt(0)
	}

	return result
}

// GetNextSqrtPriceFromInput calculates the next sqrt price given an input amount
// zeroForOne: true if swapping token0 for token1, false otherwise
func GetNextSqrtPriceFromInput(sqrtPX96, liquidity, amountIn *big.Int, zeroForOne bool) (*big.Int, error) {
	if sqrtPX96.Cmp(big.NewInt(0)) <= 0 {
		return nil, ErrInvalidPrice
	}
	if liquidity.Cmp(big.NewInt(0)) <= 0 {
		return nil, ErrInvalidLiquidity
	}

	if zeroForOne {
		return GetNextSqrtPriceFromAmount0RoundingUp(sqrtPX96, liquidity, amountIn, true)
	}

	return GetNextSqrtPriceFromAmount1RoundingDown(sqrtPX96, liquidity, amountIn, true), nil
}

// GetNextSqrtPriceFromOutput calculates the next sqrt price given an output amount
// zeroForOne: true if swapping token0 for token1, false otherwise
func GetNextSqrtPriceFromOutput(sqrtPX96, liquidity, amountOut *big.Int, zeroForOne bool) (*big.Int, error) {
	if sqrtPX96.Cmp(big.NewInt(0)) <= 0 {
		return nil, ErrInvalidPrice
	}
	if liquidity.Cmp(big.NewInt(0)) <= 0 {
		return nil, ErrInvalidLiquidity
	}

	if zeroForOne {
		return GetNextSqrtPriceFromAmount1RoundingDown(sqrtPX96, liquidity, amountOut, false), nil
	}

	return GetNextSqrtPriceFromAmount0RoundingUp(sqrtPX96, liquidity, amountOut, false)
}

// mulDivRoundingUp performs (a * b) / c with rounding up
func mulDivRoundingUp(a, b, denominator *big.Int) *big.Int {
	product := new(big.Int).Mul(a, b)
	result := new(big.Int).Div(product, denominator)

	// Check if there's a remainder
	remainder := new(big.Int).Mod(product, denominator)
	if remainder.Cmp(big.NewInt(0)) > 0 {
		result.Add(result, big.NewInt(1))
	}

	return result
}

// Q96ToFloat converts a Q96 fixed-point number to float64
// Useful for human-readable price display
func Q96ToFloat(sqrtPriceX96 *big.Int) float64 {
	// price = (sqrtPriceX96 / 2^96)^2
	q96 := new(big.Float).SetInt(new(big.Int).Lsh(big.NewInt(1), 96))
	sqrtPrice := new(big.Float).Quo(new(big.Float).SetInt(sqrtPriceX96), q96)

	price := new(big.Float).Mul(sqrtPrice, sqrtPrice)
	priceFloat, _ := price.Float64()

	return priceFloat
}

// FloatToQ96 converts a float price to Q96 sqrt price
func FloatToQ96(price float64) *big.Int {
	// sqrtPrice = sqrt(price)
	// sqrtPriceX96 = sqrtPrice * 2^96
	sqrtPrice := new(big.Float).Sqrt(big.NewFloat(price))
	q96 := new(big.Float).SetInt(new(big.Int).Lsh(big.NewInt(1), 96))

	sqrtPriceX96Float := new(big.Float).Mul(sqrtPrice, q96)

	result, _ := sqrtPriceX96Float.Int(nil)
	return result
}
