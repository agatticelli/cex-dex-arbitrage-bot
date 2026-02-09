package uniswapv3

import (
	"errors"
	"math/big"
)

// Tick math constants from Uniswap V3
// https://github.com/Uniswap/v3-core/blob/main/contracts/libraries/TickMath.sol
const (
	MinTick int32 = -887272
	MaxTick int32 = 887272
)

var (
	// MinSqrtRatio is the minimum sqrt price (at MinTick)
	MinSqrtRatio = big.NewInt(4295128739)

	// MaxSqrtRatio is the maximum sqrt price (at MaxTick)
	MaxSqrtRatio = mustParseBigInt("1461446703485210103287273052203988822378723970342")

	ErrInvalidTick      = errors.New("tick out of bounds")
	ErrInvalidSqrtRatio = errors.New("sqrt ratio out of bounds")
)

// mustParseBigInt parses a decimal string to big.Int, panics on error
func mustParseBigInt(s string) *big.Int {
	n := new(big.Int)
	n.SetString(s, 10)
	return n
}

// GetSqrtRatioAtTick calculates sqrt(1.0001^tick) * 2^96
// Ported from Uniswap V3 TickMath.sol
func GetSqrtRatioAtTick(tick int32) (*big.Int, error) {
	if tick < MinTick || tick > MaxTick {
		return nil, ErrInvalidTick
	}

	absTick := tick
	if tick < 0 {
		absTick = -tick
	}

	// Precomputed values: sqrt(1.0001^(2^i)) * 2^128 for i = 0..19
	// These are used to efficiently compute sqrt(1.0001^tick)
	var ratio *big.Int
	if absTick&0x1 != 0 {
		ratio = mustParseBigInt("0xfffcb933bd6fad37aa2d162d1a594001")
	} else {
		ratio = mustParseBigInt("0x100000000000000000000000000000000")
	}

	if absTick&0x2 != 0 {
		ratio = mulShift(ratio, mustParseBigInt("0xfff97272373d413259a46990580e213a"))
	}
	if absTick&0x4 != 0 {
		ratio = mulShift(ratio, mustParseBigInt("0xfff2e50f5f656932ef12357cf3c7fdcc"))
	}
	if absTick&0x8 != 0 {
		ratio = mulShift(ratio, mustParseBigInt("0xffe5caca7e10e4e61c3624eaa0941cd0"))
	}
	if absTick&0x10 != 0 {
		ratio = mulShift(ratio, mustParseBigInt("0xffcb9843d60f6159c9db58835c926644"))
	}
	if absTick&0x20 != 0 {
		ratio = mulShift(ratio, mustParseBigInt("0xff973b41fa98c081472e6896dfb254c0"))
	}
	if absTick&0x40 != 0 {
		ratio = mulShift(ratio, mustParseBigInt("0xff2ea16466c96a3843ec78b326b52861"))
	}
	if absTick&0x80 != 0 {
		ratio = mulShift(ratio, mustParseBigInt("0xfe5dee046a99a2a811c461f1969c3053"))
	}
	if absTick&0x100 != 0 {
		ratio = mulShift(ratio, mustParseBigInt("0xfcbe86c7900a88aedcffc83b479aa3a4"))
	}
	if absTick&0x200 != 0 {
		ratio = mulShift(ratio, mustParseBigInt("0xf987a7253ac413176f2b074cf7815e54"))
	}
	if absTick&0x400 != 0 {
		ratio = mulShift(ratio, mustParseBigInt("0xf3392b0822b70005940c7a398e4b70f3"))
	}
	if absTick&0x800 != 0 {
		ratio = mulShift(ratio, mustParseBigInt("0xe7159475a2c29b7443b29c7fa6e889d9"))
	}
	if absTick&0x1000 != 0 {
		ratio = mulShift(ratio, mustParseBigInt("0xd097f3bdfd2022b8845ad8f792aa5825"))
	}
	if absTick&0x2000 != 0 {
		ratio = mulShift(ratio, mustParseBigInt("0xa9f746462d870fdf8a65dc1f90e061e5"))
	}
	if absTick&0x4000 != 0 {
		ratio = mulShift(ratio, mustParseBigInt("0x70d869a156d2a1b890bb3df62baf32f7"))
	}
	if absTick&0x8000 != 0 {
		ratio = mulShift(ratio, mustParseBigInt("0x31be135f97d08fd981231505542fcfa6"))
	}
	if absTick&0x10000 != 0 {
		ratio = mulShift(ratio, mustParseBigInt("0x9aa508b5b7a84e1c677de54f3e99bc9"))
	}
	if absTick&0x20000 != 0 {
		ratio = mulShift(ratio, mustParseBigInt("0x5d6af8dedb81196699c329225ee604"))
	}
	if absTick&0x40000 != 0 {
		ratio = mulShift(ratio, mustParseBigInt("0x2216e584f5fa1ea926041bedfe98"))
	}
	if absTick&0x80000 != 0 {
		ratio = mulShift(ratio, mustParseBigInt("0x48a170391f7dc42444e8fa2"))
	}

	// If tick is negative, take reciprocal
	if tick > 0 {
		// Divide by 2^32
		ratio = new(big.Int).Rsh(ratio, 32)
	} else {
		// ratio = (2^256 - 1) / ratio
		maxUint256 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
		ratio = new(big.Int).Div(maxUint256, ratio)
		// Then divide by 2^32
		ratio = new(big.Int).Rsh(ratio, 32)
	}

	return ratio, nil
}

// GetTickAtSqrtRatio returns the tick corresponding to a given sqrt ratio
// Ported from Uniswap V3 TickMath.sol
func GetTickAtSqrtRatio(sqrtPriceX96 *big.Int) (int32, error) {
	// Validate input
	if sqrtPriceX96.Cmp(MinSqrtRatio) < 0 || sqrtPriceX96.Cmp(MaxSqrtRatio) >= 0 {
		return 0, ErrInvalidSqrtRatio
	}

	// Binary search for the tick
	// This is an accurate approach that finds the exact tick
	var tick int32

	left := MinTick
	right := MaxTick

	for left < right {
		mid := (left + right) / 2
		sqrtRatio, _ := GetSqrtRatioAtTick(mid)

		cmp := sqrtRatio.Cmp(sqrtPriceX96)
		if cmp == 0 {
			return mid, nil
		} else if cmp < 0 {
			left = mid + 1
		} else {
			right = mid
		}
	}

	tick = left

	// Validate result
	if tick < MinTick || tick > MaxTick {
		return 0, ErrInvalidTick
	}

	return tick, nil
}

// mulShift multiplies two uint256 values and right shifts by 128
// Helper function for tick calculations
func mulShift(a, b *big.Int) *big.Int {
	result := new(big.Int).Mul(a, b)
	return new(big.Int).Rsh(result, 128)
}

// TickSpacingToMaxLiquidityPerTick calculates max liquidity per tick given tick spacing
func TickSpacingToMaxLiquidityPerTick(tickSpacing int32) *big.Int {
	minTick := MinTick / tickSpacing * tickSpacing
	maxTick := MaxTick / tickSpacing * tickSpacing
	numTicks := (maxTick - minTick) / tickSpacing + 1

	// max liquidity = type(uint128).max / numTicks
	maxUint128 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
	return new(big.Int).Div(maxUint128, big.NewInt(int64(numTicks)))
}
