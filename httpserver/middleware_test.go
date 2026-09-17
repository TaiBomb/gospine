package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/TaiBomb/gospine/logging"
	"github.com/gin-gonic/gin"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	logging.InitLogger()
	os.Exit(m.Run())
}

// captureLogs swaps the global logger for one writing every level into a
// buffer, restoring the original once the test is over.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer

	previous := logging.Log
	logging.Log = &logging.CustomLogger{
		Logger: slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		})),
	}

	t.Cleanup(func() { logging.Log = previous })

	return &buf
}

// logLine returns the first captured log entry carrying msg, decoded.
func logLine(t *testing.T, buf *bytes.Buffer, msg string) map[string]any {
	t.Helper()

	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}

		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("failed to decode the log line %q: %v", line, err)
		}

		if entry["msg"] == msg {
			return entry
		}
	}

	t.Fatalf("expected a log line saying %q, got:\n%s", msg, buf.String())

	return nil
}

func serve(engine http.Handler, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	return w
}

func TestRequestLogger_PassesThroughToNextHandler(t *testing.T) {
	engine := gin.New()
	engine.Use(RequestLogger())
	engine.GET("/ping", func(c *gin.Context) {
		c.String(http.StatusTeapot, "pong")
	})

	w := serve(engine, httptest.NewRequest(http.MethodGet, "/ping", nil))

	if w.Code != http.StatusTeapot {
		t.Fatalf("expected status %d, got %d", http.StatusTeapot, w.Code)
	}
	if w.Body.String() != "pong" {
		t.Fatalf("expected body %q, got %q", "pong", w.Body.String())
	}
}

// TestRequestLogger_LevelFollowsTheStatus pins what the middleware exists for:
// a failed request is findable by level alone.
func TestRequestLogger_LevelFollowsTheStatus(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		wantLevel string
	}{
		{"a served request is debug noise", http.StatusOK, "DEBUG"},
		{"a rejected request warns", http.StatusBadRequest, "WARN"},
		{"a missing resource warns", http.StatusNotFound, "WARN"},
		{"a failure of ours is an error", http.StatusInternalServerError, "ERROR"},
		{"a failing upstream is an error", http.StatusBadGateway, "ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := captureLogs(t)

			engine := gin.New()
			engine.Use(RequestLogger())
			engine.GET("/thing", func(c *gin.Context) {
				c.Status(tt.status)
			})

			serve(engine, httptest.NewRequest(http.MethodGet, "/thing", nil))

			entry := logLine(t, buf, "Request handled")

			if got := entry["level"]; got != tt.wantLevel {
				t.Errorf("expected level %q for status %d, got %v", tt.wantLevel, tt.status, got)
			}
			if got := entry["status"]; got != float64(tt.status) {
				t.Errorf("expected the line to carry status %d, got %v", tt.status, got)
			}
		})
	}
}

// TestRequestLogger_QuietPathsStayAtDebug covers the probes: they are polled
// constantly and would flood the log whenever a dependency blinks.
func TestRequestLogger_QuietPathsStayAtDebug(t *testing.T) {
	buf := captureLogs(t)

	engine := gin.New()
	engine.Use(RequestLogger(WithQuietPaths("/health")))
	engine.GET("/health", func(c *gin.Context) {
		c.Status(http.StatusServiceUnavailable)
	})

	serve(engine, httptest.NewRequest(http.MethodGet, "/health", nil))

	if got := logLine(t, buf, "Request handled")["level"]; got != "DEBUG" {
		t.Fatalf("expected a quiet route to stay at DEBUG, got %v", got)
	}
}

func TestRequestLogger_FieldsOfASimpleRequest(t *testing.T) {
	buf := captureLogs(t)

	engine := gin.New()
	engine.Use(RequestLogger())
	engine.GET("/thing", func(c *gin.Context) {})

	serve(engine, httptest.NewRequest(http.MethodGet, "/thing", nil))

	entry := logLine(t, buf, "Request handled")

	if got := entry["resp_size_bytes"]; got != float64(0) {
		t.Errorf("expected resp_size_bytes 0 for an empty response, got %v", got)
	}
	if got := entry["method"]; got != http.MethodGet {
		t.Errorf("expected method GET, got %v", got)
	}
	if got := entry["path"]; got != "/thing" {
		t.Errorf("expected path /thing, got %v", got)
	}

	for _, absent := range []string{"query", "path_params", "req_size_bytes", "req_body", "req_body_truncated", "gin_errors", "resp_body"} {
		if got, found := entry[absent]; found {
			t.Errorf("expected no %s field, got %v", absent, got)
		}
	}
}

