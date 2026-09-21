package httpserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// Values of sse.outcome: the handler ended the stream, or the client left first.
const (
	streamCompleted    = "completed"
	streamClientClosed = "client_closed"
)

const streamOutcomeKey = attribute.Key("sse.outcome")

var (
	streamDurationBuckets = []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300, 600, 1800, 3600}
	firstEventBuckets     = []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10, 30, 60}
	streamEventsBuckets   = []float64{1, 2, 5, 10, 25, 50, 100, 250, 500, 1000, 5000, 10000}
)

type streamMetrics struct {
	duration   metric.Float64Histogram
	events     metric.Int64Histogram
	firstEvent metric.Float64Histogram
}

func newStreamMetrics(meter metric.Meter) (streamMetrics, error) {
	duration, errDuration := meter.Float64Histogram(
		"http.server.sse.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of server-sent event streams."),
		metric.WithExplicitBucketBoundaries(streamDurationBuckets...),
	)
	events, errEvents := meter.Int64Histogram(
		"http.server.sse.events",
		metric.WithUnit("{event}"),
		metric.WithDescription("Messages sent by a server-sent event stream."),
		metric.WithExplicitBucketBoundaries(streamEventsBuckets...),
	)
	firstEvent, errFirstEvent := meter.Float64Histogram(
		"http.server.sse.time_to_first_event",
		metric.WithUnit("s"),
		metric.WithDescription("Time from the request to the first complete message of a server-sent event stream."),
		metric.WithExplicitBucketBoundaries(firstEventBuckets...),
	)

	return streamMetrics{duration: duration, events: events, firstEvent: firstEvent},
		errors.Join(errDuration, errEvents, errFirstEvent)
}

// record reports a finished stream. ctx is the request context, canceled
// when the client disconnects.
func (m streamMetrics) record(ctx context.Context, route string, w *streamWriter) {
	outcome := streamCompleted
	if w.failed || ctx.Err() != nil {
		outcome = streamClientClosed
	}

	var routeAttrs []attribute.KeyValue
	if route != "" {
		routeAttrs = append(routeAttrs, semconv.HTTPRoute(route))
	}
	attrs := metric.WithAttributeSet(attribute.NewSet(append(routeAttrs, streamOutcomeKey.String(outcome))...))

	m.duration.Record(ctx, time.Since(w.start).Seconds(), attrs)
	m.events.Record(ctx, w.events, attrs)

	if w.events > 0 {
		m.firstEvent.Record(ctx, w.firstEvent.Seconds(), metric.WithAttributeSet(attribute.NewSet(routeAttrs...)))
	}
}

// streamWriter counts the messages of a server-sent event stream and times
// the first one; other responses pass through untouched. Like
// responseRecorder, it embeds gin.ResponseWriter and unwraps, so streams stay
// unbuffered and keep their write deadline.
type streamWriter struct {
	gin.ResponseWriter

	start      time.Time
	firstEvent time.Duration
	events     int64
	newlines   int
	failed     bool
}

func (w *streamWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *streamWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	if isEventStream(w.Header()) {
		w.observe(b[:n], err)
	}

	return n, err
}

func (w *streamWriter) WriteString(s string) (int, error) {
	n, err := w.ResponseWriter.WriteString(s)
	if isEventStream(w.Header()) {
		w.observe([]byte(s[:n]), err)
	}

	return n, err
}

// observe counts the messages written so far. Each one ends with a blank
// line, which a writer may split across writes.
func (w *streamWriter) observe(b []byte, err error) {
	if err != nil {
		w.failed = true
	}

	for _, c := range b {
		switch c {
		case '\r':
			// Ignored, so CRLF line endings count as well.
		case '\n':
			w.newlines++
			if w.newlines == 2 {
				w.events++
				if w.events == 1 {
					w.firstEvent = time.Since(w.start)
				}
			}
		default:
			w.newlines = 0
		}
	}
}
