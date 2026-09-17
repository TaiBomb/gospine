package httpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/TaiBomb/gospine/apidoc"
	"github.com/TaiBomb/gospine/config"
	"github.com/TaiBomb/gospine/logging"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/sse"
	"github.com/gin-gonic/gin"
)

type itemOutput struct {
	Body struct {
		ID string `json:"id"`
	}
}

func registerItems(api huma.API) { registerItemsAs("getItem")(api) }

func registerItemsAs(operationID string) func(huma.API) {
	return func(api huma.API) {
		huma.Register(api, huma.Operation{
			OperationID: operationID,
			Method:      http.MethodGet,
			Path:        "/items/{id}",
			Summary:     "Get an item",
		}, func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*itemOutput, error) {
			out := &itemOutput{}
			out.Body.ID = in.ID
			return out, nil
		})
	}
}

func newTestServer(opts Options) *Server {
	if opts.Config.PingTimeout == 0 {
		opts.Config.PingTimeout = time.Second
	}
	if opts.Modules == nil {
		opts.Modules = []Module{{Prefix: "/shop", Tag: "Shop", Register: registerItems}}
	}

	return New(opts)
}

func fetchJSON(t *testing.T, s *Server, path string) map[string]any {
	t.Helper()

	w := serve(s.Engine(), httptest.NewRequest(http.MethodGet, path, nil))

	if w.Code != http.StatusOK {
		t.Fatalf("GET %s: expected status %d, got %d (body: %s)", path, http.StatusOK, w.Code, w.Body.String())
	}

	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("GET %s: failed to decode the body: %v", path, err)
	}

	return doc
}

func TestNew_RegistersExpectedRoutes(t *testing.T) {
	tests := []struct {
		contextPath string
		want        []string
	}{
		{"/", []string{"GET /health", "GET /status", "GET /api/shop/items/:id", "GET /api/openapi.json"}},
		{"/svc", []string{"GET /svc/health", "GET /svc/status", "GET /svc/api/shop/items/:id", "GET /svc/api/openapi.json"}},
	}

	for _, tt := range tests {
		t.Run(tt.contextPath, func(t *testing.T) {
			s := newTestServer(Options{Config: config.Server{ContextPath: tt.contextPath}})

			var got []string
			for _, r := range s.Engine().Routes() {
				got = append(got, r.Method+" "+r.Path)
			}

			for _, route := range tt.want {
				if !slices.Contains(got, route) {
					t.Errorf("expected route %q to be registered, got %v", route, got)
				}
			}
		})
	}
}

func TestNew_LogsEveryRoute(t *testing.T) {
	buf := captureLogs(t)

	s := newTestServer(Options{Config: config.Server{ContextPath: "/"}})

	lines := strings.Count(buf.String(), `"msg":"Route registered"`)
	if lines != len(s.Engine().Routes()) {
		t.Fatalf("expected one line per route (%d), got %d", len(s.Engine().Routes()), lines)
	}
}

func TestNew_ServesModuleOperations(t *testing.T) {
	s := newTestServer(Options{Config: config.Server{ContextPath: "/svc"}})

	body := fetchJSON(t, s, "/svc/api/shop/items/42")

	if body["id"] != "42" {
		t.Fatalf("expected the module handler to answer, got %v", body)
	}
}

func TestNew_ModulesAreTagged(t *testing.T) {
	s := newTestServer(Options{
		Config: config.Server{ContextPath: "/"},
		Modules: []Module{
			{Prefix: "/shop", Tag: "Shop", Register: registerItems},
			{Prefix: "/plain", Register: registerItemsAs("getPlainItem")},
		},
	})

	doc := s.API().OpenAPI()

	if got := doc.Paths["/shop/items/{id}"].Get.Tags; !slices.Equal(got, []string{"Shop"}) {
		t.Errorf("expected the Shop tag, got %v", got)
	}
	if got := doc.Paths["/plain/items/{id}"].Get.Tags; len(got) != 0 {
		t.Errorf("expected no tag on an untagged module, got %v", got)
	}
}

func TestNew_MiddlewareScopes(t *testing.T) {
	var calls []string

	record := func(name string) gin.HandlerFunc {
		return func(c *gin.Context) {
			calls = append(calls, name+" "+c.Request.URL.Path)
			c.Next()
		}
	}

	s := newTestServer(Options{
		Config:         config.Server{ContextPath: "/"},
		Middlewares:    []gin.HandlerFunc{record("all")},
		APIMiddlewares: []gin.HandlerFunc{record("api")},
	})

	serve(s.Engine(), httptest.NewRequest(http.MethodGet, "/health", nil))
	serve(s.Engine(), httptest.NewRequest(http.MethodGet, "/api/shop/items/1", nil))

	want := []string{"all /health", "all /api/shop/items/1", "api /api/shop/items/1"}
	if !slices.Equal(calls, want) {
		t.Fatalf("expected %v, got %v", want, calls)
	}
}

