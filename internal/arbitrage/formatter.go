package arbitrage

import (
	"fmt"
)

// FormatCompactOutput returns a single-line summary for logging
// Useful for compact log output or monitoring dashboards
func (o *Opportunity) FormatCompactOutput() string {
	idShort := o.OpportunityID
	if len(idShort) > 16 {
		idShort = idShort[:16]
	}
	return fmt.Sprintf("[%s] %s %s: %.4f %s | CEX: $%s | DEX: $%s | Net: $%s (%.2f%%)",
		idShort,
		o.Direction.String(),
		o.TradingPair,
		o.TradeSizeBaseFloat(),
		o.BaseSymbol,
		o.CEXPrice.Text('f', 2),
		o.DEXPrice.Text('f', 2),
		o.NetProfitUSD.Text('f', 2),
		o.ProfitPctFloat(),
	)
}

// TradeSizeBaseFloat returns trade size as float64
func (o *Opportunity) TradeSizeBaseFloat() float64 {
	if o.TradeSizeBase == nil {
		return 0
	}
	f, _ := o.TradeSizeBase.Float64()
	return f
}

// PriceDiffPctFloat returns price difference percentage as float64
func (o *Opportunity) PriceDiffPctFloat() float64 {
	if o.PriceDiffPct == nil {
		return 0
	}
	f, _ := o.PriceDiffPct.Float64()
	return f
}

// ProfitPctFloat returns profit percentage as float64
func (o *Opportunity) ProfitPctFloat() float64 {
	if o.ProfitPct == nil {
		return 0
	}
	f, _ := o.ProfitPct.Float64()
	return f
}
