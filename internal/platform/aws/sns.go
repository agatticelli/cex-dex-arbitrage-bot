package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/gatti/cex-dex-arbitrage-bot/internal/platform/observability"
	"github.com/gatti/cex-dex-arbitrage-bot/internal/platform/resilience"
)

// SNSClient wraps AWS SNS client with resilience patterns
type SNSClient struct {
	client         *sns.Client
	circuitBreaker *resilience.CircuitBreaker
	retryConfig    resilience.RetryConfig
	logger         *observability.Logger
	metrics        *observability.Metrics
}

// SNSClientConfig holds SNS client configuration
type SNSClientConfig struct {
	AWSConfig      aws.Config
	Logger         *observability.Logger
	Metrics        *observability.Metrics
	RetryConfig    *resilience.RetryConfig
	CircuitBreaker *resilience.CircuitBreaker
}

// NewSNSClient creates a new SNS client with resilience patterns
func NewSNSClient(cfg SNSClientConfig) *SNSClient {
	client := sns.NewFromConfig(cfg.AWSConfig)

	// Default retry config
	retryConfig := resilience.DefaultRetryConfig()
	if cfg.RetryConfig != nil {
		retryConfig = *cfg.RetryConfig
	}

	// Default circuit breaker
	circuitBreaker := cfg.CircuitBreaker
	if circuitBreaker == nil {
		circuitBreaker = resilience.NewCircuitBreaker(resilience.CircuitBreakerConfig{
			Name:             "sns",
			FailureThreshold: 5,
			SuccessThreshold: 2,
			Timeout:          30 * time.Second,
			OnStateChange: func(from, to resilience.State) {
				if cfg.Logger != nil {
					cfg.Logger.Info("SNS circuit breaker state changed",
						"from", from.String(),
						"to", to.String(),
					)
				}

				// Update metrics
				if cfg.Metrics != nil {
					cfg.Metrics.SetCircuitBreakerState(context.Background(), "sns", int64(to))
				}
			},
		})
	}

	return &SNSClient{
		client:         client,
		circuitBreaker: circuitBreaker,
		retryConfig:    retryConfig,
		logger:         cfg.Logger,
		metrics:        cfg.Metrics,
	}
}

// Publish publishes a message to SNS topic with retry and circuit breaker
func (s *SNSClient) Publish(ctx context.Context, topicARN string, message interface{}, attributes map[string]string) error {
	start := time.Now()

	// Marshal message to JSON
	messageJSON, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	// Execute with circuit breaker and retry
	err = s.circuitBreaker.Execute(ctx, func(ctx context.Context) error {
		return resilience.Retry(ctx, s.retryConfig, func(ctx context.Context) error {
			return s.publishWithoutRetry(ctx, topicARN, string(messageJSON), attributes)
		})
	})

	// Record metrics
	duration := time.Since(start)
	status := "success"
	if err != nil {
		status = "error"
		if s.logger != nil {
			s.logger.LogError(ctx, "SNS publish failed", err,
				"topic_arn", topicARN,
				"duration_ms", duration.Milliseconds(),
			)
		}
	}

	if s.metrics != nil {
		s.metrics.RecordCEXAPICall(ctx, "sns", "publish", status, duration)
	}

	return err
}

// publishWithoutRetry publishes a message without retry (single attempt)
func (s *SNSClient) publishWithoutRetry(ctx context.Context, topicARN, message string, attributes map[string]string) error {
	// Build message attributes
	messageAttributes := make(map[string]types.MessageAttributeValue)
	for k, v := range attributes {
		messageAttributes[k] = types.MessageAttributeValue{
			DataType:    aws.String("String"),
			StringValue: aws.String(v),
		}
	}

	// Publish message
	input := &sns.PublishInput{
		TopicArn:          aws.String(topicARN),
		Message:           aws.String(message),
		MessageAttributes: messageAttributes,
	}

	_, err := s.client.Publish(ctx, input)
	if err != nil {
		return fmt.Errorf("SNS publish failed: %w", err)
	}

	return nil
}

// PublishBatch publishes multiple messages in batch
func (s *SNSClient) PublishBatch(ctx context.Context, topicARN string, messages []interface{}, attributes map[string]string) error {
	// SNS doesn't have native batch publish, so we publish sequentially
	// In production, consider using SNS FIFO with message grouping
	for i, msg := range messages {
		// Add batch index to attributes
		batchAttrs := make(map[string]string)
		for k, v := range attributes {
			batchAttrs[k] = v
		}
		batchAttrs["batch_index"] = fmt.Sprintf("%d", i)

		if err := s.Publish(ctx, topicARN, msg, batchAttrs); err != nil {
			return fmt.Errorf("batch publish failed at index %d: %w", i, err)
		}
	}

	return nil
}

// CircuitBreakerState returns current circuit breaker state
func (s *SNSClient) CircuitBreakerState() resilience.State {
	return s.circuitBreaker.State()
}

// ResetCircuitBreaker manually resets the circuit breaker
func (s *SNSClient) ResetCircuitBreaker() {
	s.circuitBreaker.Reset()
}
