package svcclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Shin-9x/envconfig"
	"github.com/TaiBomb/gospine/httpclient"
	"github.com/TaiBomb/gospine/logging"
	"github.com/danielgtaylor/huma/v2"
)

// newTestClient serves handler and returns a Client pointed at it, under the
// given base path.
func newTestClient(t *testing.T, basePath string, handler http.HandlerFunc) *Client {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	c, err := New(Config{BaseURL: server.URL + basePath}, server.Client())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	return c
}

func TestConfig_LoadsUnderAnEnvPrefix(t *testing.T) {
	t.Setenv("risorseService.baseUrl", "http://risorse:8081")
	t.Setenv("risorseService.timeout", "60s")

	cfg, err := envconfig.Load[struct {
		Risorse Config `envPrefix:"risorseService."`
	}]()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Risorse != (Config{BaseURL: "http://risorse:8081", Timeout: time.Minute}) {
		t.Fatalf("unexpected config: %+v", cfg.Risorse)
	}
}

func TestNew_RejectsAnInvalidBaseURL(t *testing.T) {
	for _, baseURL := range []string{"", "risorse:8081", "ftp://risorse", "http://"} {
		if _, err := New(Config{BaseURL: baseURL}, http.DefaultClient); err == nil {
			t.Errorf("expected %q to be rejected", baseURL)
		}
	}
}

func TestNew_SharesTheTransportUnderItsOwnTimeout(t *testing.T) {
	shared := httpclient.New(httpclient.Options{Timeout: 10 * time.Second}).HTTP()

	scoped, _ := New(Config{BaseURL: "http://risorse", Timeout: time.Minute}, shared)
	inherited, _ := New(Config{BaseURL: "http://risorse"}, shared)

	if scoped.http.Transport != shared.Transport {
		t.Error("expected the transport to be shared")
	}
	if scoped.http.Timeout != time.Minute || inherited.http.Timeout != 10*time.Second {
		t.Errorf("unexpected timeouts: %v and %v", scoped.http.Timeout, inherited.http.Timeout)
	}
	if shared.Timeout != 10*time.Second {
		t.Errorf("expected the shared client to be left alone, got %v", shared.Timeout)
	}
}

func TestPostJSON(t *testing.T) {
	var gotPath, gotType, gotAccept, gotBody string

	c := newTestClient(t, "/ctx/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotPath, gotBody = r.URL.Path, string(body)
		gotType, gotAccept = r.Header.Get("Content-Type"), r.Header.Get("Accept")

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":42}`))
	})

	var out struct{ ID int }
	if err := c.PostJSON(context.Background(), "/api/things", map[string]string{"name": "x"}, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotPath != "/ctx/api/things" {
		t.Errorf("expected the path under the base URL, got %q", gotPath)
	}
	if gotType != "application/json" || gotAccept != "application/json" || gotBody != `{"name":"x"}` {
		t.Errorf("unexpected request: %q %q %s", gotType, gotAccept, gotBody)
	}
	if out.ID != 42 {
		t.Errorf("unexpected answer: %+v", out)
	}
}

func TestGetJSON_SendsTheQuery(t *testing.T) {
	var gotQuery url.Values

	c := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		_, _ = w.Write([]byte(`[]`))
	})

	if err := c.GetJSON(context.Background(), "api/things", url.Values{"q": {"a b"}}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotQuery.Get("q") != "a b" {
		t.Errorf("unexpected query: %v", gotQuery)
	}
}

func TestDo_ReturnsTheRawBody(t *testing.T) {
	c := newTestClient(t, "", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF"))
	})

	resp, err := c.Do(context.Background(), Request{Method: http.MethodPost, Path: "/pdf", Body: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(resp.Body) != "%PDF" || resp.Header.Get("Content-Type") != "application/pdf" {
		t.Errorf("unexpected response: %q %v", resp.Body, resp.Header)
	}
}

func TestDo_ReadsTheProblemDocument(t *testing.T) {
	c := newTestClient(t, "", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusGatewayTimeout)
		_, _ = w.Write([]byte(`{"title":"Gateway Timeout","status":504,"detail":"PDF generation timed out"}`))
	})

	_, err := c.Do(context.Background(), Request{Method: http.MethodPost, Path: "/pdf"})

	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected a StatusError, got %v", err)
	}
	if statusErr.StatusCode != 504 || statusErr.Problem.Detail != "PDF generation timed out" {
		t.Errorf("unexpected error: %+v", statusErr)
	}
	if got := err.Error(); got != "POST /pdf: service answered 504: PDF generation timed out" {
		t.Errorf("unexpected message: %q", got)
	}
}

