package observability

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

// Metrics holds all application metrics
type Metrics struct {
	meter metric.Meter

	// Block processing metrics
	BlockProcessingDuration metric.Float64Histogram
	BlocksProcessed         metric.Int64Counter

	// Arbitrage detection metrics
	OpportunitiesDetected metric.Int64Counter
	OpportunitiesProfit   metric.Float64Histogram

	// CEX API metrics
	CEXAPICalls    metric.Int64Counter
	CEXAPIDuration metric.Float64Histogram

	// DEX quote metrics
	DEXQuoteDuration metric.Float64Histogram
	DEXQuoteCalls    metric.Int64Counter

	// QuoterV2 specific metrics
	QuoterCallsTotal metric.Int64Counter
	QuoterDuration   metric.Float64Histogram

	// Quote cache metrics
	QuoteCacheRequests metric.Int64Counter

	// Fee tier metrics
	FeeTierSelected metric.Int64Counter

	// ETH price metrics
	ETHPriceUSD metric.Float64Gauge

	// Block gap backfill metrics
	BlockGapBackfillTotal    metric.Int64Counter
	BlockGapBackfillDuration metric.Float64Histogram

	// WebSocket metrics
	WebSocketReconnections metric.Int64Counter
	WebSocketConnected     metric.Int64Gauge
	BlocksReceived         metric.Int64Counter
	BlockGaps              metric.Int64Counter

	// RPC endpoint metrics
	RPCEndpointHealth metric.Int64Gauge

	// Cache metrics
	CacheHits   metric.Int64Counter
	CacheMisses metric.Int64Counter

	// Circuit breaker metrics
	CircuitBreakerState metric.Int64Gauge

	// Error metrics
	Errors metric.Int64Counter

	// Prometheus exporter for HTTP handler
	exporter *prometheus.Exporter
}

