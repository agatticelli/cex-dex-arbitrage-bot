// Package observability provides logging, metrics, and tracing utilities.
package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Tracer provides distributed tracing capabilities.
// This interface decouples application code from the tracing implementation,
// allowing easy swapping between OTEL, Datadog, or other providers.
type Tracer interface {
	// StartSpan creates a new span as a child of the span in ctx.
	StartSpan(ctx context.Context, name string, opts ...SpanOption) (context.Context, Span)

	// SpanFromContext returns the current span from context.
	SpanFromContext(ctx context.Context) Span

	// WithAttributes returns a new context with the given attributes
	// that will be added to all spans created from this context.
	WithAttributes(ctx context.Context, attrs ...attribute.KeyValue) context.Context
}

// Span represents a unit of work in a trace.
type Span interface {
	// End completes the span. Must be called when work is done.
	End()

	// SetName changes the span name.
	SetName(name string)

	// SetStatus sets the span status (OK, Error).
	SetStatus(code SpanStatus, description string)

	// SetAttributes adds attributes to the span.
	SetAttributes(attrs ...attribute.KeyValue)

	// SetAttribute adds a single attribute.
	SetAttribute(key string, value interface{})

	// AddEvent records an event with optional attributes.
	AddEvent(name string, attrs ...attribute.KeyValue)

	// RecordError records an error without changing span status.
	RecordError(err error)

	// NoticeError records an error AND sets span status to Error.
	// This is the preferred method for handling errors in spans.
	NoticeError(err error)

	// IsRecording returns true if the span is recording events.
	IsRecording() bool

	// TraceID returns the trace ID as a string.
	TraceID() string

	// SpanID returns the span ID as a string.
	SpanID() string
}

// SpanStatus represents the status of a span.
type SpanStatus int

const (
	SpanStatusUnset SpanStatus = iota
	SpanStatusOK
	SpanStatusError
)

// SpanOption configures span creation.
type SpanOption func(*spanConfig)

type spanConfig struct {
	kind       trace.SpanKind
	attributes []attribute.KeyValue
}

// WithSpanKind sets the span kind (Client, Server, Producer, Consumer, Internal).
func WithSpanKind(kind trace.SpanKind) SpanOption {
	return func(c *spanConfig) {
		c.kind = kind
	}
}

// WithAttributes adds attributes to the span at creation time.
func WithAttributes(attrs ...attribute.KeyValue) SpanOption {
	return func(c *spanConfig) {
		c.attributes = append(c.attributes, attrs...)
	}
}

// --- OTEL Implementation ---

// otelTracer wraps OpenTelemetry tracer.
type otelTracer struct {
	tracer trace.Tracer
	name   string
}

// NewTracer creates a new Tracer backed by OpenTelemetry.
func NewTracer(name string) Tracer {
	return &otelTracer{
		tracer: otel.Tracer(name),
		name:   name,
	}
}

func (t *otelTracer) StartSpan(ctx context.Context, name string, opts ...SpanOption) (context.Context, Span) {
	cfg := &spanConfig{
		kind: trace.SpanKindInternal,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	otelOpts := []trace.SpanStartOption{
		trace.WithSpanKind(cfg.kind),
	}
	if len(cfg.attributes) > 0 {
		otelOpts = append(otelOpts, trace.WithAttributes(cfg.attributes...))
	}

	ctx, span := t.tracer.Start(ctx, name, otelOpts...)
	return ctx, &otelSpan{span: span}
}

func (t *otelTracer) SpanFromContext(ctx context.Context) Span {
	return &otelSpan{span: trace.SpanFromContext(ctx)}
}

func (t *otelTracer) WithAttributes(ctx context.Context, attrs ...attribute.KeyValue) context.Context {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attrs...)
	return ctx
}

// otelSpan wraps OpenTelemetry span.
type otelSpan struct {
	span trace.Span
}

func (s *otelSpan) End() {
	s.span.End()
}

func (s *otelSpan) SetName(name string) {
	s.span.SetName(name)
}

func (s *otelSpan) SetStatus(code SpanStatus, description string) {
	var otelCode codes.Code
	switch code {
	case SpanStatusOK:
		otelCode = codes.Ok
	case SpanStatusError:
		otelCode = codes.Error
	default:
		otelCode = codes.Unset
	}
	s.span.SetStatus(otelCode, description)
}

func (s *otelSpan) SetAttributes(attrs ...attribute.KeyValue) {
	s.span.SetAttributes(attrs...)
}

func (s *otelSpan) SetAttribute(key string, value interface{}) {
	switch v := value.(type) {
	case string:
		s.span.SetAttributes(attribute.String(key, v))
	case int:
		s.span.SetAttributes(attribute.Int(key, v))
	case int64:
		s.span.SetAttributes(attribute.Int64(key, v))
	case float64:
		s.span.SetAttributes(attribute.Float64(key, v))
	case bool:
		s.span.SetAttributes(attribute.Bool(key, v))
	default:
		s.span.SetAttributes(attribute.String(key, fmt.Sprintf("%v", v)))
	}
}

func (s *otelSpan) AddEvent(name string, attrs ...attribute.KeyValue) {
	if len(attrs) > 0 {
		s.span.AddEvent(name, trace.WithAttributes(attrs...))
	} else {
		s.span.AddEvent(name)
	}
}

func (s *otelSpan) RecordError(err error) {
	if err != nil {
		s.span.RecordError(err)
	}
}

func (s *otelSpan) NoticeError(err error) {
	if err != nil {
		s.span.RecordError(err)
		s.span.SetStatus(codes.Error, err.Error())
	}
}

func (s *otelSpan) IsRecording() bool {
	return s.span.IsRecording()
}

func (s *otelSpan) TraceID() string {
	return s.span.SpanContext().TraceID().String()
}

func (s *otelSpan) SpanID() string {
	return s.span.SpanContext().SpanID().String()
}

// --- Noop Implementation for disabled tracing ---

type noopTracer struct{}

// NewNoopTracer returns a tracer that does nothing.
func NewNoopTracer() Tracer {
	return &noopTracer{}
}

func (t *noopTracer) StartSpan(ctx context.Context, _ string, _ ...SpanOption) (context.Context, Span) {
	return ctx, &noopSpan{}
}

func (t *noopTracer) SpanFromContext(_ context.Context) Span {
	return &noopSpan{}
}

func (t *noopTracer) WithAttributes(ctx context.Context, _ ...attribute.KeyValue) context.Context {
	return ctx
}

type noopSpan struct{}

func (s *noopSpan) End()                                           {}
func (s *noopSpan) SetName(_ string)                               {}
func (s *noopSpan) SetStatus(_ SpanStatus, _ string)               {}
func (s *noopSpan) SetAttributes(_ ...attribute.KeyValue)          {}
func (s *noopSpan) SetAttribute(_ string, _ interface{})           {}
func (s *noopSpan) AddEvent(_ string, _ ...attribute.KeyValue)     {}
func (s *noopSpan) RecordError(_ error)                            {}
func (s *noopSpan) NoticeError(_ error)                            {}
func (s *noopSpan) IsRecording() bool                              { return false }
func (s *noopSpan) TraceID() string                                { return "" }
func (s *noopSpan) SpanID() string                                 { return "" }