func TestDo_ForwardsTheRequestIDThroughTheSharedClient(t *testing.T) {
	var got string

	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = r.Header.Get(logging.RequestIDHeader)
	}))
	defer server.Close()

	c, _ := New(Config{BaseURL: server.URL}, httpclient.New(httpclient.Options{}).HTTP())
	ctx := logging.ContextWithRequestID(context.Background(), "req-1")

	if err := c.Ping(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "req-1" {
		t.Errorf("expected the request id to be forwarded, got %q", got)
	}
}

func TestIsTimeout(t *testing.T) {
	slow := newTestClient(t, "", func(http.ResponseWriter, *http.Request) { time.Sleep(200 * time.Millisecond) })
	slow.http.Timeout = 20 * time.Millisecond
	_, clientTimeout := slow.Do(context.Background(), Request{Method: http.MethodGet, Path: "/"})

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"the client timeout", clientTimeout, true},
		{"a deadline", fmt.Errorf("call: %w", context.DeadlineExceeded), true},
		{"a 504 of the service", &StatusError{StatusCode: 504}, true},
		{"a 500 of the service", &StatusError{StatusCode: 500}, false},
		{"a refused connection", errors.New("connection refused"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTimeout(tt.err); got != tt.want {
				t.Errorf("expected %v, got %v (%v)", tt.want, got, tt.err)
			}
		})
	}
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer

	previous := logging.Log
	logging.Log = &logging.CustomLogger{
		Logger: slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	t.Cleanup(func() { logging.Log = previous })

	return &buf
}

func TestUpstream(t *testing.T) {
	full := UpstreamSpec{LogMessage: "render", ClientMessage: "failed", NotFoundMessage: "missing", TimeoutMessage: "too slow"}
	bare := UpstreamSpec{LogMessage: "render", ClientMessage: "failed"}

	tests := []struct {
		name        string
		err         error
		spec        UpstreamSpec
		wantStatus  int
		wantMessage string
		wantLevel   string
	}{
		{"a 404 becomes a 404 when configured", &StatusError{StatusCode: 404}, full, 404, "missing", "DEBUG"},
		{"a timeout becomes a 504 when configured", &StatusError{StatusCode: 504}, full, 504, "too slow", "ERROR"},
		{"a 404 is a 502 when not configured", &StatusError{StatusCode: 404}, bare, 502, "failed", "ERROR"},
		{"a timeout is a 502 when not configured", context.DeadlineExceeded, bare, 502, "failed", "ERROR"},
		{"anything else is a 502", &StatusError{StatusCode: 500, Body: "secret stack trace"}, full, 502, "failed", "ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := captureLogs(t)

			err := Upstream(context.Background(), tt.err, tt.spec, "id", "42")

			var se huma.StatusError
			if !errors.As(err, &se) || se.GetStatus() != tt.wantStatus || se.Error() != tt.wantMessage {
				t.Fatalf("expected %d %q, got %v", tt.wantStatus, tt.wantMessage, err)
			}

			var entry map[string]any
			_ = json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &entry)
			if entry["level"] != tt.wantLevel || entry["id"] != "42" {
				t.Errorf("unexpected log line: %v", entry)
			}
		})
	}
}

func TestErrorLogFields(t *testing.T) {
	err := fmt.Errorf("POST /pdf: %w", &StatusError{StatusCode: 500, Body: "boom"})

	fields := ErrorLogFields(err, "k", "v")

	if len(fields) != 8 || fields[4] != "statusCode" || fields[5] != 500 || fields[7] != "boom" {
		t.Errorf("unexpected fields: %v", fields)
	}
}
