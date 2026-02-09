package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TracerProvider wraps OpenTelemetry tracer provider
type TracerProvider struct {
	provider *sdktrace.TracerProvider
	tracer   trace.Tracer
}

// NewTracerProvider creates a new tracer provider
func NewTracerProvider(ctx context.Context, serviceName, endpoint string, enabled bool) (*TracerProvider, error) {
	if !enabled {
		// Return a no-op provider
		return &TracerProvider{
			provider: sdktrace.NewTracerProvider(),
			tracer:   otel.Tracer(serviceName),
		}, nil
	}

	// Create resource
	res, err := resource.New(
		ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
			semconv.ServiceVersionKey.String("1.0.0"),
			attribute.String("environment", "development"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Create OTLP exporter
	conn, err := grpc.NewClient(
		endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection: %w", err)
	}

	exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP trace exporter: %w", err)
	}

	// Create tracer provider
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	// Set global propagator
	otel.SetTextMapPropagator(propagation.TraceContext{})

	// Set global tracer provider
	otel.SetTracerProvider(provider)

	return &TracerProvider{
		provider: provider,
		tracer:   provider.Tracer(serviceName),
	}, nil
}

// Shutdown gracefully shuts down the tracer provider
func (tp *TracerProvider) Shutdown(ctx context.Context) error {
	if tp.provider == nil {
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	return tp.provider.Shutdown(shutdownCtx)
}

// Tracer returns the tracer instance
func (tp *TracerProvider) Tracer() trace.Tracer {
	return tp.tracer
}

// StartSpan starts a new span with the given name
func (tp *TracerProvider) StartSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return tp.tracer.Start(ctx, name, opts...)
}

// Helper functions for common tracing patterns

// StartSpanWithAttributes starts a span with attributes
func StartSpanWithAttributes(ctx context.Context, tracer trace.Tracer, name string, attrs map[string]string) (context.Context, trace.Span) {
	spanOpts := []trace.SpanStartOption{}

	if len(attrs) > 0 {
		attributes := make([]attribute.KeyValue, 0, len(attrs))
		for k, v := range attrs {
			attributes = append(attributes, attribute.String(k, v))
		}
		spanOpts = append(spanOpts, trace.WithAttributes(attributes...))
	}

	return tracer.Start(ctx, name, spanOpts...)
}

// EndSpanWithError ends a span and records error if present
func EndSpanWithError(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
	}
	span.End()
}

// AddSpanAttributes adds attributes to current span
func AddSpanAttributes(ctx context.Context, attrs map[string]string) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}

	for k, v := range attrs {
		span.SetAttributes(attribute.String(k, v))
	}
}

// AddSpanEvent adds an event to current span
func AddSpanEvent(ctx context.Context, name string, attrs map[string]string) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}

	eventOpts := []trace.EventOption{}

	if len(attrs) > 0 {
		attributes := make([]attribute.KeyValue, 0, len(attrs))
		for k, v := range attrs {
			attributes = append(attributes, attribute.String(k, v))
		}
		eventOpts = append(eventOpts, trace.WithAttributes(attributes...))
	}

	span.AddEvent(name, eventOpts...)
}
