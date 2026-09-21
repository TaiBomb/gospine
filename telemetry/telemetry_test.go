package telemetry

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TaiBomb/gospine/config"
	"github.com/TaiBomb/gospine/logging"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// Setup changes process-wide state: no test here runs in parallel, and each
// one undoes its Setup through setup's cleanup.

func TestMain(m *testing.M) {
	logging.InitLogger()
	os.Exit(m.Run())
}

func enabled(exporter string) config.Telemetry {
	return config.Telemetry{
		Enabled:  true,
		Exporter: exporter,
		Path:     "/metrics",
		Interval: time.Hour,
	}
}

func setup(t *testing.T, cfg config.Telemetry, opts ...Option) func(context.Context) error {
	t.Helper()

	shutdown, err := Setup(context.Background(), cfg, Service{Name: "test-service"}, opts...)
	if err != nil {
		t.Fatalf("Setup returned an error: %v", err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	return shutdown
}

func freePort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("cannot find a free port: %v", err)
	}
	defer func() { _ = listener.Close() }()

	return listener.Addr().(*net.TCPAddr).Port
}

func portIsFree(port int) bool {
	listener, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	_ = listener.Close()

	return true
}

func collect(t *testing.T, reader sdkmetric.Reader) metricdata.ResourceMetrics {
	t.Helper()

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	return rm
}

func findMetric(t *testing.T, rm metricdata.ResourceMetrics, name string) metricdata.Metrics {
	t.Helper()

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m
			}
		}
	}

	t.Fatalf("metric %q not collected", name)

	return metricdata.Metrics{}
}

func scrape(t *testing.T, url string) (int, string) {
	t.Helper()

	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("GET %s: cannot read the body: %v", url, err)
	}

	return resp.StatusCode, string(body)
}

var runtimeMetric = regexp.MustCompile(`(?m)^go_\w+`)

func TestSetup_DisabledIsANoOp(t *testing.T) {
	port := freePort(t)
	before := otel.GetMeterProvider()

	// Not even validated while off.
	cfg := config.Telemetry{Enabled: false, Exporter: "bogus", Port: port, Path: "/metrics"}

	shutdown, err := Setup(context.Background(), cfg, Service{Name: "test-service"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if Enabled() {
		t.Error("expected telemetry to stay disabled")
	}
	if _, _, ok := Handler(); ok {
		t.Error("expected no scrape handler")
	}
	if otel.GetMeterProvider() != before {
		t.Error("expected the global meter provider to be left alone")
	}
	if !portIsFree(port) {
		t.Errorf("expected port %d to stay closed", port)
	}

	for range 2 {
		if err := shutdown(context.Background()); err != nil {
			t.Fatalf("expected a no-op shutdown, got %v", err)
		}
	}
}

func TestSetup_InjectedReaderCollectsServiceMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	setup(t, enabled(ExporterPrometheus), WithReader(reader))

	if !Enabled() {
		t.Fatal("expected telemetry to be enabled")
	}
	if _, _, ok := Handler(); ok {
		t.Error("expected nothing to serve with an injected reader")
	}

	counter, err := Meter("test").Int64Counter("test.jobs")
	if err != nil {
		t.Fatalf("cannot create the counter: %v", err)
	}
	counter.Add(context.Background(), 3)

	rm := collect(t, reader)

	sum, ok := findMetric(t, rm, "test.jobs").Data.(metricdata.Sum[int64])
	if !ok || len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != 3 {
		t.Fatalf("expected one data point worth 3, got %+v", sum)
	}

	if name, _ := rm.Resource.Set().Value(semconv.ServiceNameKey); name.AsString() != "test-service" {
		t.Errorf("expected service.name test-service, got %q", name.AsString())
	}
}

func TestSetup_RuntimeMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	setup(t, enabled(ExporterPrometheus), WithReader(reader))

	findMetric(t, collect(t, reader), "go.goroutine.count")
}

func TestSetup_ServiceName(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		env        string
		want       string
	}{
		{"from the service", "", "", "test-service"},
		{"telemetry.serviceName wins over the service", "from-config", "", "from-config"},
		{"OTEL_SERVICE_NAME wins over both", "from-config", "from-env", "from-env"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env != "" {
				t.Setenv("OTEL_SERVICE_NAME", tt.env)
			}

			cfg := enabled(ExporterPrometheus)
			cfg.ServiceName = tt.configured

			reader := sdkmetric.NewManualReader()
			shutdown := setup(t, cfg, WithReader(reader))
			defer func() { _ = shutdown(context.Background()) }()

			name, _ := collect(t, reader).Resource.Set().Value(semconv.ServiceNameKey)
			if name.AsString() != tt.want {
				t.Fatalf("expected service.name %q, got %q", tt.want, name.AsString())
			}
		})
	}
}

func TestSetup_HistogramBoundaries(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	setup(t, enabled(ExporterPrometheus), WithReader(reader))

	meter := Meter("test")
	ctx := context.Background()

	wantLastBound := map[string]float64{
		"http.server.request.duration":       60,
		"http.client.request.duration":       10,
		"payloadcms.client.request.duration": 10,
		"http.server.response.body.size":     64 << 20,
	}

	for name := range wantLastBound {
		histogram, err := meter.Float64Histogram(name)
		if err != nil {
			t.Fatalf("cannot create %s: %v", name, err)
		}
		histogram.Record(ctx, 1)
	}

	rm := collect(t, reader)

	for name, want := range wantLastBound {
		data, ok := findMetric(t, rm, name).Data.(metricdata.Histogram[float64])
		if !ok || len(data.DataPoints) != 1 {
			t.Fatalf("%s: expected one histogram data point, got %+v", name, data)
		}

		bounds := data.DataPoints[0].Bounds
		if got := bounds[len(bounds)-1]; got != want {
			t.Errorf("%s: expected the last bound to be %v, got %v (%v)", name, want, got, bounds)
		}
	}
}

