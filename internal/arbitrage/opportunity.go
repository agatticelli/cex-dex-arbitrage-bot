package arbitrage

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/pricing"
)

// Direction represents the direction of an arbitrage opportunity
type Direction int

const (
	// CEXToDEX represents buying on CEX and selling on DEX
	CEXToDEX Direction = iota
	// DEXToCEX represents buying on DEX and selling on CEX
	DEXToCEX
)

// String returns string representation of direction
func (d Direction) String() string {
	if d == CEXToDEX {
		return "CEX → DEX"
	}
	return "DEX → CEX"
}

// MarshalJSON implements JSON marshaling for Direction
func (d Direction) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

// Opportunity represents an arbitrage opportunity
type Opportunity struct {
	OpportunityID  string     `json:"opportunity_id"`
	BlockNumber    uint64     `json:"block_number"`
	Timestamp      int64      `json:"timestamp"`
	Direction      Direction  `json:"direction"`
	TradingPair    string     `json:"trading_pair"` // "ETH-USDC", "BTC-USDC", etc.
	BaseSymbol     string     `json:"base_symbol"`
	QuoteSymbol    string     `json:"quote_symbol"`
	TradeSizeRaw   *big.Int   `json:"trade_size_raw"`
	TradeSizeBase  *big.Float `json:"trade_size_base"`
	CEXPrice       *big.Float `json:"cex_price"`
	DEXPrice       *big.Float `json:"dex_price"`
	PriceDiff      *big.Float `json:"price_diff"`
	PriceDiffPct   *big.Float `json:"price_diff_pct"`
	GrossProfitUSD *big.Float `json:"gross_profit_usd"`
	GasCostWei     *big.Int   `json:"gas_cost_wei"`
	GasCostUSD     *big.Float `json:"gas_cost_usd"`
	TradingFees    *big.Float `json:"trading_fees"`
	NetProfitUSD   *big.Float `json:"net_profit_usd"`
	ProfitPct      *big.Float `json:"profit_pct"`
	ExecutionSteps []string   `json:"execution_steps"`
	RiskFactors    []string   `json:"risk_factors"`
}

// NewOpportunity creates a new arbitrage opportunity
func NewOpportunity(blockNumber uint64, direction Direction, tradeSize *big.Int) *Opportunity {
	return &Opportunity{
		OpportunityID:  fmt.Sprintf("block-%d-%d-%s", blockNumber, time.Now().UnixNano(), direction.String()),
		BlockNumber:    blockNumber,
		Timestamp:      time.Now().Unix(),
		Direction:      direction,
		TradeSizeRaw:   tradeSize,
		ExecutionSteps: make([]string, 0),
		RiskFactors:    make([]string, 0),
	}
}

// SetBaseInfo sets base/quote metadata and computes normalized trade size.
func (o *Opportunity) SetBaseInfo(baseSymbol, quoteSymbol string, baseDecimals int) {
	o.BaseSymbol = baseSymbol
	o.QuoteSymbol = quoteSymbol
	o.TradeSizeBase = pricing.RawToFloat(o.TradeSizeRaw, baseDecimals)
}

// SetPrices sets the CEX and DEX prices and calculates price differences
func (o *Opportunity) SetPrices(cexPrice, dexPrice *big.Float) {
	o.CEXPrice = cexPrice
	o.DEXPrice = dexPrice

	// Calculate price difference
	o.PriceDiff = new(big.Float).Sub(dexPrice, cexPrice)

	// Calculate price difference percentage
	o.PriceDiffPct = new(big.Float).Quo(o.PriceDiff, cexPrice)
	o.PriceDiffPct.Mul(o.PriceDiffPct, big.NewFloat(100))
}

// SetProfitMetrics sets the profit-related metrics
func (o *Opportunity) SetProfitMetrics(grossProfit, netProfit, profitPct *big.Float) {
	o.GrossProfitUSD = grossProfit
	o.NetProfitUSD = netProfit
	o.ProfitPct = profitPct
}

// SetCosts sets the cost-related metrics
func (o *Opportunity) SetCosts(gasCostWei *big.Int, gasCostUSD, tradingFees *big.Float) {
	if gasCostWei == nil {
		gasCostWei = big.NewInt(0)
	}
	if gasCostUSD == nil {
		gasCostUSD = big.NewFloat(0)
	}
	if tradingFees == nil {
		tradingFees = big.NewFloat(0)
	}
	o.GasCostWei = gasCostWei
	o.GasCostUSD = gasCostUSD
	o.TradingFees = tradingFees
}

// AddExecutionStep adds a step to the execution plan
func (o *Opportunity) AddExecutionStep(step string) {
	o.ExecutionSteps = append(o.ExecutionSteps, step)
}

// AddRiskFactor adds a risk factor to consider
func (o *Opportunity) AddRiskFactor(risk string) {
	o.RiskFactors = append(o.RiskFactors, risk)
}