// TestNew_MiddlewaresRunAfterTheBuiltIns covers middlewares that answer on
// their own, like a CORS preflight: the answer is still logged and carries an id.
func TestNew_MiddlewaresRunAfterTheBuiltIns(t *testing.T) {
	buf := captureLogs(t)

	s := newTestServer(Options{
		Config: config.Server{ContextPath: "/"},
		Middlewares: []gin.HandlerFunc{func(c *gin.Context) {
			c.AbortWithStatus(http.StatusNoContent)
		}},
	})

	w := serve(s.Engine(), httptest.NewRequest(http.MethodOptions, "/api/shop/items/1", nil))

	if w.Header().Get(logging.RequestIDHeader) == "" {
		t.Error("expected the early answer to carry a request id")
	}
	if got := logLine(t, buf, "Request handled")["status"]; got != float64(http.StatusNoContent) {
		t.Errorf("expected the early answer to be logged, got status %v", got)
	}
}

func TestNew_ProbesAreQuiet(t *testing.T) {
	buf := captureLogs(t)

	s := newTestServer(Options{
		Config: config.Server{ContextPath: "/svc"},
		Status: Status{Probe: ProbeFunc(func(context.Context) error { return errors.New("down") })},
	})

	serve(s.Engine(), httptest.NewRequest(http.MethodGet, "/svc/status", nil))

	if got := logLine(t, buf, "Request handled")["level"]; got != "DEBUG" {
		t.Fatalf("expected /status to be logged at DEBUG even when failing, got %v", got)
	}
}

func TestServer_StartAndShutdown(t *testing.T) {
	s := newTestServer(Options{
		Config: config.Server{
			ContextPath:  "/",
			Port:         0,
			ReadTimeout:  time.Second,
			WriteTimeout: time.Second,
		},
	})

	if err := s.Start(); err != nil {
		t.Fatalf("Start returned an error: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown returned an error: %v", err)
	}
}