func TestRequestLogger_ReportsLatencyInBothForms(t *testing.T) {
	buf := captureLogs(t)

	engine := gin.New()
	engine.Use(RequestLogger())
	engine.GET("/slow", func(c *gin.Context) {
		time.Sleep(5 * time.Millisecond)
		c.Status(http.StatusOK)
	})

	serve(engine, httptest.NewRequest(http.MethodGet, "/slow", nil))

	entry := logLine(t, buf, "Request handled")

	readable, _ := entry["latency"].(string)
	if readable == "" || !strings.ContainsAny(readable, "smµn") {
		t.Errorf("expected a human readable latency, got %v", entry["latency"])
	}

	if milliseconds, _ := entry["latency_ms"].(float64); milliseconds < 5 {
		t.Errorf("expected latency_ms to be at least 5, got %v", entry["latency_ms"])
	}
}

func TestRequestLogger_LogsTheRequestBodyAndLeavesItReadable(t *testing.T) {
	buf := captureLogs(t)

	var seen string

	engine := gin.New()
	engine.Use(RequestLogger())
	engine.POST("/items", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			t.Errorf("the handler could not read the body: %v", err)
		}

		seen = string(body)
		c.Status(http.StatusOK)
	})

	sent := "{\n  \"page\": 2,\n  \"limit\": 10\n}"
	req := httptest.NewRequest(http.MethodPost, "/items", strings.NewReader(sent))
	req.Header.Set("Content-Type", "application/json")

	serve(engine, req)

	if seen != sent {
		t.Fatalf("the handler got %q, expected the body to reach it untouched", seen)
	}

	entry := logLine(t, buf, "Request handled")

	if got := entry["req_body"]; got != `{"page":2,"limit":10}` {
		t.Fatalf("unexpected logged body: %v", got)
	}
	if got := entry["req_size_bytes"]; got != float64(len(sent)) {
		t.Errorf("expected req_size_bytes %d, got %v", len(sent), got)
	}
}

func TestRequestLogger_TruncatesOversizedBodies(t *testing.T) {
	buf := captureLogs(t)

	var seen int

	engine := gin.New()
	engine.Use(RequestLogger())
	engine.POST("/items", func(c *gin.Context) {
		body, _ := io.ReadAll(c.Request.Body)
		seen = len(body)
		c.Status(http.StatusOK)
	})

	sent := strings.Repeat("a", 5000)
	req := httptest.NewRequest(http.MethodPost, "/items", strings.NewReader(sent))
	req.Header.Set("Content-Type", "text/plain")

	serve(engine, req)

	// Truncation only affects the log line: the handler still gets every byte.
	if seen != len(sent) {
		t.Fatalf("the handler read %d bytes, expected %d", seen, len(sent))
	}

	entry := logLine(t, buf, "Request handled")

	logged, _ := entry["req_body"].(string)
	if len(logged) != 4<<10 {
		t.Errorf("expected the logged body to be capped at 4 KiB, got %d bytes", len(logged))
	}
	if entry["req_body_truncated"] != true {
		t.Errorf("expected the line to say the body was truncated, got %v", entry["req_body_truncated"])
	}
}

func TestRequestLogger_LogsTextualBodies(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		contentType string
	}{
		{"a vendor json type", http.MethodPut, "application/merge-patch+json"},
		{"a form", http.MethodPatch, "application/x-www-form-urlencoded"},
		{"plain text with a charset", http.MethodDelete, "text/plain; charset=utf-8"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := captureLogs(t)

			engine := gin.New()
			engine.Use(RequestLogger())
			engine.Handle(tt.method, "/thing", func(c *gin.Context) {
				c.Status(http.StatusOK)
			})

			req := httptest.NewRequest(tt.method, "/thing", strings.NewReader("payload"))
			req.Header.Set("Content-Type", tt.contentType)

			serve(engine, req)

			if got := logLine(t, buf, "Request handled")["req_body"]; got != "payload" {
				t.Fatalf("expected the body to be logged, got %v", got)
			}
		})
	}
}

