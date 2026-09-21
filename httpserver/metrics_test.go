package httpserver

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/TaiBomb/gospine/config"
	"github.com/TaiBomb/gospine/telemetry"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/sse"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// setupTelemetry enables telemetry for the test only. It is process-wide
// state: the tests of this package never run in parallel.
func setupTelemetry(t *testing.T, cfg config.Telemetry, opts ...telemetry.Option) {
	t.Helper()

	cfg.Enabled = true
	if cfg.Exporter == "" {
		cfg.Exporter = telemetry.ExporterPrometheus
	}
	if cfg.Path == "" {
		cfg.Path = "/metrics"
	}

	shutdown, err := telemetry.Setup(context.Background(), cfg, telemetry.Service{Name: "test-service"}, opts...)
	if err != nil {
		t.Fatalf("telemetry setup failed: %v", err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })
}

func withMetricsReader(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	setupTelemetry(t, config.Telemetry{}, telemetry.WithReader(reader))

	return reader
}

func collectMetrics(t *testing.T, reader sdkmetric.Reader) metricdata.ResourceMetrics {
	t.Helper()

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	return rm
}

func metricData(rm metricdata.ResourceMetrics, name string) (metricdata.Aggregation, bool) {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m.Data, true
			}
		}
	}

	return nil, false
}

// durationPoints returns the attribute sets of http.server.request.duration.
func durationPoints(t *testing.T, rm metricdata.ResourceMetrics) []attribute.Set {
	t.Helper()

	data, found := metricData(rm, "http.server.request.duration")
	if !found {
		return nil
	}

	histogram, ok := data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("unexpected data type %T", data)
	}

	var sets []attribute.Set
	for _, dp := range histogram.DataPoints {
		sets = append(sets, dp.Attributes)
	}

	return sets
}

func attr(set attribute.Set, key attribute.Key) (string, bool) {
	v, ok := set.Value(key)
	return v.Emit(), ok
}

func TestMetrics_RouteIsTheTemplate(t *testing.T) {
	reader := withMetricsReader(t)

	s := newTestServer(Options{Config: config.Server{ContextPath: "/svc"}})
	serve(s.Engine(), httptest.NewRequest(http.MethodGet, "/svc/api/shop/items/42", nil))

	points := durationPoints(t, collectMetrics(t, reader))
	if len(points) != 1 {
		t.Fatalf("expected one data point, got %v", points)
	}

	want := map[attribute.Key]string{
		semconv.HTTPRouteKey:              "/svc/api/shop/items/:id",
		semconv.HTTPRequestMethodKey:      "GET",
		semconv.HTTPResponseStatusCodeKey: "200",
		semconv.URLSchemeKey:              "http",
	}
	for key, value := range want {
		if got, _ := attr(points[0], key); got != value {
			t.Errorf("expected %s=%q, got %q", key, value, got)
		}
	}
	if _, found := attr(points[0], semconv.ErrorTypeKey); found {
		t.Error("expected no error.type on a 200")
	}
}

func TestMetrics_ProbesAreLeftOut(t *testing.T) {
	reader := withMetricsReader(t)

	s := newTestServer(Options{Config: config.Server{ContextPath: "/svc"}})
	serve(s.Engine(), httptest.NewRequest(http.MethodGet, "/svc/health", nil))
	serve(s.Engine(), httptest.NewRequest(http.MethodGet, "/svc/status", nil))

	rm := collectMetrics(t, reader)

	for _, name := range []string{"http.server.request.duration", "http.server.active_requests"} {
		if _, found := metricData(rm, name); found {
			t.Errorf("expected no %s for the probes", name)
		}
	}
}

