package notification

import (
	"context"
	"fmt"

	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/arbitrage"
	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/aws"
	"github.com/agatticelli/cex-dex-arbitrage-bot/internal/platform/observability"
	"go.opentelemetry.io/otel/attribute"
)

// Publisher publishes arbitrage opportunities to SNS
type Publisher struct {
	snsClient *aws.SNSClient
	topicARN  string
	logger    *observability.Logger
	metrics   *observability.Metrics
	tracer    observability.Tracer
}

// PublisherConfig holds publisher configuration
type PublisherConfig struct {
	SNSClient *aws.SNSClient
	TopicARN  string
	Logger    *observability.Logger
	Metrics   *observability.Metrics
	Tracer    observability.Tracer
}

// NewPublisher creates a new arbitrage opportunity publisher
func NewPublisher(cfg PublisherConfig) (*Publisher, error) {
	if cfg.SNSClient == nil {
		return nil, fmt.Errorf("SNS client is required")
	}
	if cfg.TopicARN == "" {
		return nil, fmt.Errorf("SNS topic ARN is required")
	}
	if cfg.Tracer == nil {
		cfg.Tracer = observability.NewNoopTracer()
	}

	return &Publisher{
		snsClient: cfg.SNSClient,
		topicARN:  cfg.TopicARN,
		logger:    cfg.Logger,
		metrics:   cfg.Metrics,
		tracer:    cfg.Tracer,
	}, nil
}

// PublishOpportunity publishes an arbitrage opportunity to SNS
// Implements arbitrage.NotificationPublisher interface
func (p *Publisher) PublishOpportunity(ctx context.Context, opp *arbitrage.Opportunity) error {
	ctx, span := p.tracer.StartSpan(
		ctx,
		"Publisher.PublishOpportunity",
		observability.WithAttributes(
			attribute.String("opportunity_id", opp.OpportunityID),
			attribute.Bool("profitable", opp.IsProfitable()),
			attribute.String("topic_arn", p.topicARN),
		),
	)
	defer span.End()

	// Convert opportunity to JSON
	payload, err := opp.ToJSON()
	if err != nil {
		span.NoticeError(err)
		return fmt.Errorf("failed to marshal opportunity: %w", err)
	}

	// Create message attributes for filtering
	attributes := map[string]string{
		"direction":   opp.Direction.String(),
		"blockNumber": fmt.Sprintf("%d", opp.BlockNumber),
		"profitable":  fmt.Sprintf("%t", opp.IsProfitable()),
	}

	// Add profit percentage attribute if profitable
	if opp.IsProfitable() && opp.ProfitPct != nil {
		profitPct, _ := opp.ProfitPct.Float64()
		attributes["profitPct"] = fmt.Sprintf("%.4f", profitPct)
	}

	// Publish to SNS
	err = p.snsClient.Publish(ctx, p.topicARN, string(payload), attributes)
	if err != nil {
		span.NoticeError(err)
		if p.logger != nil {
			p.logger.LogError(ctx, "failed to publish to SNS", err,
				"opportunity_id", opp.OpportunityID,
				"topic_arn", p.topicARN,
			)
		}
		return fmt.Errorf("SNS publish failed: %w", err)
	}

	// Log success
	if p.logger != nil {
		p.logger.Info("published opportunity to SNS",
			"opportunity_id", opp.OpportunityID,
			"direction", opp.Direction.String(),
			"profitable", opp.IsProfitable(),
			"topic_arn", p.topicARN,
		)
	}

	return nil
}

// PublishBatch publishes multiple opportunities in batch
func (p *Publisher) PublishBatch(ctx context.Context, opportunities []*arbitrage.Opportunity) error {
	successCount := 0
	errorCount := 0

	for _, opp := range opportunities {
		if err := p.PublishOpportunity(ctx, opp); err != nil {
			errorCount++
			if p.logger != nil {
				p.logger.LogError(ctx, "failed to publish opportunity in batch", err,
					"opportunity_id", opp.OpportunityID,
				)
			}
		} else {
			successCount++
		}
	}

	if p.logger != nil {
		p.logger.Info("batch publish completed",
			"total", len(opportunities),
			"success", successCount,
			"errors", errorCount,
		)
	}

	if errorCount > 0 {
		return fmt.Errorf("batch publish completed with %d errors out of %d", errorCount, len(opportunities))
	}

	return nil
}

// CircuitBreakerState returns the current circuit breaker state
func (p *Publisher) CircuitBreakerState() string {
	return p.snsClient.CircuitBreakerState().String()
}

// ResetCircuitBreaker manually resets the circuit breaker
func (p *Publisher) ResetCircuitBreaker() {
	p.snsClient.ResetCircuitBreaker()
	if p.logger != nil {
		p.logger.Info("reset SNS circuit breaker")
	}
}