func TestRequestLogger_LeavesBodiesItCannotReadAlone(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		contentType string
	}{
		{"a GET carries no body worth logging", http.MethodGet, "application/json"},
		{"an upload is not text", http.MethodPost, "multipart/form-data; boundary=x"},
		{"a binary payload is not text", http.MethodPost, "application/octet-stream"},
		{"an unlabelled body is not worth guessing at", http.MethodPost, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := captureLogs(t)

			engine := gin.New()
			engine.Use(RequestLogger())
			engine.Handle(tt.method, "/thing", func(c *gin.Context) {
				c.Status(http.StatusOK)
			})

			req := httptest.NewRequest(tt.method, "/thing", strings.NewReader("payload"))
			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}

			serve(engine, req)

			if got, found := logLine(t, buf, "Request handled")["req_body"]; found {
				t.Fatalf("expected no body to be logged, got %v", got)
			}
		})
	}
}

func TestRequestLogger_LogsQueryParamsAndUserAgent(t *testing.T) {
	buf := captureLogs(t)

	engine := gin.New()
	engine.Use(RequestLogger())
	engine.GET("/items/:id", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/items/abc?limit=5&page=2", nil)
	req.Header.Set("User-Agent", "tester/1.0")

	serve(engine, req)

	entry := logLine(t, buf, "Request handled")

	if got := entry["query"]; got != "limit=5&page=2" {
		t.Errorf("expected the query string to be logged, got %v", got)
	}

	params, _ := entry["path_params"].(map[string]any)
	if params["id"] != "abc" {
		t.Errorf("expected the path parameters to be logged, got %v", entry["path_params"])
	}

	if got := entry["user_agent"]; got != "tester/1.0" {
		t.Errorf("expected the user agent to be logged, got %v", got)
	}
}

func TestRequestLogger_LogsGinErrors(t *testing.T) {
	buf := captureLogs(t)

	engine := gin.New()
	engine.Use(RequestLogger())
	engine.GET("/thing", func(c *gin.Context) {
		_ = c.Error(io.ErrUnexpectedEOF)
		c.Status(http.StatusBadRequest)
	})

	serve(engine, httptest.NewRequest(http.MethodGet, "/thing", nil))

	if got, _ := logLine(t, buf, "Request handled")["gin_errors"].(string); !strings.Contains(got, "unexpected EOF") {
		t.Fatalf("expected gin errors to be logged, got %q", got)
	}
}

// TestRequestLogger_CapturesTheResponseBodyOnlyOnFailure pins both halves:
// a failure logs what the caller was told, a success pays nothing.
func TestRequestLogger_CapturesTheResponseBodyOnlyOnFailure(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		wantBody any
	}{
		{"a failure carries its explanation", http.StatusBadGateway, "failed to fetch data"},
		{"a success carries nothing", http.StatusOK, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := captureLogs(t)

			engine := gin.New()
			engine.Use(RequestLogger())
			engine.GET("/thing", func(c *gin.Context) {
				c.String(tt.status, "failed to fetch data")
			})

			serve(engine, httptest.NewRequest(http.MethodGet, "/thing", nil))

			if got := logLine(t, buf, "Request handled")["resp_body"]; got != tt.wantBody {
				t.Fatalf("expected resp_body to be %v, got %v", tt.wantBody, got)
			}
		})
	}
}

func TestRequestLogger_CapsTheCapturedResponseBody(t *testing.T) {
	buf := captureLogs(t)

	engine := gin.New()
	engine.Use(RequestLogger())
	engine.GET("/thing", func(c *gin.Context) {
		c.String(http.StatusInternalServerError, strings.Repeat("x", 3000))
		c.String(http.StatusInternalServerError, strings.Repeat("y", 3000))
	})

	w := serve(engine, httptest.NewRequest(http.MethodGet, "/thing", nil))

	if w.Body.Len() != 6000 {
		t.Fatalf("expected the caller to get the whole body, got %d bytes", w.Body.Len())
	}

	if got, _ := logLine(t, buf, "Request handled")["resp_body"].(string); len(got) != 4<<10 {
		t.Fatalf("expected the captured body to be capped at 4 KiB, got %d bytes", len(got))
	}
}