// IsProfitable returns whether this opportunity is profitable (net profit > 0)
func (o *Opportunity) IsProfitable() bool {
	return o.NetProfitUSD != nil && o.NetProfitUSD.Cmp(big.NewFloat(0)) > 0
}

// FormatOutput formats the opportunity for console output
func (o *Opportunity) FormatOutput() string {
	var sb strings.Builder

	sb.WriteString("\n")
	sb.WriteString("═══════════════════════════════════════════════════════════════\n")
	sb.WriteString("           ARBITRAGE OPPORTUNITY DETECTED\n")
	sb.WriteString("═══════════════════════════════════════════════════════════════\n")
	sb.WriteString("\n")

	// Basic information
	sb.WriteString(fmt.Sprintf("Opportunity ID:  %s\n", o.OpportunityID))
	sb.WriteString(fmt.Sprintf("Block Number:    %d\n", o.BlockNumber))
	sb.WriteString(fmt.Sprintf("Timestamp:       %s\n", time.Unix(o.Timestamp, 0).Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("Trading Pair:    %s\n", o.TradingPair))
	sb.WriteString(fmt.Sprintf("Direction:       %s\n", o.Direction.String()))
	sb.WriteString("\n")

	// Trade details
	sb.WriteString("─────────────────────────────────────────────────────────────────\n")
	sb.WriteString("TRADE DETAILS\n")
	sb.WriteString("─────────────────────────────────────────────────────────────────\n")
	baseSymbol := o.BaseSymbol
	if baseSymbol == "" {
		baseSymbol = "BASE"
	}
	sb.WriteString(fmt.Sprintf("Trade Size:      %s %s (%s raw units)\n",
		formatBigFloat(o.TradeSizeBase, 4),
		baseSymbol,
		o.TradeSizeRaw.String()))
	sb.WriteString(fmt.Sprintf("CEX Price:       $%s\n", formatBigFloat(o.CEXPrice, 2)))
	sb.WriteString(fmt.Sprintf("DEX Price:       $%s\n", formatBigFloat(o.DEXPrice, 2)))
	sb.WriteString(fmt.Sprintf("Price Diff:      $%s (%s%%)\n",
		formatBigFloat(o.PriceDiff, 2),
		formatBigFloat(o.PriceDiffPct, 4)))
	sb.WriteString("\n")

	// Profit analysis
	sb.WriteString("─────────────────────────────────────────────────────────────────\n")
	sb.WriteString("PROFIT ANALYSIS\n")
	sb.WriteString("─────────────────────────────────────────────────────────────────\n")
	sb.WriteString(fmt.Sprintf("Gross Profit:    $%s (after $%s in trading fees)\n",
		formatBigFloat(o.GrossProfitUSD, 2),
		formatBigFloat(o.TradingFees, 2)))
	sb.WriteString(fmt.Sprintf("Gas Cost:        %s wei ($%s)\n",
		o.GasCostWei.String(),
		formatBigFloat(o.GasCostUSD, 2)))
	sb.WriteString(fmt.Sprintf("Net Profit:      $%s (%s%%)\n",
		formatBigFloat(o.NetProfitUSD, 2),
		formatBigFloat(o.ProfitPct, 4)))
	sb.WriteString("\n")

	// Execution steps
	if len(o.ExecutionSteps) > 0 {
		sb.WriteString("─────────────────────────────────────────────────────────────────\n")
		sb.WriteString("EXECUTION STEPS\n")
		sb.WriteString("─────────────────────────────────────────────────────────────────\n")
		for i, step := range o.ExecutionSteps {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, step))
		}
		sb.WriteString("\n")
	}

	// Risk factors
	if len(o.RiskFactors) > 0 {
		sb.WriteString("─────────────────────────────────────────────────────────────────\n")
		sb.WriteString("RISK FACTORS\n")
		sb.WriteString("─────────────────────────────────────────────────────────────────\n")
		for _, risk := range o.RiskFactors {
			sb.WriteString(fmt.Sprintf("[!] %s\n", risk))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("═══════════════════════════════════════════════════════════════\n")

	return sb.String()
}

// ToJSON converts opportunity to JSON
func (o *Opportunity) ToJSON() ([]byte, error) {
	// Create a serializable version
	data := map[string]interface{}{
		"opportunity_id":   o.OpportunityID,
		"block_number":     o.BlockNumber,
		"timestamp":        o.Timestamp,
		"direction":        o.Direction.String(),
		"trading_pair":     o.TradingPair,
		"base_symbol":      o.BaseSymbol,
		"quote_symbol":     o.QuoteSymbol,
		"trade_size_raw":   o.TradeSizeRaw.String(),
		"trade_size_base":  formatBigFloat(o.TradeSizeBase, 4),
		"cex_price":        formatBigFloat(o.CEXPrice, 2),
		"dex_price":        formatBigFloat(o.DEXPrice, 2),
		"price_diff":       formatBigFloat(o.PriceDiff, 2),
		"price_diff_pct":   formatBigFloat(o.PriceDiffPct, 4),
		"gross_profit_usd": formatBigFloat(o.GrossProfitUSD, 2),
		"gas_cost_wei":     o.GasCostWei.String(),
		"gas_cost_usd":     formatBigFloat(o.GasCostUSD, 2),
		"trading_fees":     formatBigFloat(o.TradingFees, 2),
		"net_profit_usd":   formatBigFloat(o.NetProfitUSD, 2),
		"profit_pct":       formatBigFloat(o.ProfitPct, 4),
		"execution_steps":  o.ExecutionSteps,
		"risk_factors":     o.RiskFactors,
	}

	return json.Marshal(data)
}

// formatBigFloat formats a big.Float for display
func formatBigFloat(f *big.Float, precision int) string {
	if f == nil {
		return "0.00"
	}
	return f.Text('f', precision)
}

// SerializableOpportunity is a JSON-serializable version of Opportunity
// Used for SNS/SQS messaging and Lambda processing
type SerializableOpportunity struct {
	OpportunityID  string   `json:"opportunity_id"`
	BlockNumber    uint64   `json:"block_number"`
	Timestamp      int64    `json:"timestamp"`
	Direction      string   `json:"direction"`
	TradingPair    string   `json:"trading_pair"`
	BaseSymbol     string   `json:"base_symbol"`
	QuoteSymbol    string   `json:"quote_symbol"`
	TradeSizeRaw   string   `json:"trade_size_raw"`
	TradeSizeBase  string   `json:"trade_size_base"`
	CEXPrice       string   `json:"cex_price"`
	DEXPrice       string   `json:"dex_price"`
	PriceDiff      string   `json:"price_diff"`
	PriceDiffPct   string   `json:"price_diff_pct"`
	GrossProfitUSD string   `json:"gross_profit_usd"`
	GasCostWei     string   `json:"gas_cost_wei"`
	GasCostUSD     string   `json:"gas_cost_usd"`
	TradingFees    string   `json:"trading_fees"`
	NetProfitUSD   string   `json:"net_profit_usd"`
	ProfitPct      string   `json:"profit_pct"`
	ExecutionSteps []string `json:"execution_steps"`
	RiskFactors    []string `json:"risk_factors"`
}

// ToSerializable converts Opportunity to SerializableOpportunity
func (o *Opportunity) ToSerializable() *SerializableOpportunity {
	return &SerializableOpportunity{
		OpportunityID:  o.OpportunityID,
		BlockNumber:    o.BlockNumber,
		Timestamp:      o.Timestamp,
		Direction:      o.Direction.String(),
		TradingPair:    o.TradingPair,
		BaseSymbol:     o.BaseSymbol,
		QuoteSymbol:    o.QuoteSymbol,
		TradeSizeRaw:   o.TradeSizeRaw.String(),
		TradeSizeBase:  formatBigFloat(o.TradeSizeBase, 4),
		CEXPrice:       formatBigFloat(o.CEXPrice, 2),
		DEXPrice:       formatBigFloat(o.DEXPrice, 2),
		PriceDiff:      formatBigFloat(o.PriceDiff, 2),
		PriceDiffPct:   formatBigFloat(o.PriceDiffPct, 4),
		GrossProfitUSD: formatBigFloat(o.GrossProfitUSD, 2),
		GasCostWei:     o.GasCostWei.String(),
		GasCostUSD:     formatBigFloat(o.GasCostUSD, 2),
		TradingFees:    formatBigFloat(o.TradingFees, 2),
		NetProfitUSD:   formatBigFloat(o.NetProfitUSD, 2),
		ProfitPct:      formatBigFloat(o.ProfitPct, 4),
		ExecutionSteps: o.ExecutionSteps,
		RiskFactors:    o.RiskFactors,
	}
}

// OpportunitySummary provides a compact summary of the opportunity
type OpportunitySummary struct {
	ID         string `json:"id"`
	Block      uint64 `json:"block"`
	Direction  string `json:"direction"`
	ProfitUSD  string `json:"profit_usd"`
	ProfitPct  string `json:"profit_pct"`
	Profitable bool   `json:"profitable"`
}

// ToSummary creates a compact summary of the opportunity
func (o *Opportunity) ToSummary() *OpportunitySummary {
	return &OpportunitySummary{
		ID:         o.OpportunityID,
		Block:      o.BlockNumber,
		Direction:  o.Direction.String(),
		ProfitUSD:  formatBigFloat(o.NetProfitUSD, 2),
		ProfitPct:  formatBigFloat(o.ProfitPct, 4),
		Profitable: o.IsProfitable(),
	}
}
