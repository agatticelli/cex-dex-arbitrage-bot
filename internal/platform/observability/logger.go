package observability

import (
	"context"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel/trace"
)

// Logger wraps slog.Logger with trace context integration
type Logger struct {
	*slog.Logger
}

// NewLogger creates a new Logger instance
func NewLogger(level, format string) *Logger {
	var handler slog.Handler

	// Parse log level
	logLevel := parseLogLevel(level)

	// Choose handler based on format
	opts := &slog.HandlerOptions{
		Level: logLevel,
		AddSource: true,
	}

	switch format {
	case "json":
		handler = slog.NewJSONHandler(os.Stdout, opts)
	case "text":
		handler = slog.NewTextHandler(os.Stdout, opts)
	default:
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	return &Logger{
		Logger: slog.New(handler),
	}
}

// WithTrace extracts trace ID and span ID from context and adds them to log fields
func (l *Logger) WithTrace(ctx context.Context) *slog.Logger {
	span := trace.SpanFromContext(ctx)
	if !span.SpanContext().IsValid() {
		return l.Logger
	}

	return l.With(
		slog.String("trace_id", span.SpanContext().TraceID().String()),
		slog.String("span_id", span.SpanContext().SpanID().String()),
	)
}

// WithFields adds fields to the logger
func (l *Logger) WithFields(fields ...any) *slog.Logger {
	return l.With(fields...)
}

// WithContext is an alias for WithTrace
func (l *Logger) WithContext(ctx context.Context) *slog.Logger {
	return l.WithTrace(ctx)
}

// parseLogLevel converts string level to slog.Level
func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Helper methods for common logging patterns

// LogError logs an error with context
func (l *Logger) LogError(ctx context.Context, msg string, err error, fields ...any) {
	allFields := append(fields, slog.Any("error", err))
	l.WithTrace(ctx).Error(msg, allFields...)
}

// LogInfo logs info with context
func (l *Logger) LogInfo(ctx context.Context, msg string, fields ...any) {
	l.WithTrace(ctx).Info(msg, fields...)
}

// LogDebug logs debug with context
func (l *Logger) LogDebug(ctx context.Context, msg string, fields ...any) {
	l.WithTrace(ctx).Debug(msg, fields...)
}

// LogWarn logs warning with context
func (l *Logger) LogWarn(ctx context.Context, msg string, fields ...any) {
	l.WithTrace(ctx).Warn(msg, fields...)
}
