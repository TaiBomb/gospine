package logging

import (
	"context"
	"log/slog"
	"math"
	"time"
)

const (
	// RequestIDHeader is the header the request id travels on.
	RequestIDHeader = "X-Request-ID"

	// RequestIDField is the log field carrying the request id.
	RequestIDField = "request_id"

	// CorrelationIDField is the log field carrying the id sent by the caller.
	CorrelationIDField = "correlation_id"
)

type requestIDCtxKey struct{}
type correlationIDCtxKey struct{}

// ContextWithRequestID returns a context carrying the request id.
func ContextWithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDCtxKey{}, id)
}

// RequestIDFromContext returns the request id, or an empty string if absent.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDCtxKey{}).(string)
	return id
}

// ContextWithCorrelationID returns a context carrying the caller's id.
func ContextWithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationIDCtxKey{}, id)
}

// CorrelationIDFromContext returns the caller's id, or an empty string if absent.
func CorrelationIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(correlationIDCtxKey{}).(string)
	return id
}

// FromContext returns Log enriched with the ids stored in ctx.
// It falls back to slog.Default when InitLogger was never called.
func FromContext(ctx context.Context) *CustomLogger {
	base := Log
	if base == nil {
		base = &CustomLogger{slog.Default()}
	}

	var fields []any

	if id := RequestIDFromContext(ctx); id != "" {
		fields = append(fields, RequestIDField, id)
	}

	if id := CorrelationIDFromContext(ctx); id != "" {
		fields = append(fields, CorrelationIDField, id)
	}

	if len(fields) == 0 {
		return base
	}

	return &CustomLogger{base.Logger.With(fields...)}
}

// Millis renders d as milliseconds with microsecond precision.
func Millis(d time.Duration) float64 {
	return math.Round(float64(d.Nanoseconds())/1e3) / 1e3
}
