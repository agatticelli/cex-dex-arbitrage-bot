// Package arbitrage provides CEX-DEX arbitrage detection capabilities.
package arbitrage

import (
	"math/big"

	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/money"
)

// MEVRisk represents the risk level of MEV exploitation.
type MEVRisk int

const (
	MEVRiskLow    MEVRisk = iota // Safe to execute
	MEVRiskMedium               // Proceed with caution
	MEVRiskHigh                 // High probability of sandwich attack
	MEVRiskCritical             // Do not execute
)

// String returns human-readable risk level.
func (r MEVRisk) String() string {
	switch r {
	case MEVRiskLow:
		return "LOW"
	case MEVRiskMedium:
		return "MEDIUM"
	case MEVRiskHigh:
		return "HIGH"
	case MEVRiskCritical:
		return "CRITICAL"
	default:
		return "UNKNOWN"
	}
}

// MEVAnalysis contains the complete MEV risk assessment.
type MEVAnalysis struct {
	// Overall risk level
	Risk MEVRisk

	// Risk score from 0-100 (higher = more risky)
	RiskScore int

	// Individual risk factors
	SandwichRisk     SandwichRiskFactors
	FrontrunningRisk FrontrunningFactors
	GasRisk          GasRiskFactors

	// Recommendations
	Recommendations []string

	// Estimated loss from MEV if executed
	EstimatedMEVLoss money.USD
}

// SandwichRiskFactors assesses sandwich attack vulnerability.
type SandwichRiskFactors struct {
	// Trade size as percentage of pool liquidity
	TradeSizePctOfLiquidity float64

	// Expected price impact from our trade
	PriceImpactBPS money.BPS

	// Profit margin available for sandwich (our spread - their costs)
	SandwichProfitMarginBPS money.BPS

	// Is trade size large enough to be worth attacking?
	IsAttractiveTarget bool
}

// FrontrunningFactors assesses frontrunning vulnerability.
type FrontrunningFactors struct {
	// Our gas price relative to recent blocks
	GasPricePercentile int

	// Is our opportunity visible in mempool?
	MempoolVisible bool

	// Time since last block (more time = more risk)
	TimeSinceBlockMs int64

	// Are there known MEV bots active on this pair?
	KnownBotActivity bool
}

// GasRiskFactors assesses gas-related MEV risks.
type GasRiskFactors struct {
	// Current gas price in gwei
	CurrentGasGwei float64

	// Gas price spike (vs 5-block average)
	GasSpikePct float64

	// Is gas unusually high (indicating MEV activity)?
	IsGasSpiking bool

	// Estimated priority fee needed to avoid frontrunning
	RecommendedPriorityFee money.Gwei
}

// MEVAnalyzer assesses MEV risks for arbitrage opportunities.
type MEVAnalyzer struct {
	// Thresholds for risk assessment
	sandwichThresholdBPS   money.BPS // Below this, sandwich attack not profitable
	largeTradePctThreshold float64   // Trade size % that attracts MEV bots
	gasSpikePctThreshold   float64   // Gas increase % indicating MEV activity
}

// NewMEVAnalyzer creates an MEV analyzer with default thresholds.
func NewMEVAnalyzer() *MEVAnalyzer {
	return &MEVAnalyzer{
		sandwichThresholdBPS:   money.NewBPSFromInt(20), // 0.2% minimum for sandwich to be profitable
		largeTradePctThreshold: 1.0,                     // 1% of pool liquidity attracts attention
		gasSpikePctThreshold:   50.0,                    // 50% gas increase = MEV activity
	}
}