// NewMetrics creates a new Metrics instance
func NewMetrics(serviceName string, enabled bool) (*Metrics, error) {
	if !enabled {
		return &Metrics{}, nil
	}

	// Create resource
	res, err := resource.New(
		context.Background(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
			semconv.ServiceVersionKey.String("1.0.0"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Create Prometheus exporter
	exporter, err := prometheus.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create Prometheus exporter: %w", err)
	}

	// Create meter provider
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(exporter),
	)

	// Get meter
	meter := provider.Meter(serviceName)

	// Create metrics instance
	m := &Metrics{
		meter:    meter,
		exporter: exporter,
	}

	// Initialize all metrics
	if err := m.initMetrics(); err != nil {
		return nil, fmt.Errorf("failed to initialize metrics: %w", err)
	}

	return m, nil
}

// initMetrics initializes all metric instruments
func (m *Metrics) initMetrics() error {
	var err error

	// Block processing metrics
	m.BlockProcessingDuration, err = m.meter.Float64Histogram(
		"arbitrage.block.processing.duration",
		metric.WithDescription("Block processing duration in milliseconds"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return err
	}

	m.BlocksProcessed, err = m.meter.Int64Counter(
		"arbitrage.blocks.processed",
		metric.WithDescription("Total number of blocks processed"),
	)
	if err != nil {
		return err
	}

	// Arbitrage detection metrics
	m.OpportunitiesDetected, err = m.meter.Int64Counter(
		"arbitrage.opportunities.detected",
		metric.WithDescription("Total arbitrage opportunities detected"),
	)
	if err != nil {
		return err
	}

	m.OpportunitiesProfit, err = m.meter.Float64Histogram(
		"arbitrage.opportunities.profit",
		metric.WithDescription("Arbitrage opportunity profit in USD"),
		metric.WithUnit("USD"),
	)
	if err != nil {
		return err
	}

	// CEX API metrics
	m.CEXAPICalls, err = m.meter.Int64Counter(
		"arbitrage.cex.api.calls",
		metric.WithDescription("Total CEX API calls"),
	)
	if err != nil {
		return err
	}

	m.CEXAPIDuration, err = m.meter.Float64Histogram(
		"arbitrage.cex.api.duration",
		metric.WithDescription("CEX API call duration in milliseconds"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return err
	}

	// DEX quote metrics
	m.DEXQuoteDuration, err = m.meter.Float64Histogram(
		"arbitrage.dex.quote.duration",
		metric.WithDescription("DEX quote call duration in milliseconds"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return err
	}

	m.DEXQuoteCalls, err = m.meter.Int64Counter(
		"arbitrage.dex.quote.calls",
		metric.WithDescription("Total DEX quote calls"),
	)
	if err != nil {
		return err
	}

	// QuoterV2 specific metrics
	m.QuoterCallsTotal, err = m.meter.Int64Counter(
		"arbitrage.quoter.calls",
		metric.WithDescription("Total QuoterV2 contract calls"),
	)
	if err != nil {
		return err
	}

	m.QuoterDuration, err = m.meter.Float64Histogram(
		"arbitrage.quoter.duration",
		metric.WithDescription("QuoterV2 call duration in milliseconds"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return err
	}

	// Quote cache metrics
	m.QuoteCacheRequests, err = m.meter.Int64Counter(
		"arbitrage.quote_cache.requests",
		metric.WithDescription("Quote cache requests (hit/miss)"),
	)
	if err != nil {
		return err
	}

	// Fee tier metrics
	m.FeeTierSelected, err = m.meter.Int64Counter(
		"arbitrage.fee_tier.selected",
		metric.WithDescription("Fee tier selected for best execution price"),
	)
	if err != nil {
		return err
	}

	// ETH price metrics
	m.ETHPriceUSD, err = m.meter.Float64Gauge(
		"arbitrage.eth.price.usd",
		metric.WithDescription("Current ETH price in USD"),
		metric.WithUnit("USD"),
	)
	if err != nil {
		return err
	}

	// Block gap backfill metrics
	m.BlockGapBackfillTotal, err = m.meter.Int64Counter(
		"arbitrage.block_gap.backfill",
		metric.WithDescription("Total blocks backfilled after gaps"),
	)
	if err != nil {
		return err
	}

	m.BlockGapBackfillDuration, err = m.meter.Float64Histogram(
		"arbitrage.block_gap.backfill_duration",
		metric.WithDescription("Block gap backfill duration in milliseconds"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return err
	}

	// WebSocket metrics
	m.WebSocketReconnections, err = m.meter.Int64Counter(
		"arbitrage.websocket.reconnections",
		metric.WithDescription("Total WebSocket reconnections"),
	)
	if err != nil {
		return err
	}

	m.WebSocketConnected, err = m.meter.Int64Gauge(
		"arbitrage.websocket.connected",
		metric.WithDescription("WebSocket connection status (1=connected, 0=disconnected)"),
	)
	if err != nil {
		return err
	}

	m.BlocksReceived, err = m.meter.Int64Counter(
		"arbitrage.blocks.received",
		metric.WithDescription("Total blocks received from WebSocket"),
	)
	if err != nil {
		return err
	}

	m.BlockGaps, err = m.meter.Int64Counter(
		"arbitrage.block.gaps",
		metric.WithDescription("Total block sequence gaps detected"),
	)
	if err != nil {
		return err
	}

	// RPC endpoint metrics
	m.RPCEndpointHealth, err = m.meter.Int64Gauge(
		"arbitrage.rpc.endpoint.health",
		metric.WithDescription("RPC endpoint health status (1=healthy, 0=unhealthy)"),
	)
	if err != nil {
		return err
	}

	// Cache metrics
	m.CacheHits, err = m.meter.Int64Counter(
		"arbitrage.cache.hits",
		metric.WithDescription("Total cache hits"),
	)
	if err != nil {
		return err
	}

	m.CacheMisses, err = m.meter.Int64Counter(
		"arbitrage.cache.misses",
		metric.WithDescription("Total cache misses"),
	)
	if err != nil {
		return err
	}

	// Circuit breaker metrics
	m.CircuitBreakerState, err = m.meter.Int64Gauge(
		"arbitrage.circuit_breaker.state",
		metric.WithDescription("Circuit breaker state (0=closed, 1=open, 2=half-open)"),
	)
	if err != nil {
		return err
	}

	// Error metrics
	m.Errors, err = m.meter.Int64Counter(
		"arbitrage.errors",
		metric.WithDescription("Total errors encountered"),
	)
	if err != nil {
		return err
	}

	return nil
}

// RecordBlockProcessing records block processing duration
func (m *Metrics) RecordBlockProcessing(ctx context.Context, duration time.Duration) {
	m.BlockProcessingDuration.Record(ctx, float64(duration.Milliseconds()))
	m.BlocksProcessed.Add(ctx, 1)
}

// RecordOpportunity records an arbitrage opportunity
func (m *Metrics) RecordOpportunity(ctx context.Context, pair string, direction string, profitable bool, profitUSD float64) {
	attrs := []attribute.KeyValue{
		attribute.String("pair", pair),
		attribute.String("direction", direction),
		attribute.Bool("profitable", profitable),
	}

	m.OpportunitiesDetected.Add(ctx, 1, metric.WithAttributes(attrs...))

	if profitable {
		m.OpportunitiesProfit.Record(ctx, profitUSD, metric.WithAttributes(attrs...))
	}
}

// RecordCEXAPICall records a CEX API call
func (m *Metrics) RecordCEXAPICall(ctx context.Context, exchange, endpoint, status string, duration time.Duration) {
	attrs := []attribute.KeyValue{
		attribute.String("exchange", exchange),
		attribute.String("endpoint", endpoint),
		attribute.String("status", status),
	}

	m.CEXAPICalls.Add(ctx, 1, metric.WithAttributes(attrs...))
	m.CEXAPIDuration.Record(ctx, float64(duration.Milliseconds()), metric.WithAttributes(attrs...))
}

// RecordDEXQuote records a DEX quote call
func (m *Metrics) RecordDEXQuote(ctx context.Context, dex string, duration time.Duration, success bool) {
	attrs := []attribute.KeyValue{
		attribute.String("dex", dex),
		attribute.Bool("success", success),
	}

	m.DEXQuoteCalls.Add(ctx, 1, metric.WithAttributes(attrs...))
	m.DEXQuoteDuration.Record(ctx, float64(duration.Milliseconds()), metric.WithAttributes(attrs...))
}

// RecordWebSocketReconnection records a WebSocket reconnection
func (m *Metrics) RecordWebSocketReconnection(ctx context.Context, attempts int) {
	m.WebSocketReconnections.Add(ctx, 1, metric.WithAttributes(
		attribute.Int("attempts", attempts),
	))
}

// RecordWebSocketConnection records WebSocket connection status with URL
func (m *Metrics) RecordWebSocketConnection(ctx context.Context, url string, connected bool) {
	val := int64(0)
	if connected {
		val = 1
	}
	m.WebSocketConnected.Record(ctx, val, metric.WithAttributes(
		attribute.String("url", url),
	))
}

// RecordBlockReceived records a block received from WebSocket
func (m *Metrics) RecordBlockReceived(ctx context.Context, blockNumber uint64) {
	m.BlocksReceived.Add(ctx, 1, metric.WithAttributes(
		attribute.Int64("block_number", int64(blockNumber)),
	))
}

// RecordBlockGap records a gap in block sequence
func (m *Metrics) RecordBlockGap(ctx context.Context, gap int64) {
	m.BlockGaps.Add(ctx, 1, metric.WithAttributes(
		attribute.Int64("gap_size", gap),
	))
}

// RecordRPCEndpointHealth records RPC endpoint health status
func (m *Metrics) RecordRPCEndpointHealth(ctx context.Context, url string, healthy bool) {
	val := int64(0)
	if healthy {
		val = 1
	}
	m.RPCEndpointHealth.Record(ctx, val, metric.WithAttributes(
		attribute.String("url", url),
	))
}

// SetWebSocketConnected sets WebSocket connection status
func (m *Metrics) SetWebSocketConnected(ctx context.Context, connected bool) {
	val := int64(0)
	if connected {
		val = 1
	}
	m.WebSocketConnected.Record(ctx, val)
}

// RecordCacheHit records a cache hit
func (m *Metrics) RecordCacheHit(ctx context.Context, layer string) {
	m.CacheHits.Add(ctx, 1, metric.WithAttributes(attribute.String("layer", layer)))
}

// RecordCacheMiss records a cache miss
func (m *Metrics) RecordCacheMiss(ctx context.Context, layer string) {
	m.CacheMisses.Add(ctx, 1, metric.WithAttributes(attribute.String("layer", layer)))
}

// SetCircuitBreakerState sets circuit breaker state
// 0 = closed, 1 = open, 2 = half-open
func (m *Metrics) SetCircuitBreakerState(ctx context.Context, service string, state int64) {
	m.CircuitBreakerState.Record(ctx, state, metric.WithAttributes(attribute.String("service", service)))
}

// RecordError records an error
func (m *Metrics) RecordError(ctx context.Context, errorType string) {
	m.Errors.Add(ctx, 1, metric.WithAttributes(attribute.String("type", errorType)))
}

// RecordFeeTierUsed records when a specific fee tier is selected for best execution
func (m *Metrics) RecordFeeTierUsed(ctx context.Context, feeTier uint32) {
	m.FeeTierSelected.Add(ctx, 1, metric.WithAttributes(
		attribute.Int64("fee_tier", int64(feeTier)),
	))
}

// RecordETHPrice records the current ETH price in USD
func (m *Metrics) RecordETHPrice(ctx context.Context, priceUSD float64) {
	m.ETHPriceUSD.Record(ctx, priceUSD)
}

// RecordQuoterCall records a QuoterV2 contract call
func (m *Metrics) RecordQuoterCall(ctx context.Context, feeTier uint32, status string, duration time.Duration) {
	attrs := []attribute.KeyValue{
		attribute.Int64("fee_tier", int64(feeTier)),
		attribute.String("status", status),
	}
	m.QuoterCallsTotal.Add(ctx, 1, metric.WithAttributes(attrs...))
	m.QuoterDuration.Record(ctx, float64(duration.Milliseconds()), metric.WithAttributes(attrs...))
}

// RecordQuoteCacheRequest records a quote cache request (hit or miss)
func (m *Metrics) RecordQuoteCacheRequest(ctx context.Context, pair string, feeTier uint32, provider, layer string, hit bool) {
	status := "miss"
	if hit {
		status = "hit"
	}
	if pair == "" {
		pair = "unknown"
	}
	attrs := []attribute.KeyValue{
		attribute.String("pair", pair),
		attribute.Int64("fee_tier", int64(feeTier)),
		attribute.String("provider", provider),
		attribute.String("layer", layer),
		attribute.String("status", status),
	}
	m.QuoteCacheRequests.Add(ctx, 1, metric.WithAttributes(attrs...))
}

// RecordBlockGapBackfill records a block gap backfill operation
func (m *Metrics) RecordBlockGapBackfill(ctx context.Context, blocksRecovered int64, duration time.Duration) {
	m.BlockGapBackfillTotal.Add(ctx, blocksRecovered)
	m.BlockGapBackfillDuration.Record(ctx, float64(duration.Milliseconds()))
}

// Handler returns the HTTP handler for Prometheus metrics
func (m *Metrics) Handler() http.Handler {
	// Use the standard Prometheus HTTP handler
	// The OpenTelemetry Prometheus exporter automatically registers metrics
	// with the default Prometheus registry
	return promhttp.Handler()
}