func TestMetrics_RecoveredPanicIsA500(t *testing.T) {
	reader := withMetricsReader(t)

	s := newTestServer(Options{Config: config.Server{ContextPath: "/"}})
	s.Engine().GET("/boom", func(*gin.Context) { panic("boom") })

	serve(s.Engine(), httptest.NewRequest(http.MethodGet, "/boom", nil))

	points := durationPoints(t, collectMetrics(t, reader))
	if len(points) != 1 {
		t.Fatalf("expected one data point, got %v", points)
	}
	if got, _ := attr(points[0], semconv.HTTPResponseStatusCodeKey); got != "500" {
		t.Errorf("expected status 500, got %q", got)
	}
	if got, _ := attr(points[0], semconv.ErrorTypeKey); got != "500" {
		t.Errorf("expected error.type 500, got %q", got)
	}
}

func TestMetrics_UnroutedRequestsCarryNoRoute(t *testing.T) {
	reader := withMetricsReader(t)

	s := newTestServer(Options{Config: config.Server{ContextPath: "/"}})
	serve(s.Engine(), httptest.NewRequest(http.MethodGet, "/wp-admin/setup.php", nil))
	serve(s.Engine(), httptest.NewRequest("FOO", "/api/shop/items/1", nil))

	points := durationPoints(t, collectMetrics(t, reader))
	if len(points) != 2 {
		t.Fatalf("expected two data points, got %v", points)
	}

	var methods []string
	for _, set := range points {
		if route, found := attr(set, semconv.HTTPRouteKey); found {
			t.Errorf("expected no http.route, got %q", route)
		}
		if got, _ := attr(set, semconv.HTTPResponseStatusCodeKey); got != "404" {
			t.Errorf("expected status 404, got %q", got)
		}
		method, _ := attr(set, semconv.HTTPRequestMethodKey)
		methods = append(methods, method)
	}

	slices.Sort(methods)
	if !slices.Equal(methods, []string{"GET", "_OTHER"}) {
		t.Errorf("expected an unknown method to be recorded as _OTHER, got %v", methods)
	}
}

func TestMetrics_EventStreamsStayOutOfTheDuration(t *testing.T) {
	reader := withMetricsReader(t)

	s := newTestServer(Options{
		Config: config.Server{ContextPath: "/"},
		Modules: []Module{{
			Prefix: "/stream",
			Register: func(api huma.API) {
				sse.Register(api, huma.Operation{
					OperationID: "streamTicks",
					Method:      http.MethodPost,
					Path:        "/ticks",
				}, map[string]any{"tick": tick{}}, func(ctx context.Context, _ *struct{}, send sse.Sender) {
					_ = send.Data(tick{N: 1})
					_ = send.Data(tick{N: 2})
				})
			},
		}},
	})

	ts := httptest.NewServer(s.Engine())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/stream/ticks", "application/json", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, _ = io.Copy(io.Discard, bufio.NewReader(resp.Body))
	_ = resp.Body.Close()

	rm := collectMetrics(t, reader)

	if points := durationPoints(t, rm); len(points) != 0 {
		t.Errorf("expected the stream out of the duration, got %v", points)
	}
	if _, found := metricData(rm, "http.server.response.body.size"); found {
		t.Error("expected the stream out of the response size")
	}

	data, found := metricData(rm, "http.server.request.body.size")
	if !found {
		t.Fatal("expected the stream to be counted in the request size")
	}
	if dp := data.(metricdata.Histogram[int64]).DataPoints; len(dp) != 1 || dp[0].Count != 1 {
		t.Errorf("expected one request, got %+v", dp)
	}

	data, found = metricData(rm, "http.server.active_requests")
	if !found {
		t.Fatal("expected the stream to be counted in active_requests")
	}
	for _, dp := range data.(metricdata.Sum[int64]).DataPoints {
		if dp.Value != 0 {
			t.Errorf("expected no active request once the stream is over, got %d", dp.Value)
		}
	}
}