// Analyze performs comprehensive MEV risk assessment.
func (m *MEVAnalyzer) Analyze(params MEVAnalysisParams) MEVAnalysis {
	analysis := MEVAnalysis{}

	// Calculate sandwich attack risk
	analysis.SandwichRisk = m.assessSandwichRisk(params)

	// Calculate frontrunning risk
	analysis.FrontrunningRisk = m.assessFrontrunningRisk(params)

	// Calculate gas-related risks
	analysis.GasRisk = m.assessGasRisk(params)

	// Calculate overall risk score (0-100)
	analysis.RiskScore = m.calculateRiskScore(analysis)

	// Determine risk level
	analysis.Risk = m.riskLevelFromScore(analysis.RiskScore)

	// Generate recommendations
	analysis.Recommendations = m.generateRecommendations(analysis, params)

	// Estimate potential MEV loss
	analysis.EstimatedMEVLoss = m.estimateMEVLoss(analysis, params)

	return analysis
}

// MEVAnalysisParams contains input parameters for MEV analysis.
type MEVAnalysisParams struct {
	// Trade details
	TradeSize      *big.Int  // In base token raw units
	PriceImpactBPS money.BPS // Expected price impact
	SpreadBPS      money.BPS // Our profit spread

	// Pool state
	PoolLiquidity *big.Int // Total pool liquidity
	PoolFeeBPS    money.BPS // Pool fee tier

	// Gas state
	CurrentGasGwei  float64 // Current gas price
	AvgGasGwei      float64 // 5-block average gas
	TimeSinceBlock  int64   // Milliseconds since last block
	BlockBaseFee    *big.Int // EIP-1559 base fee

	// Context
	TradeValueUSD money.USD // USD value of trade
}

func (m *MEVAnalyzer) assessSandwichRisk(params MEVAnalysisParams) SandwichRiskFactors {
	factors := SandwichRiskFactors{}

	// Calculate trade size as % of pool liquidity
	if params.PoolLiquidity != nil && params.PoolLiquidity.Sign() > 0 && params.TradeSize != nil {
		tradeSizeFloat := new(big.Float).SetInt(params.TradeSize)
		liquidityFloat := new(big.Float).SetInt(params.PoolLiquidity)
		pctFloat := new(big.Float).Quo(tradeSizeFloat, liquidityFloat)
		pctFloat.Mul(pctFloat, big.NewFloat(100))
		factors.TradeSizePctOfLiquidity, _ = pctFloat.Float64()
	}

	factors.PriceImpactBPS = params.PriceImpactBPS

	// Sandwich profit = our spread - (2 * pool fees) - gas costs
	// If positive, we're an attractive target
	sandwichCosts := params.PoolFeeBPS.Add(params.PoolFeeBPS) // 2x fees for front+back
	factors.SandwichProfitMarginBPS = params.SpreadBPS.Sub(sandwichCosts)

	// Is trade large enough to be worth attacking?
	// Typically, MEV bots need > $1000 profit to cover gas
	minProfitableTradeUSD := money.NewUSD(10000) // $10k trade minimum
	factors.IsAttractiveTarget = params.TradeValueUSD.GreaterThan(minProfitableTradeUSD) &&
		factors.SandwichProfitMarginBPS.GreaterThan(m.sandwichThresholdBPS)

	return factors
}

func (m *MEVAnalyzer) assessFrontrunningRisk(params MEVAnalysisParams) FrontrunningFactors {
	factors := FrontrunningFactors{}

	// Calculate gas percentile (rough estimate)
	// If our gas is below average, we're easy to frontrun
	if params.AvgGasGwei > 0 {
		gasPct := (params.CurrentGasGwei / params.AvgGasGwei) * 100
		factors.GasPricePercentile = int(gasPct)
	}

	// Public mempool = always visible (unless using private relay)
	factors.MempoolVisible = true

	factors.TimeSinceBlockMs = params.TimeSinceBlock

	// Longer time since block = more MEV competition
	// If > 6 seconds (half a block), risk increases
	factors.KnownBotActivity = params.TimeSinceBlock > 6000

	return factors
}

