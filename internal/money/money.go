// Package money provides fixed-point arithmetic for financial calculations.
// Uses int64 with fixed scaling to avoid floating-point precision issues.
// Zero allocations, no GC pressure - optimized for hot paths.
package money

import (
	"fmt"
	"math"
)

// Scale factors for different precisions
const (
	USDScale int64 = 100       // 2 decimals: $1.00 = 100
	BPSScale int64 = 10000     // basis points: 100% = 10000
	WeiScale int64 = 1e9       // gwei to wei
	MaxUSD   int64 = math.MaxInt64 / USDScale
)

// USD represents US dollars in cents (2 decimal places).
// Max value: ~$92 quadrillion (more than enough for crypto).
type USD int64

// BPS represents basis points (1 bps = 0.01% = 0.0001).
type BPS int64

// Gwei represents gas price in gwei.
type Gwei int64

// --- USD Constructors ---

// NewUSD creates USD from a dollar amount.
func NewUSD(dollars float64) USD {
	return USD(dollars * float64(USDScale))
}

// NewUSDFromCents creates USD from cents.
func NewUSDFromCents(cents int64) USD {
	return USD(cents)
}

// Zero returns zero USD.
func Zero() USD {
	return USD(0)
}

// --- USD Arithmetic ---

// Add returns a + b.
func (a USD) Add(b USD) USD {
	return a + b
}

// Sub returns a - b. Can be negative (loss).
func (a USD) Sub(b USD) USD {
	return a - b
}

// Mul multiplies by an integer factor.
func (a USD) Mul(factor int64) USD {
	return USD(int64(a) * factor)
}

// Div divides by an integer divisor.
func (a USD) Div(divisor int64) USD {
	if divisor == 0 {
		return 0
	}
	return USD(int64(a) / divisor)
}

// MulBPS multiplies USD by basis points and returns USD.
// Example: $100.MulBPS(50) = $0.50 (0.5%)
func (a USD) MulBPS(bps BPS) USD {
	return USD((int64(a) * int64(bps)) / BPSScale)
}

// MulFloat multiplies by a float (use sparingly, only at boundaries).
func (a USD) MulFloat(f float64) USD {
	return USD(float64(a) * f)
}

// Abs returns absolute value.
func (a USD) Abs() USD {
	if a < 0 {
		return -a
	}
	return a
}

// --- USD Comparison ---

// IsPositive returns true if > 0.
func (a USD) IsPositive() bool {
	return a > 0
}

// IsNegative returns true if < 0.
func (a USD) IsNegative() bool {
	return a < 0
}

// IsZero returns true if == 0.
func (a USD) IsZero() bool {
	return a == 0
}

// GreaterThan returns a > b.
func (a USD) GreaterThan(b USD) bool {
	return a > b
}

// LessThan returns a < b.
func (a USD) LessThan(b USD) bool {
	return a < b
}

// --- USD Conversion (Boundary functions - use for display only) ---

// Float64 converts to float64 for display.
func (a USD) Float64() float64 {
	return float64(a) / float64(USDScale)
}

// Cents returns the raw cent value.
func (a USD) Cents() int64 {
	return int64(a)
}

// String returns formatted string like "$123.45" or "-$45.00".
func (a USD) String() string {
	if a < 0 {
		return fmt.Sprintf("-$%.2f", float64(-a)/float64(USDScale))
	}
	return fmt.Sprintf("$%.2f", float64(a)/float64(USDScale))
}

// --- BPS Constructors ---

// NewBPS creates BPS from a percentage (e.g., 0.5 for 0.5% = 50 bps).
func NewBPS(percent float64) BPS {
	return BPS(percent * 100)
}

// NewBPSFromInt creates BPS directly from basis points.
func NewBPSFromInt(bps int64) BPS {
	return BPS(bps)
}

// --- BPS Arithmetic ---

// Add returns a + b.
func (a BPS) Add(b BPS) BPS {
	return a + b
}

// Sub returns a - b.
func (a BPS) Sub(b BPS) BPS {
	return a - b
}

// Abs returns absolute value.
func (a BPS) Abs() BPS {
	if a < 0 {
		return -a
	}
	return a
}

// --- BPS Comparison ---

// IsPositive returns true if > 0.
func (a BPS) IsPositive() bool {
	return a > 0
}

// GreaterThan returns a > b.
func (a BPS) GreaterThan(b BPS) bool {
	return a > b
}

// --- BPS Conversion ---

// Float64 returns the percentage as float (e.g., 50 bps = 0.5).
func (a BPS) Float64() float64 {
	return float64(a) / 100.0
}

// Percent returns as percentage string (e.g., "0.50%").
func (a BPS) Percent() string {
	return fmt.Sprintf("%.2f%%", float64(a)/100.0)
}

// String returns basis points as string (e.g., "50 bps").
func (a BPS) String() string {
	return fmt.Sprintf("%d bps", a)
}

// Int64 returns raw basis points value.
func (a BPS) Int64() int64 {
	return int64(a)
}

// --- Gwei ---

// NewGwei creates Gwei from a float.
func NewGwei(gwei float64) Gwei {
	return Gwei(gwei)
}

// ToWei converts gwei to wei.
func (g Gwei) ToWei() int64 {
	return int64(g) * WeiScale
}

// Float64 returns gwei as float.
func (g Gwei) Float64() float64 {
	return float64(g)
}

// String returns formatted string.
func (g Gwei) String() string {
	return fmt.Sprintf("%.1f gwei", float64(g))
}

// --- Utility Functions ---

// CalculateSpreadBPS calculates spread in basis points between two prices.
// Returns (dexPrice - cexPrice) / cexPrice * 10000.
func CalculateSpreadBPS(cexPrice, dexPrice USD) BPS {
	if cexPrice == 0 {
		return 0
	}
	// (dex - cex) * 10000 / cex
	diff := int64(dexPrice) - int64(cexPrice)
	return BPS((diff * BPSScale) / int64(cexPrice))
}

// CalculateProfit calculates net profit after costs.
func CalculateProfit(gross, gasCost, exchangeFees USD) USD {
	return gross.Sub(gasCost).Sub(exchangeFees)
}

// CalculateFees calculates exchange fees from trade value.
// totalFeeBPS is the combined fee rate (e.g., 40 bps = 0.4%).
func CalculateFees(tradeValue USD, totalFeeBPS BPS) USD {
	return tradeValue.MulBPS(totalFeeBPS)
}

// Min returns the minimum of two USD values.
func Min(a, b USD) USD {
	if a < b {
		return a
	}
	return b
}

// Max returns the maximum of two USD values.
func Max(a, b USD) USD {
	if a > b {
		return a
	}
	return b
}
