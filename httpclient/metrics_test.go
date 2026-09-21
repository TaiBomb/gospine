package httpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TaiBomb/gospine/config"
	"github.com/TaiBomb/gospine/logging"
	"github.com/TaiBomb/gospine/telemetry"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

func TestNew_RecordsClientMetricsWhenTelemetryIsEnabled(t *testing.T) {
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
	defer func() { _ = shutdown(context.Background()) }()

	var gotID string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID = r.Header.Get(logging.RequestIDHeader)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer ts.Close()

	ctx := logging.ContextWithRequestID(context.Background(), "id-1")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/things/42?q=secret", nil)
	if err != nil {
		t.Fatalf("failed to build the request: %v", err)
	}

	resp, err := New(Options{}).HTTP().Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = resp.Body.Close()

	if gotID != "id-1" {
		t.Errorf("expected the request id to still be forwarded, got %q", gotID)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	var points []metricdata.HistogramDataPoint[float64]
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "http.client.request.duration" {
				points = m.Data.(metricdata.Histogram[float64]).DataPoints
			}
		}
	}

	if len(points) != 1 {
		t.Fatalf("expected one http.client.request.duration data point, got %d", len(points))
	}

	attrs := points[0].Attributes
	if v, _ := attrs.Value(semconv.ServerAddressKey); v.AsString() != "127.0.0.1" {
		t.Errorf("expected server.address 127.0.0.1, got %q", v.Emit())
	}
	if v, _ := attrs.Value(semconv.HTTPResponseStatusCodeKey); v.AsInt64() != http.StatusAccepted {
		t.Errorf("expected status 202, got %q", v.Emit())
	}
	for _, kv := range attrs.ToSlice() {
		if kv.Key == semconv.URLFullKey || kv.Key == semconv.URLPathKey {
			t.Errorf("expected no URL in the metric attributes, got %s=%s", kv.Key, kv.Value.Emit())
		}
	}
}
