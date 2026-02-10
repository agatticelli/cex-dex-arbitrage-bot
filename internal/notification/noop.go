package notification

import (
	"context"

	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/arbitrage"
	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/observability"
)

// NoOpPublisher is a publisher that does nothing but log opportunities.
// Use this when SNS is not configured (local development, testing).
type NoOpPublisher struct {
	logger *observability.Logger
}

// NewNoOpPublisher creates a new no-op publisher that only logs opportunities.
func NewNoOpPublisher(logger *observability.Logger) *NoOpPublisher {
	return &NoOpPublisher{
		logger: logger,
	}
}

// PublishOpportunity logs the opportunity instead of publishing to SNS.
// Implements arbitrage.NotificationPublisher interface.
func (p *NoOpPublisher) PublishOpportunity(ctx context.Context, opp *arbitrage.Opportunity) error {
	if p.logger != nil {
		netProfit := float64(0)
		if opp.NetProfitUSD != nil {
			netProfit, _ = opp.NetProfitUSD.Float64()
		}

		profitPct := float64(0)
		if opp.ProfitPct != nil {
			profitPct, _ = opp.ProfitPct.Float64()
		}

		p.logger.Info("opportunity detected (SNS disabled)",
			"opportunity_id", opp.OpportunityID,
			"block", opp.BlockNumber,
			"pair", opp.TradingPair,
			"direction", opp.Direction.String(),
			"profitable", opp.IsProfitable(),
			"net_profit_usd", netProfit,
			"profit_pct", profitPct,
		)
	}
	return nil
}

// PublishBatch logs multiple opportunities.
func (p *NoOpPublisher) PublishBatch(ctx context.Context, opportunities []*arbitrage.Opportunity) error {
	for _, opp := range opportunities {
		if err := p.PublishOpportunity(ctx, opp); err != nil {
			return err
		}
	}
	return nil
}

// CircuitBreakerState returns "closed" since there's no circuit breaker.
func (p *NoOpPublisher) CircuitBreakerState() string {
	return "closed"
}

// ResetCircuitBreaker is a no-op since there's no circuit breaker.
func (p *NoOpPublisher) ResetCircuitBreaker() {}
