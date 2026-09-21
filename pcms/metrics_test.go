package pcms

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TaiBomb/gopcms"
	"github.com/TaiBomb/gospine/config"
	"github.com/TaiBomb/gospine/telemetry"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

func TestAPIPath(t *testing.T) {
	tests := []struct {
		cfg  Config
		want string
	}{
		{Config{BaseURL: "http://cms", APIURL: "/api"}, "/api"},
		{Config{BaseURL: "http://cms/", APIURL: "api/"}, "/api"},
		{Config{BaseURL: "http://host/cms", APIURL: "/api"}, "/cms/api"},
		{Config{BaseURL: "http://cms", APIURL: ""}, ""},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s + %s", tt.cfg.BaseURL, tt.cfg.APIURL), func(t *testing.T) {
			if got := apiPath(tt.cfg); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestCollectionFromURL(t *testing.T) {
	tests := []struct {
		apiPath string
		url     string
		want    string
	}{
		{"/api", "http://cms/api/strutture", "strutture"},
		{"/api", "http://cms/api/strutture/64f1a2b3c4d5e6f7a8b9c0d1", "strutture"},
		{"/api", "http://cms/api/strutture?where%5Bslug%5D%5Bequals%5D=villa-rosa&depth=1", "strutture"},
		{"/api", "http://cms/api/strutture/versions/abc", "strutture"},
		{"/api", "http://cms/api/users/me", "users"},
		{"/api", "http://cms/api/access", "access"},
		{"/api", "http://cms/api/globals/footer", "globals/footer"},
		{"/api", "http://cms/api/globals/footer/versions/abc", "globals/footer"},
		{"/api", "http://cms/api/globals", "globals"},
		{"/api", "http://cms/api", "root"},
		{"/api", "http://cms/api/", "root"},
		{"/api", "http://cms/apis/strutture", "_OTHER"},
		{"/api", "http://cms/other/strutture", "_OTHER"},
		{"/cms/api", "http://host/cms/api/strutture/1", "strutture"},
		{"", "http://cms//strutture/1", "strutture"},
		{"/api", "://not a url", "_OTHER"},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			if got := collectionFromURL(tt.apiPath, tt.url); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestErrorType(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		err        error
		want       string
	}{
		{"success", http.StatusOK, nil, ""},
		{"client error", http.StatusNotFound, errors.New("not found"), "http_404"},
		{"server error", http.StatusServiceUnavailable, errors.New("unavailable"), "http_503"},
		{"canceled", 0, fmt.Errorf("request failed: %w", context.Canceled), "canceled"},
		{"deadline", 0, fmt.Errorf("request failed: %w", context.DeadlineExceeded), "timeout"},
		{"client timeout", 0, fmt.Errorf("request failed: %w", &url.Error{Op: "Get", URL: "http://cms", Err: timeoutError{}}), "timeout"},
		{"network", 0, fmt.Errorf("request failed: %w", errors.New("connection refused")), "network"},
		{"undecodable body", http.StatusOK, errors.New("failed to decode response body"), "invalid_response"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := errorType(tt.statusCode, tt.err); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func withMetricsReader(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()

	reader := sdkmetric.NewManualReader()

	shutdown, err := telemetry.Setup(
		context.Background(),
		config.Telemetry{Enabled: true, Exporter: telemetry.ExporterPrometheus, Path: "/metrics"},
		telemetry.Service{Name: "test-service"},
		telemetry.WithReader(reader),
	)
	if err != nil {
		t.Fatalf("telemetry setup failed: %v", err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	return reader
}

// requestCounts returns payloadcms.client.requests by attribute set.
func requestCounts(t *testing.T, reader sdkmetric.Reader) map[attribute.Distinct]metricdata.DataPoint[int64] {
	t.Helper()

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	counts := map[attribute.Distinct]metricdata.DataPoint[int64]{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "payloadcms.client.requests" {
				continue
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				counts[dp.Attributes.Equivalent()] = dp
			}
		}
	}

	return counts
}

func countOf(counts map[attribute.Distinct]metricdata.DataPoint[int64], attrs ...attribute.KeyValue) int64 {
	set := attribute.NewSet(attrs...)
	return counts[set.Equivalent()].Value
}

func TestNew_RecordsPayloadCMSCalls(t *testing.T) {
	reader := withMetricsReader(t)

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/globals/footer" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errors":[{"message":"Not Found"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"64f1a2b3"}`))
	})

	ctx := context.Background()
	_ = c.Raw().Do(ctx, http.MethodGet, "/strutture/64f1a2b3", url.Values{"depth": {"1"}}, nil, nil)
	_ = c.Raw().Do(ctx, http.MethodGet, "/globals/footer", nil, nil, nil)

	counts := requestCounts(t, reader)
	if len(counts) != 2 {
		t.Fatalf("expected two series, got %v", counts)
	}

	if got := countOf(counts,
		semconv.HTTPRequestMethodKey.String("GET"),
		collectionKey.String("strutture"),
		semconv.HTTPResponseStatusCode(http.StatusOK),
	); got != 1 {
		t.Errorf("expected one successful call on strutture, got %d (%v)", got, counts)
	}

	if got := countOf(counts,
		semconv.HTTPRequestMethodKey.String("GET"),
		collectionKey.String("globals/footer"),
		semconv.HTTPResponseStatusCode(http.StatusNotFound),
		semconv.ErrorTypeKey.String("http_404"),
	); got != 1 {
		t.Errorf("expected one failed call on globals/footer, got %d (%v)", got, counts)
	}
}

func TestObserver_CountsEveryRetry(t *testing.T) {
	reader := withMetricsReader(t)

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	cfg := Config{BaseURL: server.URL, APIURL: "/api"}

	raw, err := gopcms.New(
		cfg.BaseURL,
		gopcms.WithAPIPrefix(cfg.APIURL),
		gopcms.WithRetry(gopcms.RetryPolicy{MaxRetries: 2, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond}),
		gopcms.WithObserver(newObserver(cfg)),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := raw.Do(context.Background(), http.MethodGet, "/strutture", nil, nil, nil); err != nil {
		t.Fatalf("expected the retry to succeed, got %v", err)
	}

	counts := requestCounts(t, reader)

	failed := countOf(counts,
		semconv.HTTPRequestMethodKey.String("GET"),
		collectionKey.String("strutture"),
		semconv.HTTPResponseStatusCode(http.StatusServiceUnavailable),
		semconv.ErrorTypeKey.String("http_503"),
	)
	succeeded := countOf(counts,
		semconv.HTTPRequestMethodKey.String("GET"),
		collectionKey.String("strutture"),
		semconv.HTTPResponseStatusCode(http.StatusOK),
	)

	if failed != 1 || succeeded != 1 {
		t.Fatalf("expected each attempt to count once, got %d failed and %d succeeded (%v)", failed, succeeded, counts)
	}
}
