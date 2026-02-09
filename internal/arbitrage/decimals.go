package arbitrage

import "math/big"

// rawToFloat converts a raw integer amount into a human-readable float using decimals.
func rawToFloat(raw *big.Int, decimals int) *big.Float {
	if raw == nil {
		return big.NewFloat(0)
	}
	if decimals < 0 {
		decimals = 0
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	val := new(big.Float).SetInt(raw)
	return new(big.Float).Quo(val, new(big.Float).SetInt(scale))
}

// floatToRaw converts a human-readable float into a raw integer amount using decimals.
func floatToRaw(amount float64, decimals int) *big.Int {
	if decimals < 0 {
		decimals = 0
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	scaled := new(big.Float).Mul(big.NewFloat(amount), new(big.Float).SetInt(scale))
	raw := new(big.Int)
	scaled.Int(raw)
	return raw
}
