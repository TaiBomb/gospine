package httpserver

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TaiBomb/gospine/telemetry"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/semconv/v1.43.0/httpconv"
)

const meterName = "github.com/TaiBomb/gospine/httpserver"

// knownMethods are the methods recorded as they are; any other is recorded as
// _OTHER, so clients cannot add series by inventing methods.
var knownMethods = map[string]struct{}{
	http.MethodGet:     {},
	http.MethodHead:    {},
	http.MethodPost:    {},
	http.MethodPut:     {},
	http.MethodPatch:   {},
	http.MethodDelete:  {},
	http.MethodConnect: {},
	http.MethodOptions: {},
	http.MethodTrace:   {},
}

type metricsConfig struct {
	skipped map[string]struct{}
}

// MetricsOption customizes Metrics.
type MetricsOption func(*metricsConfig)

// WithoutRoutes leaves the given routes out of the metrics, like the probes.
// Routes are matched against the registered route, context path included.
func WithoutRoutes(routes ...string) MetricsOption {
	return func(cfg *metricsConfig) {
		if cfg.skipped == nil {
			cfg.skipped = make(map[string]struct{}, len(routes))
		}

		for _, r := range routes {
			cfg.skipped[r] = struct{}{}
		}
	}
}

// Metrics records OpenTelemetry HTTP metrics, including request duration,
// active requests, body sizes, and SSE duration, events, and time to first event.
// Routes use http.route templates; SSE metrics include sse.outcome.
// Register before RequestLogger and Recovery to capture recovered panics as 500s.
func Metrics(opts ...MetricsOption) gin.HandlerFunc {
	var cfg metricsConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	meter := telemetry.Meter(meterName)

	duration, errDuration := httpconv.NewServerRequestDuration(meter)
	active, errActive := httpconv.NewServerActiveRequests(meter)
	requestSize, errRequestSize := httpconv.NewServerRequestBodySize(meter)
	responseSize, errResponseSize := httpconv.NewServerResponseBodySize(meter)
	streams, errStreams := newStreamMetrics(meter)

	// A failed instrument is a no-op one: report it and keep serving.
	if err := errors.Join(errDuration, errActive, errRequestSize, errResponseSize, errStreams); err != nil {
		otel.Handle(err)
	}

	return func(c *gin.Context) {
		route := c.FullPath()
		if _, skip := cfg.skipped[route]; skip {
			c.Next()
			return
		}

		ctx := c.Request.Context()
		start := time.Now()

		method := methodAttr(c.Request.Method)
		scheme := schemeAttr(c.Request)

		activeAttrs := metric.WithAttributeSet(attribute.NewSet(method, scheme))
		active.Inst().Add(ctx, 1, activeAttrs)
		// Deferred, so the count stays right even if a panic is not recovered.
		defer active.Inst().Add(ctx, -1, activeAttrs)

		stream := &streamWriter{ResponseWriter: c.Writer, start: start}
		c.Writer = stream

		c.Next()

		status := c.Writer.Status()

		attrs := []attribute.KeyValue{method, scheme, semconv.HTTPResponseStatusCode(status)}
		if route != "" {
			attrs = append(attrs, semconv.HTTPRoute(route))
		}
		if status >= http.StatusInternalServerError {
			attrs = append(attrs, semconv.ErrorTypeKey.String(strconv.Itoa(status)))
		}
		requestAttrs := metric.WithAttributeSet(attribute.NewSet(attrs...))

		if size := c.Request.ContentLength; size >= 0 {
			requestSize.Inst().Record(ctx, size, requestAttrs)
		}

		if isEventStream(c.Writer.Header()) {
			streams.record(ctx, route, stream)
			return
		}

		duration.Inst().Record(ctx, time.Since(start).Seconds(), requestAttrs)
		// gin reports -1 when nothing was written.
		responseSize.Inst().Record(ctx, int64(max(c.Writer.Size(), 0)), requestAttrs)
	}
}

func methodAttr(method string) attribute.KeyValue {
	if _, known := knownMethods[method]; known {
		return semconv.HTTPRequestMethodKey.String(method)
	}

	return semconv.HTTPRequestMethodOther
}

func schemeAttr(r *http.Request) attribute.KeyValue {
	if r.TLS != nil {
		return semconv.URLScheme("https")
	}

	return semconv.URLScheme("http")
}

func isEventStream(h http.Header) bool {
	return strings.HasPrefix(h.Get("Content-Type"), "text/event-stream")
}