func TestServer_ShutdownWithoutStart(t *testing.T) {
	s := newTestServer(Options{Config: config.Server{ContextPath: "/"}})

	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestHealthHandler(t *testing.T) {
	s := newTestServer(Options{Config: config.Server{ContextPath: "/"}})

	body := fetchJSON(t, s, "/health")

	if body["status"] != "ok" {
		t.Fatalf("unexpected status: %v", body["status"])
	}
	if _, found := body["error"]; found {
		t.Fatalf("expected no error field, got %v", body["error"])
	}

	ts, _ := body["timestamp"].(string)
	if parsed, err := time.Parse(time.RFC3339Nano, ts); err != nil || parsed.Location() != time.UTC {
		t.Fatalf("expected a UTC timestamp, got %q", ts)
	}
}

func TestStatusHandler_Reachable(t *testing.T) {
	var hadDeadline bool

	s := newTestServer(Options{
		Config: config.Server{ContextPath: "/"},
		Status: Status{Probe: ProbeFunc(func(ctx context.Context) error {
			_, hadDeadline = ctx.Deadline()
			return nil
		})},
	})

	body := fetchJSON(t, s, "/status")

	if body["status"] != "ok" {
		t.Fatalf("unexpected status: %v", body["status"])
	}
	if !hadDeadline {
		t.Fatal("expected the ping to run within PingTimeout")
	}
}

func TestStatusHandler_Unreachable(t *testing.T) {
	buf := captureLogs(t)

	s := newTestServer(Options{
		Config: config.Server{ContextPath: "/"},
		Status: Status{
			Probe:      ProbeFunc(func(context.Context) error { return errors.New("connection refused") }),
			Dependency: "upstream",
			ErrorFields: func(err error, extra ...any) []any {
				return append([]any{"error", err, "statusCode", 503}, extra...)
			},
		},
	})

	w := serve(s.Engine(), httptest.NewRequest(http.MethodGet, "/status", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, w.Code)
	}

	var body HealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body.Status != "error" || body.Error != "upstream unreachable" {
		t.Fatalf("unexpected body: %+v", body)
	}

	line := logLine(t, buf, "Status check failed: upstream unreachable")
	if line["level"] != "ERROR" || line["error"] != "connection refused" || line["statusCode"] != float64(503) {
		t.Fatalf("unexpected log line: %v", line)
	}
}

func TestStatusHandler_UnreachableWithDefaults(t *testing.T) {
	buf := captureLogs(t)

	s := newTestServer(Options{
		Config: config.Server{ContextPath: "/"},
		Status: Status{Probe: ProbeFunc(func(context.Context) error { return errors.New("boom") })},
	})

	w := serve(s.Engine(), httptest.NewRequest(http.MethodGet, "/status", nil))

	if !strings.Contains(w.Body.String(), `"error":"dependency unreachable"`) {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
	if got := logLine(t, buf, "Status check failed: dependency unreachable")["error"]; got != "boom" {
		t.Fatalf("expected the error to be logged, got %v", got)
	}
}

func TestStatusHandler_WithoutProbeAnswersLikeHealth(t *testing.T) {
	s := newTestServer(Options{Config: config.Server{ContextPath: "/"}})

	if body := fetchJSON(t, s, "/status"); body["status"] != "ok" {
		t.Fatalf("unexpected status: %v", body["status"])
	}
}

func TestOpenAPIDocument_IsServedInBothVersions(t *testing.T) {
	s := newTestServer(Options{Config: config.Server{ContextPath: "/"}})

	tests := []struct {
		path        string
		wantVersion string
	}{
		{"/api/openapi.json", "3.1.0"},
		{"/api/openapi-3.0.json", "3.0.3"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := fetchJSON(t, s, tt.path)["openapi"]; got != tt.wantVersion {
				t.Fatalf("expected OpenAPI version %q, got %v", tt.wantVersion, got)
			}
		})
	}
}

func TestOpenAPIDocument_DeclaresTheAPIBasePathAsItsServer(t *testing.T) {
	tests := []struct {
		contextPath string
		wantServer  string
	}{
		{"/", "/api"},
		{"/svc", "/svc/api"},
	}

	for _, tt := range tests {
		t.Run(tt.contextPath, func(t *testing.T) {
			s := newTestServer(Options{Config: config.Server{ContextPath: tt.contextPath}})

			doc := fetchJSON(t, s, tt.wantServer+"/openapi.json")

			servers, _ := doc["servers"].([]any)
			if len(servers) == 0 {
				t.Fatalf("expected the document to declare a server, got %v", doc["servers"])
			}

			server, _ := servers[0].(map[string]any)
			if got := server["url"]; got != tt.wantServer {
				t.Fatalf("expected server url %q, got %v", tt.wantServer, got)
			}
		})
	}
}

func TestOpenAPIDocument_UsesTheInfoAndLeavesProbesOut(t *testing.T) {
	s := newTestServer(Options{
		Config: config.Server{ContextPath: "/"},
		APIDoc: apidoc.Info{Title: "Shop API", Version: "2.0.0", Description: "Sells things."},
	})

	doc := fetchJSON(t, s, "/api/openapi.json")

	info, _ := doc["info"].(map[string]any)
	if info["title"] != "Shop API" || info["version"] != "2.0.0" || info["description"] != "Sells things." {
		t.Errorf("unexpected info: %v", info)
	}

	paths, _ := doc["paths"].(map[string]any)
	if _, found := paths["/shop/items/{id}"]; !found {
		t.Errorf("expected the module operation to be documented, got %v", paths)
	}
	for _, path := range []string{"/health", "/status"} {
		if _, found := paths[path]; found {
			t.Errorf("expected %q to stay out of the document", path)
		}
	}
}

func TestOpenAPIDocument_BodiesCarryNoSchemaLink(t *testing.T) {
	s := newTestServer(Options{Config: config.Server{ContextPath: "/"}})

	if body := fetchJSON(t, s, "/api/shop/items/1"); body["$schema"] != nil {
		t.Fatalf("expected no $schema property, got %v", body)
	}
}

type tick struct {
	N int `json:"n"`
}

// TestSSE_IsNotBuffered guards the response recorder: each event must reach
// the caller while the handler is still running.
func TestSSE_IsNotBuffered(t *testing.T) {
	release := make(chan struct{})

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
					<-release
					_ = send.Data(tick{N: 2})
				})
			},
		}},
	})

	ts := httptest.NewServer(s.Engine())
	defer ts.Close()
	defer close(release)

	resp, err := http.Post(ts.URL+"/api/stream/ticks", "application/json", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	firstEvent := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			if line := scanner.Text(); strings.HasPrefix(line, "data:") {
				firstEvent <- line
				return
			}
		}
	}()

	select {
	case line := <-firstEvent:
		if !strings.Contains(line, `"n":1`) {
			t.Fatalf("unexpected first event: %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the first event did not arrive before the handler finished: the stream is buffered")
	}
}

