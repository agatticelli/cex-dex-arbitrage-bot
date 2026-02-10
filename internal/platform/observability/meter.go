package observability

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

// MeterProvider creates meters for different components.
// This allows per-component metrics that are co-located with the code.
type MeterProvider interface {
	// Meter returns a meter for the given component name.
	Meter(name string) Meter

	// Shutdown gracefully shuts down the meter provider.
	Shutdown(ctx context.Context) error

	// Handler returns the HTTP handler for metrics (e.g., /metrics).
	Handler() http.Handler
}

// Meter provides metric instruments for a specific component.
type Meter interface {
	// Counter creates a counter (monotonically increasing value).
	Counter(name, description string) Counter

	// Gauge creates a gauge (value that can go up or down).
	Gauge(name, description string) Gauge

	// Histogram creates a histogram for measuring distributions.
	Histogram(name, description string, buckets ...float64) Histogram
}

// Counter is a monotonically increasing metric.
type Counter interface {
	Add(ctx context.Context, value int64, attrs ...attribute.KeyValue)
	Inc(ctx context.Context, attrs ...attribute.KeyValue)
}

// Gauge is a metric that can increase or decrease.
type Gauge interface {
	Set(ctx context.Context, value float64, attrs ...attribute.KeyValue)
	Record(ctx context.Context, value int64, attrs ...attribute.KeyValue)
}

// Histogram records distributions of values.
type Histogram interface {
	Record(ctx context.Context, value float64, attrs ...attribute.KeyValue)
	RecordDuration(ctx context.Context, start time.Time, attrs ...attribute.KeyValue)
}

// --- Provider Configuration ---

// MetricProviderType represents the type of metric backend.
type MetricProviderType string

const (
	ProviderPrometheus MetricProviderType = "prometheus"
	ProviderOTLP       MetricProviderType = "otlp"
)

// MetricProviderConfig configures a metric provider.
type MetricProviderConfig struct {
	Type     MetricProviderType
	Endpoint string            // For OTLP: gRPC endpoint
	Headers  map[string]string // For OTLP: headers
	Insecure bool              // For OTLP: disable TLS
}

// MeterProviderConfig configures the meter provider.
type MeterProviderConfig struct {
	ServiceName string
	Version     string
	Providers   []MetricProviderConfig
}

// --- OTEL Implementation ---

type otelMeterProvider struct {
	provider *sdkmetric.MeterProvider
	exporter *prometheus.Exporter
}

// NewMeterProvider creates a new meter provider with the given configuration.
func NewMeterProvider(cfg MeterProviderConfig) (MeterProvider, error) {
	ctx := context.Background()

	// Create resource
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(cfg.ServiceName),
			semconv.ServiceVersionKey.String(cfg.Version),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	var readers []sdkmetric.Reader
	var promExporter *prometheus.Exporter

	// Configure providers
	for _, provCfg := range cfg.Providers {
		switch provCfg.Type {
		case ProviderPrometheus:
			exp, err := prometheus.New()
			if err != nil {
				return nil, fmt.Errorf("failed to create prometheus exporter: %w", err)
			}
			promExporter = exp
			readers = append(readers, exp)

		case ProviderOTLP:
			opts := []otlpmetricgrpc.Option{
				otlpmetricgrpc.WithEndpointURL(provCfg.Endpoint),
			}
			if len(provCfg.Headers) > 0 {
				opts = append(opts, otlpmetricgrpc.WithHeaders(provCfg.Headers))
			}
			if provCfg.Insecure {
				opts = append(opts, otlpmetricgrpc.WithInsecure())
			}

			exp, err := otlpmetricgrpc.New(ctx, opts...)
			if err != nil {
				return nil, fmt.Errorf("failed to create OTLP exporter: %w", err)
			}
			readers = append(readers, sdkmetric.NewPeriodicReader(exp))
		}
	}

	// Default to Prometheus if no providers configured
	if len(readers) == 0 {
		exp, err := prometheus.New()
		if err != nil {
			return nil, fmt.Errorf("failed to create default prometheus exporter: %w", err)
		}
		promExporter = exp
		readers = append(readers, exp)
	}

	// Build provider options
	opts := []sdkmetric.Option{
		sdkmetric.WithResource(res),
	}
	for _, reader := range readers {
		opts = append(opts, sdkmetric.WithReader(reader))
	}

	provider := sdkmetric.NewMeterProvider(opts...)
	otel.SetMeterProvider(provider)

	return &otelMeterProvider{
		provider: provider,
		exporter: promExporter,
	}, nil
}