func TestSetup_PrometheusOnADedicatedPort(t *testing.T) {
	port := freePort(t)

	cfg := enabled(ExporterPrometheus)
	cfg.Port = port
	setup(t, cfg)

	if _, _, ok := Handler(); ok {
		t.Error("expected the main server to serve nothing")
	}

	base := "http://127.0.0.1:" + strconv.Itoa(port)

	status, body := scrape(t, base+"/metrics")
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	if !strings.Contains(body, "target_info{") || !strings.Contains(body, `service_name="test-service"`) {
		t.Errorf("expected target_info with the service name, got:\n%s", body)
	}
	if !runtimeMetric.MatchString(body) {
		t.Errorf("expected Go runtime metrics, got:\n%s", body)
	}

	if status, _ := scrape(t, base+"/other"); status != http.StatusNotFound {
		t.Errorf("expected 404 outside the metrics path, got %d", status)
	}
}

func TestSetup_PrometheusOnTheMainServer(t *testing.T) {
	const defaultPort = 9464
	wasFree := portIsFree(defaultPort)

	setup(t, enabled(ExporterPrometheus))

	h, path, ok := Handler()
	if !ok || path != "/metrics" {
		t.Fatalf("expected the handler for /metrics, got ok=%v path=%q", ok, path)
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "target_info{") {
		t.Fatalf("expected the metrics, got %d:\n%s", w.Code, w.Body.String())
	}

	if wasFree && !portIsFree(defaultPort) {
		t.Errorf("expected no listener on port %d", defaultPort)
	}
}

func TestSetup_OTLPPushesOnShutdown(t *testing.T) {
	var (
		pushes atomic.Int32
		path   atomic.Value
	)

	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path.Store(r.URL.Path)
		pushes.Add(1)
	}))
	defer collector.Close()

	cfg := enabled(ExporterOTLP)
	cfg.OTLPEndpoint = collector.URL
	shutdown := setup(t, cfg)

	if _, _, ok := Handler(); ok {
		t.Error("expected nothing to serve with the otlp exporter")
	}

	counter, _ := Meter("test").Int64Counter("test.jobs")
	counter.Add(context.Background(), 1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := shutdown(ctx); err != nil {
		t.Fatalf("shutdown returned an error: %v", err)
	}

	if pushes.Load() == 0 {
		t.Fatal("expected shutdown to push the pending metrics")
	}
	if got := path.Load(); got != "/v1/metrics" {
		t.Errorf("expected the push on /v1/metrics, got %v", got)
	}
}

func TestSetup_RejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*config.Telemetry)
	}{
		{"unknown exporter", func(c *config.Telemetry) { c.Exporter = "statsd" }},
		{"negative port", func(c *config.Telemetry) { c.Port = -1 }},
		{"port out of range", func(c *config.Telemetry) { c.Port = 70000 }},
		{"relative path", func(c *config.Telemetry) { c.Path = "metrics" }},
		{"unclean path", func(c *config.Telemetry) { c.Path = "/metrics/" }},
		{"main server path on /health", func(c *config.Telemetry) { c.Path = "/health" }},
		{"main server path on /status", func(c *config.Telemetry) { c.Path = "/status" }},
		{"main server path on /api", func(c *config.Telemetry) { c.Path = "/api" }},
		{"main server path under /api", func(c *config.Telemetry) { c.Path = "/api/metrics" }},
		{"otlp without interval", func(c *config.Telemetry) { c.Exporter, c.Interval = ExporterOTLP, 0 }},
		{"otlp endpoint without scheme", func(c *config.Telemetry) { c.Exporter, c.OTLPEndpoint = ExporterOTLP, "collector:4318" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := enabled(ExporterPrometheus)
			tt.mutate(&cfg)

			if _, err := Setup(context.Background(), cfg, Service{Name: "test-service"}); err == nil {
				t.Fatal("expected an error")
			}
			if Enabled() {
				t.Fatal("expected telemetry to stay disabled after a failed Setup")
			}
		})
	}
}

func TestSetup_BusyPortFails(t *testing.T) {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("cannot listen: %v", err)
	}
	defer func() { _ = listener.Close() }()

	cfg := enabled(ExporterPrometheus)
	cfg.Port = listener.Addr().(*net.TCPAddr).Port

	if _, err := Setup(context.Background(), cfg, Service{Name: "test-service"}); err == nil {
		t.Fatal("expected an error on a busy port")
	}
	if Enabled() {
		t.Fatal("expected telemetry to stay disabled")
	}
}

func TestSetup_Twice(t *testing.T) {
	setup(t, enabled(ExporterPrometheus), WithReader(sdkmetric.NewManualReader()))

	if _, err := Setup(context.Background(), enabled(ExporterPrometheus), Service{Name: "again"}); err == nil {
		t.Fatal("expected a second Setup to fail")
	}
	if !Enabled() {
		t.Fatal("expected the first Setup to stay in place")
	}
}

func TestShutdown_IsIdempotent(t *testing.T) {
	shutdown := setup(t, enabled(ExporterPrometheus), WithReader(sdkmetric.NewManualReader()))

	for range 2 {
		if err := shutdown(context.Background()); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	}

	if Enabled() {
		t.Fatal("expected telemetry to be disabled after shutdown")
	}

	// A new Setup is possible once the previous one is shut down.
	setup(t, enabled(ExporterPrometheus), WithReader(sdkmetric.NewManualReader()))
}
