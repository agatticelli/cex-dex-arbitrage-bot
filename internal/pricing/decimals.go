package pricing

import "math/big"

// pow10 returns 10^decimals as *big.Int
func pow10(decimals int) *big.Int {
	if decimals < 0 {
		decimals = 0
	}
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
}

// rawToFloat converts a raw integer amount into a human-readable float using decimals.
func rawToFloat(raw *big.Int, decimals int) *big.Float {
	if raw == nil {
		return big.NewFloat(0)
	}
	val := new(big.Float).SetInt(raw)
	scale := new(big.Float).SetInt(pow10(decimals))
	return new(big.Float).Quo(val, scale)
}

// floatToRaw converts a human-readable float into a raw integer amount using decimals.
func floatToRaw(val *big.Float, decimals int) *big.Int {
	if val == nil {
		return big.NewInt(0)
	}
	scaled := new(big.Float).Mul(val, new(big.Float).SetInt(pow10(decimals)))
	raw := new(big.Int)
	scaled.Int(raw)
	return raw
}
