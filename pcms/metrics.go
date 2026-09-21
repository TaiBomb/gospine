package pcms

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/TaiBomb/gospine/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

const meterName = "github.com/TaiBomb/gospine/pcms"

// collectionKey is the metric attribute naming the collection, or
// globals/<slug>, a PayloadCMS call went to.
const collectionKey = attribute.Key("payloadcms.collection")

// otherCollection stands for a URL outside the API path.
const otherCollection = "_OTHER"

// clientMetrics sees every attempt through the gopcms Observer: they sit
// above the HTTP client metrics, split by collection instead of by host.
type clientMetrics struct {
	apiPath  string
	duration metric.Float64Histogram
	requests metric.Int64Counter
}

// newClientMetrics creates the instruments once, on the global provider:
// no-op while telemetry is off.
func newClientMetrics(cfg Config) clientMetrics {
	meter := telemetry.Meter(meterName)

	duration, errDuration := meter.Float64Histogram(
		"payloadcms.client.request.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of PayloadCMS calls, one per attempt."),
	)
	requests, errRequests := meter.Int64Counter(
		"payloadcms.client.requests",
		metric.WithUnit("{request}"),
		metric.WithDescription("PayloadCMS calls, one per attempt: each retry counts."),
	)

	// A failed instrument is a no-op one: report it and keep going.
	if err := errors.Join(errDuration, errRequests); err != nil {
		otel.Handle(err)
	}

	return clientMetrics{
		apiPath:  apiPath(cfg),
		duration: duration,
		requests: requests,
	}
}

func (m clientMetrics) record(ctx context.Context, method, rawURL string, statusCode int, duration time.Duration, err error) {
	attrs := []attribute.KeyValue{
		semconv.HTTPRequestMethodKey.String(method),
		collectionKey.String(collectionFromURL(m.apiPath, rawURL)),
	}
	if statusCode != 0 {
		attrs = append(attrs, semconv.HTTPResponseStatusCode(statusCode))
	}
	if errType := errorType(statusCode, err); errType != "" {
		attrs = append(attrs, semconv.ErrorTypeKey.String(errType))
	}

	set := metric.WithAttributeSet(attribute.NewSet(attrs...))
	m.duration.Record(ctx, duration.Seconds(), set)
	m.requests.Add(ctx, 1, set)
}

// apiPath is what gopcms puts before every call: the path of the base URL and
// the API prefix, without a trailing slash.
func apiPath(cfg Config) string {
	var basePath string
	if u, err := url.Parse(cfg.BaseURL); err == nil {
		basePath = strings.TrimRight(u.Path, "/")
	}

	return strings.TrimRight(basePath+"/"+strings.Trim(cfg.APIURL, "/"), "/")
}

// collectionFromURL names the collection, or globals/<slug>, of a call.
// Document ids, versions, queries and anything past the slug are left out.
func collectionFromURL(apiPath, rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return otherCollection
	}

	rest, found := strings.CutPrefix(u.Path, apiPath)
	if !found || (rest != "" && !strings.HasPrefix(rest, "/")) {
		return otherCollection
	}

	segments := strings.FieldsFunc(rest, func(r rune) bool { return r == '/' })

	switch {
	case len(segments) == 0:
		return "root"
	case segments[0] == "globals" && len(segments) > 1:
		return "globals/" + segments[1]
	default:
		return segments[0]
	}
}

// errorType classifies a failed attempt into a bounded set of values.
func errorType(statusCode int, err error) string {
	var netErr net.Error

	switch {
	case statusCode >= http.StatusBadRequest:
		return "http_" + strconv.Itoa(statusCode)
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return "timeout"
	case statusCode == 0:
		return "network"
	default:
		// A response whose body could not be read or decoded.
		return "invalid_response"
	}
}
