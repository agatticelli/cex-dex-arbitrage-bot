package uniswapv3

import (
	"math/big"
)

// NextInitializedTickWithinOneWord finds the next initialized tick in the same word
// or the next word. This is used during tick-walking to efficiently find liquidity changes.
//
// Ported from Uniswap V3 TickBitmap.sol
func NextInitializedTickWithinOneWord(
	tick int32,
	tickSpacing int32,
	lte bool,
	tickBitmap *big.Int,
) (int32, bool) {
	compressed := tick / tickSpacing
	if tick < 0 && tick%tickSpacing != 0 {
		compressed-- // round towards negative infinity
	}

	if lte {
		// Find next initialized tick to the left (lower tick)
		wordPos := int16(compressed >> 8)
		bitPos := uint8(compressed % 256)

		// Mask off bits to the right (higher bits)
		mask := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(bitPos+1)), big.NewInt(1))
		masked := new(big.Int).And(tickBitmap, mask)

		// If there's an initialized tick in this word
		initialized := masked.Cmp(big.NewInt(0)) != 0

		if initialized {
			// Find most significant bit (rightmost initialized tick)
			msb := mostSignificantBit(masked)
			next := (int32(wordPos) * 256 + int32(msb)) * tickSpacing
			return next, true
		} else {
			// No initialized tick in this word to the left
			next := (int32(wordPos) * 256) * tickSpacing
			return next - tickSpacing, false
		}
	} else {
		// Find next initialized tick to the right (higher tick)
		wordPos := int16((compressed + 1) >> 8)
		bitPos := uint8((compressed + 1) % 256)

		// Mask off bits to the left (lower bits)
		mask := new(big.Int).Sub(
			new(big.Int).Lsh(big.NewInt(1), 256),
			new(big.Int).Lsh(big.NewInt(1), uint(bitPos)),
		)
		masked := new(big.Int).And(tickBitmap, mask)

		// If there's an initialized tick in this word
		initialized := masked.Cmp(big.NewInt(0)) != 0

		if initialized {
			// Find least significant bit (leftmost initialized tick)
			lsb := leastSignificantBit(masked)
			next := (int32(wordPos) * 256 + int32(lsb)) * tickSpacing
			return next, true
		} else {
			// No initialized tick in this word to the right
			next := (int32(wordPos+1) * 256) * tickSpacing
			return next, false
		}
	}
}

// mostSignificantBit returns the index of the most significant bit (MSB)
// This is used to find the rightmost (highest) initialized tick in a word
func mostSignificantBit(x *big.Int) uint8 {
	if x.Cmp(big.NewInt(0)) == 0 {
		return 0
	}

	// Find the position of the MSB
	bitLen := x.BitLen()
	return uint8(bitLen - 1)
}

// leastSignificantBit returns the index of the least significant bit (LSB)
// This is used to find the leftmost (lowest) initialized tick in a word
func leastSignificantBit(x *big.Int) uint8 {
	if x.Cmp(big.NewInt(0)) == 0 {
		return 0
	}

	// Find the position of the LSB by testing each bit
	for i := 0; i < 256; i++ {
		if x.Bit(i) == 1 {
			return uint8(i)
		}
	}

	return 0
}

// TickToWordPosition converts a tick to its word position in the bitmap
func TickToWordPosition(tick int32, tickSpacing int32) int16 {
	compressed := tick / tickSpacing
	if tick < 0 && tick%tickSpacing != 0 {
		compressed--
	}
	return int16(compressed >> 8)
}