func (p *otelMeterProvider) Meter(name string) Meter {
	return &otelMeter{
		meter: p.provider.Meter(name),
	}
}

func (p *otelMeterProvider) Shutdown(ctx context.Context) error {
	return p.provider.Shutdown(ctx)
}

func (p *otelMeterProvider) Handler() http.Handler {
	if p.exporter != nil {
		return promhttp.Handler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("metrics not available"))
	})
}

// --- Meter Implementation ---

type otelMeter struct {
	meter metric.Meter
}

func (m *otelMeter) Counter(name, description string) Counter {
	counter, err := m.meter.Int64Counter(name, metric.WithDescription(description))
	if err != nil {
		// Return noop on error (shouldn't happen in practice)
		return &noopCounter{}
	}
	return &otelCounter{counter: counter}
}

func (m *otelMeter) Gauge(name, description string) Gauge {
	gauge, err := m.meter.Int64Gauge(name, metric.WithDescription(description))
	if err != nil {
		return &noopGauge{}
	}
	return &otelGauge{gauge: gauge}
}

func (m *otelMeter) Histogram(name, description string, buckets ...float64) Histogram {
	opts := []metric.Float64HistogramOption{
		metric.WithDescription(description),
	}
	if len(buckets) > 0 {
		opts = append(opts, metric.WithExplicitBucketBoundaries(buckets...))
	}

	histogram, err := m.meter.Float64Histogram(name, opts...)
	if err != nil {
		return &noopHistogram{}
	}
	return &otelHistogram{histogram: histogram}
}

// --- Counter Implementation ---

type otelCounter struct {
	counter metric.Int64Counter
}

func (c *otelCounter) Add(ctx context.Context, value int64, attrs ...attribute.KeyValue) {
	c.counter.Add(ctx, value, metric.WithAttributes(attrs...))
}

func (c *otelCounter) Inc(ctx context.Context, attrs ...attribute.KeyValue) {
	c.counter.Add(ctx, 1, metric.WithAttributes(attrs...))
}

// --- Gauge Implementation ---

type otelGauge struct {
	gauge metric.Int64Gauge
}

func (g *otelGauge) Set(ctx context.Context, value float64, attrs ...attribute.KeyValue) {
	g.gauge.Record(ctx, int64(value), metric.WithAttributes(attrs...))
}

func (g *otelGauge) Record(ctx context.Context, value int64, attrs ...attribute.KeyValue) {
	g.gauge.Record(ctx, value, metric.WithAttributes(attrs...))
}

// --- Histogram Implementation ---

type otelHistogram struct {
	histogram metric.Float64Histogram
}

func (h *otelHistogram) Record(ctx context.Context, value float64, attrs ...attribute.KeyValue) {
	h.histogram.Record(ctx, value, metric.WithAttributes(attrs...))
}

func (h *otelHistogram) RecordDuration(ctx context.Context, start time.Time, attrs ...attribute.KeyValue) {
	duration := time.Since(start).Milliseconds()
	h.histogram.Record(ctx, float64(duration), metric.WithAttributes(attrs...))
}

// --- Noop Implementations ---

type noopCounter struct{}

func (c *noopCounter) Add(_ context.Context, _ int64, _ ...attribute.KeyValue) {}
func (c *noopCounter) Inc(_ context.Context, _ ...attribute.KeyValue)          {}

type noopGauge struct{}

func (g *noopGauge) Set(_ context.Context, _ float64, _ ...attribute.KeyValue)   {}
func (g *noopGauge) Record(_ context.Context, _ int64, _ ...attribute.KeyValue) {}

type noopHistogram struct{}

func (h *noopHistogram) Record(_ context.Context, _ float64, _ ...attribute.KeyValue)      {}
func (h *noopHistogram) RecordDuration(_ context.Context, _ time.Time, _ ...attribute.KeyValue) {}

// --- Noop Meter Provider ---

type noopMeterProvider struct{}

// NewNoopMeterProvider returns a meter provider that does nothing.
func NewNoopMeterProvider() MeterProvider {
	return &noopMeterProvider{}
}

func (p *noopMeterProvider) Meter(_ string) Meter {
	return &noopMeter{}
}

func (p *noopMeterProvider) Shutdown(_ context.Context) error {
	return nil
}

func (p *noopMeterProvider) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
}

type noopMeter struct{}

func (m *noopMeter) Counter(_, _ string) Counter                        { return &noopCounter{} }
func (m *noopMeter) Gauge(_, _ string) Gauge                            { return &noopGauge{} }
func (m *noopMeter) Histogram(_, _ string, _ ...float64) Histogram      { return &noopHistogram{} }