// TestSSE_OutlivesTheServerWriteTimeout guards the recorder's Unwrap: without
// it Huma cannot refresh the write deadline and the stream is cut.
func TestSSE_OutlivesTheServerWriteTimeout(t *testing.T) {
	const writeTimeout = 200 * time.Millisecond

	s := newTestServer(Options{
		Config: config.Server{ContextPath: "/"},
		Modules: []Module{{
			Prefix: "/stream",
			Register: func(api huma.API) {
				sse.Register(api, huma.Operation{
					OperationID: "streamSlowTicks",
					Method:      http.MethodPost,
					Path:        "/slow",
				}, map[string]any{"tick": tick{}}, func(ctx context.Context, _ *struct{}, send sse.Sender) {
					for n := range 5 {
						if err := send.Data(tick{N: n}); err != nil {
							return
						}
						time.Sleep(writeTimeout)
					}
				})
			},
		}},
	})

	ts := httptest.NewUnstartedServer(s.Engine())
	ts.Config.WriteTimeout = writeTimeout
	ts.Start()
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/stream/slow", "application/json", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	events := 0
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data:") {
			events++
		}
	}

	if events != 5 {
		t.Fatalf("expected all 5 events past the server write timeout, got %d", events)
	}
}

func TestResponseRecorder_Unwrap(t *testing.T) {
	engine := gin.New()
	engine.Use(RequestLogger())
	engine.GET("/thing", func(c *gin.Context) {
		if _, ok := c.Writer.(interface{ Unwrap() http.ResponseWriter }); !ok {
			t.Error("expected the writer seen by handlers to unwrap")
		}
		if err := http.NewResponseController(c.Writer).SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
			t.Errorf("expected the write deadline to reach the connection, got %v", err)
		}
	})

	ts := httptest.NewServer(engine)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = resp.Body.Close()
}

func TestNew_ProbeHandlersCanBeReplaced(t *testing.T) {
	buf := captureLogs(t)

	custom := func(name string) gin.HandlerFunc {
		return func(c *gin.Context) {
			c.JSON(http.StatusTeapot, gin.H{"handler": name})
		}
	}

	s := newTestServer(Options{
		Config:        config.Server{ContextPath: "/svc"},
		Status:        Status{Probe: ProbeFunc(func(context.Context) error { t.Error("the built-in probe must not run"); return nil })},
		HealthHandler: custom("health"),
		StatusHandler: custom("status"),
	})

	for _, name := range []string{"health", "status"} {
		w := serve(s.Engine(), httptest.NewRequest(http.MethodGet, "/svc/"+name, nil))

		if w.Code != http.StatusTeapot || !strings.Contains(w.Body.String(), name) {
			t.Errorf("expected the custom %s handler to answer, got %d %s", name, w.Code, w.Body.String())
		}
	}

	// The access log still treats the replaced probes as quiet.
	if got := logLine(t, buf, "Request handled")["level"]; got != "DEBUG" {
		t.Errorf("expected a replaced probe to stay at DEBUG, got %v", got)
	}
}

// TestNewStatusHandler_CanBeComposed covers a service wrapping the built-in
// handler, e.g. to check a second dependency first.
func TestNewStatusHandler_CanBeComposed(t *testing.T) {
	builtIn := NewStatusHandler(time.Second, Status{Probe: ProbeFunc(func(context.Context) error { return nil })})

	engine := gin.New()
	engine.GET("/status", func(c *gin.Context) {
		if c.Query("second") == "down" {
			c.JSON(http.StatusServiceUnavailable, HealthResponse{Status: "error", Error: "search unreachable"})
			return
		}
		builtIn(c)
	})

	if w := serve(engine, httptest.NewRequest(http.MethodGet, "/status", nil)); w.Code != http.StatusOK {
		t.Errorf("expected the built-in handler to answer 200, got %d", w.Code)
	}
	if w := serve(engine, httptest.NewRequest(http.MethodGet, "/status?second=down", nil)); w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected the wrapper to answer 503, got %d", w.Code)
	}
}

func TestNewStatusHandler_WithoutTimeout(t *testing.T) {
	var hadDeadline bool

	engine := gin.New()
	engine.GET("/status", NewStatusHandler(0, Status{Probe: ProbeFunc(func(ctx context.Context) error {
		_, hadDeadline = ctx.Deadline()
		return nil
	})}))

	if w := serve(engine, httptest.NewRequest(http.MethodGet, "/status", nil)); w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if hadDeadline {
		t.Fatal("expected no deadline with a zero ping timeout")
	}
}