// TestRequestLogger_RequestID pins the trust model: a line is always logged
// under our own id, whatever the caller sent.
func TestRequestLogger_RequestID(t *testing.T) {
	tests := []struct {
		name              string
		sent              string
		wantCorrelationID any
	}{
		{"an id sent by the caller is kept as the correlation id", "abc-123", "abc-123"},
		{"a caller sending nothing correlates to nothing", "", nil},
		{"an unusable id is dropped", "not a\nvalid id", nil},
		{"an id at the length cap is kept", strings.Repeat("x", 128), strings.Repeat("x", 128)},
		{"an oversized id is dropped", strings.Repeat("x", 129), nil},
		{"a non ASCII id is dropped", "idè", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := captureLogs(t)

			var seen string

			engine := gin.New()
			engine.Use(RequestLogger())
			engine.GET("/thing", func(c *gin.Context) {
				seen = logging.RequestIDFromContext(c.Request.Context())
				c.Status(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodGet, "/thing", nil)
			if tt.sent != "" {
				// Set stores the value verbatim: a header is no proof of sanity.
				req.Header.Set(logging.RequestIDHeader, tt.sent)
			}

			w := serve(engine, req)

			echoed := w.Header().Get(logging.RequestIDHeader)
			if echoed == "" {
				t.Fatal("expected a request id to be echoed back to the caller")
			}
			if echoed == tt.sent {
				t.Fatalf("expected an id of our own, got the caller's %q back", tt.sent)
			}

			if seen != echoed {
				t.Errorf("the handler saw %q, expected %q", seen, echoed)
			}

			entry := logLine(t, buf, "Request handled")

			if got := entry[logging.RequestIDField]; got != echoed {
				t.Errorf("expected the log line to carry %q, got %v", echoed, got)
			}
			if got := entry[logging.CorrelationIDField]; got != tt.wantCorrelationID {
				t.Errorf("expected correlation id %v, got %v", tt.wantCorrelationID, got)
			}
		})
	}
}

// TestRequestLogger_LogsPanicsAsErrors guards the registration order: with
// the logger after the recovery, a panic would get an unlogged 500.
func TestRequestLogger_LogsPanicsAsErrors(t *testing.T) {
	buf := captureLogs(t)

	engine := gin.New()
	engine.Use(RequestLogger(), Recovery())
	engine.GET("/boom", func(c *gin.Context) {
		panic("kaboom")
	})

	w := serve(engine, httptest.NewRequest(http.MethodGet, "/boom", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, w.Code)
	}

	panicLine := logLine(t, buf, "Panic recovered while serving the request")
	if got := panicLine["level"]; got != "ERROR" {
		t.Errorf("expected the panic to be logged as an error, got %v", got)
	}
	if got := panicLine["panic"]; got != "kaboom" {
		t.Errorf("expected the panic value to be logged, got %v", got)
	}
	if got, _ := panicLine["stack"].(string); !strings.Contains(got, "httpserver") {
		t.Errorf("expected the panic line to carry a stack trace, got %q", got)
	}
	if got := panicLine[logging.RequestIDField]; got == nil || got == "" {
		t.Errorf("expected the panic line to carry the request id, got %v", got)
	}

	accessLine := logLine(t, buf, "Request handled")
	if got := accessLine["level"]; got != "ERROR" {
		t.Errorf("expected the access log line to be an error, got %v", got)
	}
	if got := accessLine["status"]; got != float64(http.StatusInternalServerError) {
		t.Errorf("expected the access log line to carry status 500, got %v", got)
	}
}

type testKeyCtxKey struct{}

func contextWithTestKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, testKeyCtxKey{}, key)
}

// newForwarderEngine mounts the forwarder in front of a handler reporting
// whichever value reached the request context.
func newForwarderEngine(got *string, found *bool) *gin.Engine {
	engine := gin.New()
	engine.Use(HeaderForwarder("x-test-key", contextWithTestKey))
	engine.GET("/api/items", func(c *gin.Context) {
		*got, *found = c.Request.Context().Value(testKeyCtxKey{}).(string)
		c.Status(http.StatusOK)
	})
	return engine
}

func TestHeaderForwarder_CarriesTheHeaderIntoTheContext(t *testing.T) {
	var got string
	var found bool
	engine := newForwarderEngine(&got, &found)

	req := httptest.NewRequest(http.MethodGet, "/api/items", nil)
	req.Header.Set("X-Test-Key", "s3cr3t")

	w := serve(engine, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}
	if !found || got != "s3cr3t" {
		t.Fatalf("expected the value to reach the handler, got %q (found=%v)", got, found)
	}
}

// TestHeaderForwarder_ServesRequestsWithoutTheHeader pins the non-blocking
// behaviour: a request without the header is served anyway.
func TestHeaderForwarder_ServesRequestsWithoutTheHeader(t *testing.T) {
	tests := []struct {
		name    string
		present bool
	}{
		{"header absent", false},
		{"header present but empty", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			var found bool
			engine := newForwarderEngine(&got, &found)

			req := httptest.NewRequest(http.MethodGet, "/api/items", nil)
			if tt.present {
				req.Header.Set("x-test-key", "")
			}

			w := serve(engine, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected the request to be served, got status %d", w.Code)
			}
			if found {
				t.Fatalf("expected nothing in the context, got %q", got)
			}
		})
	}
}