func (m *MEVAnalyzer) assessGasRisk(params MEVAnalysisParams) GasRiskFactors {
	factors := GasRiskFactors{
		CurrentGasGwei: params.CurrentGasGwei,
	}

	// Calculate gas spike
	if params.AvgGasGwei > 0 {
		factors.GasSpikePct = ((params.CurrentGasGwei - params.AvgGasGwei) / params.AvgGasGwei) * 100
		factors.IsGasSpiking = factors.GasSpikePct > m.gasSpikePctThreshold
	}

	// Recommend priority fee to stay competitive
	// Add 20% above current gas to avoid frontrunning
	factors.RecommendedPriorityFee = money.NewGwei(params.CurrentGasGwei * 1.2)

	return factors
}

func (m *MEVAnalyzer) calculateRiskScore(analysis MEVAnalysis) int {
	score := 0

	// Sandwich risk contribution (0-40 points)
	if analysis.SandwichRisk.IsAttractiveTarget {
		score += 25
		if analysis.SandwichRisk.TradeSizePctOfLiquidity > m.largeTradePctThreshold {
			score += 15
		}
	}

	// Frontrunning risk contribution (0-30 points)
	if analysis.FrontrunningRisk.MempoolVisible {
		score += 10
	}
	if analysis.FrontrunningRisk.GasPricePercentile < 50 {
		score += 10 // Below median gas = easy to frontrun
	}
	if analysis.FrontrunningRisk.KnownBotActivity {
		score += 10
	}

	// Gas risk contribution (0-30 points)
	if analysis.GasRisk.IsGasSpiking {
		score += 20 // Gas spike = MEV bots active
	}
	if analysis.GasRisk.GasSpikePct > 100 {
		score += 10 // Extreme spike = intense MEV competition
	}

	if score > 100 {
		score = 100
	}
	return score
}

func (m *MEVAnalyzer) riskLevelFromScore(score int) MEVRisk {
	switch {
	case score < 25:
		return MEVRiskLow
	case score < 50:
		return MEVRiskMedium
	case score < 75:
		return MEVRiskHigh
	default:
		return MEVRiskCritical
	}
}

func (m *MEVAnalyzer) generateRecommendations(analysis MEVAnalysis, params MEVAnalysisParams) []string {
	var recs []string

	if analysis.Risk >= MEVRiskHigh {
		recs = append(recs, "Consider using Flashbots Protect for private submission")
	}

	if analysis.SandwichRisk.IsAttractiveTarget {
		recs = append(recs, "Split trade into smaller chunks to reduce sandwich profitability")
	}

	if analysis.FrontrunningRisk.GasPricePercentile < 50 {
		recs = append(recs,
			"Increase gas price to "+analysis.GasRisk.RecommendedPriorityFee.String()+" to reduce frontrunning risk")
	}

	if analysis.GasRisk.IsGasSpiking {
		recs = append(recs, "High MEV activity detected - delay execution or use private relay")
	}

	if analysis.SandwichRisk.TradeSizePctOfLiquidity > 2.0 {
		recs = append(recs, "Trade size may cause excessive slippage - reduce size or find deeper pools")
	}

	if len(recs) == 0 {
		recs = append(recs, "Trade appears safe for standard execution")
	}

	return recs
}

func (m *MEVAnalyzer) estimateMEVLoss(analysis MEVAnalysis, params MEVAnalysisParams) money.USD {
	if !analysis.SandwichRisk.IsAttractiveTarget {
		return money.Zero()
	}

	// Estimate MEV loss as: trade value * sandwich profit margin
	// This is what a sandwich attacker could extract
	marginBPS := analysis.SandwichRisk.SandwichProfitMarginBPS
	if marginBPS < 0 {
		return money.Zero()
	}

	return params.TradeValueUSD.MulBPS(marginBPS)
}

// IsSafeToExecute returns true if the MEV risk is acceptable.
func (a MEVAnalysis) IsSafeToExecute() bool {
	return a.Risk <= MEVRiskMedium
}

// ShouldUsePrivateRelay returns true if a private relay is recommended.
func (a MEVAnalysis) ShouldUsePrivateRelay() bool {
	return a.Risk >= MEVRiskHigh
}