func TestMetrics_RegisteredOnlyWhenEnabled(t *testing.T) {
	builtIns := func() int {
		return len(newTestServer(Options{Config: config.Server{ContextPath: "/"}}).Engine().Handlers)
	}

	disabled := builtIns()

	withMetricsReader(t)

	if enabled := builtIns(); enabled != disabled+1 {
		t.Errorf("expected one more middleware with telemetry enabled: %d, then %d", disabled, enabled)
	}

	s := newTestServer(Options{Config: config.Server{ContextPath: "/"}, DisableMetrics: true})
	if got := len(s.Engine().Handlers); got != disabled {
		t.Errorf("expected DisableMetrics to leave the middleware out, got %d handlers", got)
	}
}

func TestMetrics_ScrapeOnTheMainServer(t *testing.T) {
	tests := []struct {
		contextPath string
		scrapePath  string
		apiPath     string
		route       string
	}{
		{"/", "/metrics", "/api/shop/items/1", "/api/shop/items/:id"},
		{"/svc", "/svc/metrics", "/svc/api/shop/items/1", "/svc/api/shop/items/:id"},
	}

	for _, tt := range tests {
		t.Run(tt.contextPath, func(t *testing.T) {
			setupTelemetry(t, config.Telemetry{Port: 0})
			buf := captureLogs(t)

			s := newTestServer(Options{Config: config.Server{ContextPath: tt.contextPath}})

			serve(s.Engine(), httptest.NewRequest(http.MethodGet, tt.apiPath, nil))
			serve(s.Engine(), httptest.NewRequest(http.MethodGet, tt.scrapePath, nil))
			w := serve(s.Engine(), httptest.NewRequest(http.MethodGet, tt.scrapePath, nil))

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", w.Code)
			}

			body := w.Body.String()
			if !strings.Contains(body, `http_server_request_duration_seconds_bucket{`) ||
				!strings.Contains(body, `http_route="`+tt.route+`"`) {
				t.Errorf("expected the API call in the Prometheus format, got:\n%s", body)
			}
			if strings.Contains(body, `http_route="`+tt.scrapePath+`"`) {
				t.Error("expected the scrape to stay out of the metrics")
			}

			for path := range s.API().OpenAPI().Paths {
				if strings.Contains(path, "metrics") {
					t.Errorf("expected the scrape to stay out of the OpenAPI document, got %q", path)
				}
			}

			for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
				if strings.Contains(line, `"msg":"Request handled"`) && strings.Contains(line, `"path":"`+tt.scrapePath+`"`) &&
					!strings.Contains(line, `"level":"DEBUG"`) {
					t.Errorf("expected the scrape to be logged at DEBUG, got %s", line)
				}
			}
		})
	}
}

func TestMetrics_DedicatedPortLeavesTheMainServerAlone(t *testing.T) {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("cannot find a free port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	setupTelemetry(t, config.Telemetry{Port: port})

	s := newTestServer(Options{Config: config.Server{ContextPath: "/"}})

	if w := serve(s.Engine(), httptest.NewRequest(http.MethodGet, "/metrics", nil)); w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on the main server, got %d", w.Code)
	}
}

func TestMetrics_Standalone(t *testing.T) {
	reader := withMetricsReader(t)

	engine := gin.New()
	engine.Use(Metrics(WithoutRoutes("/ping")))
	engine.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })
	engine.GET("/things/:id", func(c *gin.Context) { c.String(http.StatusCreated, "made") })

	serve(engine, httptest.NewRequest(http.MethodGet, "/ping", nil))
	serve(engine, httptest.NewRequest(http.MethodGet, "/things/7", nil))

	rm := collectMetrics(t, reader)

	points := durationPoints(t, rm)
	if len(points) != 1 {
		t.Fatalf("expected one data point, got %v", points)
	}
	if got, _ := attr(points[0], semconv.HTTPRouteKey); got != "/things/:id" {
		t.Errorf("expected the route template, got %q", got)
	}

	data, _ := metricData(rm, "http.server.response.body.size")
	if dp := data.(metricdata.Histogram[int64]).DataPoints; len(dp) != 1 || dp[0].Sum != int64(len("made")) {
		t.Errorf("expected the response size to be recorded, got %+v", dp)
	}
}
