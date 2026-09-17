package pcms

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/TaiBomb/gopcms"
	"github.com/TaiBomb/gospine/logging"
)

func TestMain(m *testing.M) {
	logging.InitLogger()
	os.Exit(m.Run())
}

func newTestClient(t *testing.T, handler http.HandlerFunc) Client {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	c, err := New(Config{BaseURL: server.URL, APIURL: "/api"}, server.Client())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	return c
}

func TestNew_Success(t *testing.T) {
	c, err := New(Config{BaseURL: "http://localhost:3000", APIURL: "/api"}, http.DefaultClient)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected a non-nil client")
	}
	if c.Raw() == nil {
		t.Fatal("expected Raw() to return a non-nil gopcms client")
	}
}

func TestNew_InvalidBaseURL(t *testing.T) {
	c, err := New(Config{BaseURL: "", APIURL: "/api"}, http.DefaultClient)
	if err == nil {
		t.Fatal("expected an error for an empty base URL")
	}
	if c != nil {
		t.Fatal("expected a nil client when New fails")
	}
}

func TestPing_Success(t *testing.T) {
	var gotMethod, gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusOK)
	})

	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("expected Ping to succeed, got %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Fatalf("expected a GET request, got %s", gotMethod)
	}
	if gotPath != "/api/access" {
		t.Fatalf("expected request to /api/access, got %q", gotPath)
	}
}

func TestPing_Failure(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("expected Ping to fail when PayloadCMS is unavailable")
	}
}

func TestInternalAPIKeyContextRoundTrip(t *testing.T) {
	ctx := ContextWithInternalAPIKey(context.Background(), "s3cr3t")

	got, ok := InternalAPIKeyFromContext(ctx)
	if !ok || got != "s3cr3t" {
		t.Fatalf("expected the key to survive the round trip, got %q (ok=%v)", got, ok)
	}
}

func TestInternalAPIKeyFromContext_AbsentOrEmpty(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
	}{
		{"no key at all", context.Background()},
		{"empty key", ContextWithInternalAPIKey(context.Background(), "")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, ok := InternalAPIKeyFromContext(tt.ctx); ok || got != "" {
				t.Fatalf("expected no key, got %q (ok=%v)", got, ok)
			}
		})
	}
}

func TestInternalAPIKeyAuth_AppliesTheHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/articles", nil)
	ctx := ContextWithInternalAPIKey(context.Background(), "s3cr3t")

	if err := (internalAPIKeyAuth{}).ApplyAuth(ctx, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := req.Header.Get(InternalAPIKeyHeader); got != "s3cr3t" {
		t.Fatalf("expected the key to be applied, got %q", got)
	}
}

// TestInternalAPIKeyAuth_LeavesTheRequestUnauthenticated pins the choice of not
// failing a keyless call: PayloadCMS decides what is readable anonymously.
func TestInternalAPIKeyAuth_LeavesTheRequestUnauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/articles", nil)

	if err := (internalAPIKeyAuth{}).ApplyAuth(context.Background(), req); err != nil {
		t.Fatalf("expected a keyless context not to fail the request, got %v", err)
	}

	if req.Header.Get(InternalAPIKeyHeader) != "" {
		t.Fatal("expected no internal API key header to be set")
	}
}

// TestNew_ForwardsTheInternalAPIKey exercises the whole wiring: a key in the
// context reaches PayloadCMS as a header on the actual call.
func TestNew_ForwardsTheInternalAPIKey(t *testing.T) {
	var gotKey string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get(InternalAPIKeyHeader)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"docs":[]}`))
	})

	ctx := ContextWithInternalAPIKey(context.Background(), "s3cr3t")
	collection := gopcms.NewCollection[map[string]any](c.Raw(), "articles")

	if _, err := collection.Find(ctx, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotKey != "s3cr3t" {
		t.Fatalf("expected PayloadCMS to receive the internal API key, got %q", gotKey)
	}
}

// TestPing_IsIssuedUnauthenticated pins why /status works without a key:
// gopcms issues the ping without the Authenticator.
func TestPing_IsIssuedUnauthenticated(t *testing.T) {
	var gotKey string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get(InternalAPIKeyHeader)
		w.WriteHeader(http.StatusOK)
	})

	ctx := ContextWithInternalAPIKey(context.Background(), "s3cr3t")
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("expected Ping to succeed, got %v", err)
	}

	if gotKey != "" {
		t.Fatalf("expected the ping to carry no internal API key, got %q", gotKey)
	}
}
