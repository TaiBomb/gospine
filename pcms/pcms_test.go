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

func TestContextAuth_AppliesTheInternalAPIKey(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/articles", nil)
	ctx := ContextWithInternalAPIKey(context.Background(), "s3cr3t")

	if err := (contextAuth{}).ApplyAuth(ctx, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := req.Header.Get(InternalAPIKeyHeader); got != "s3cr3t" {
		t.Fatalf("expected the key to be applied, got %q", got)
	}
}

// TestContextAuth_LeavesTheRequestUnauthenticated pins the choice of not
// failing a keyless call: PayloadCMS decides what is readable anonymously.
func TestContextAuth_LeavesTheRequestUnauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/articles", nil)

	if err := (contextAuth{}).ApplyAuth(context.Background(), req); err != nil {
		t.Fatalf("expected a keyless context not to fail the request, got %v", err)
	}

	if req.Header.Get(InternalAPIKeyHeader) != "" || req.Header.Get(AuthorizationHeader) != "" {
		t.Fatal("expected no credential header to be set")
	}
}

func TestAuthorizationContextRoundTrip(t *testing.T) {
	ctx := ContextWithAuthorization(context.Background(), "Bearer t0k3n")

	got, ok := AuthorizationFromContext(ctx)
	if !ok || got != "Bearer t0k3n" {
		t.Fatalf("expected the value to survive the round trip, got %q (ok=%v)", got, ok)
	}
}

func TestAuthorizationFromContext_AbsentOrEmpty(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
	}{
		{"no value at all", context.Background()},
		{"empty value", ContextWithAuthorization(context.Background(), "")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, ok := AuthorizationFromContext(tt.ctx); ok || got != "" {
				t.Fatalf("expected no value, got %q (ok=%v)", got, ok)
			}
		})
	}
}

// TestContextAuth_AppliesBothCredentials checks that the user's
// Authorization travels alongside the internal API key, not in its place.
func TestContextAuth_AppliesBothCredentials(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/users/me", nil)
	ctx := ContextWithInternalAPIKey(context.Background(), "s3cr3t")
	ctx = ContextWithAuthorization(ctx, "Bearer t0k3n")

	if err := (contextAuth{}).ApplyAuth(ctx, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := req.Header.Get(InternalAPIKeyHeader); got != "s3cr3t" {
		t.Fatalf("expected the internal API key to be applied, got %q", got)
	}
	if got := req.Header.Get(AuthorizationHeader); got != "Bearer t0k3n" {
		t.Fatalf("expected the Authorization to be applied, got %q", got)
	}
}

// TestContextAuth_KeyOnlyAddsNoAuthorization pins the backward compatibility
// every existing service relies on: with only the key in the context, the
// call is exactly what it was before Authorization forwarding existed.
func TestContextAuth_KeyOnlyAddsNoAuthorization(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/articles", nil)
	ctx := ContextWithInternalAPIKey(context.Background(), "s3cr3t")

	if err := (contextAuth{}).ApplyAuth(ctx, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, found := req.Header[AuthorizationHeader]; found {
		t.Fatalf("expected no Authorization header, got %q", req.Header.Get(AuthorizationHeader))
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

// TestNew_ForwardsTheAuthorization exercises the whole wiring for a user
// call: the Authorization in the context reaches PayloadCMS with the key.
func TestNew_ForwardsTheAuthorization(t *testing.T) {
	var gotKey, gotAuth string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotKey, gotAuth = r.Header.Get(InternalAPIKeyHeader), r.Header.Get(AuthorizationHeader)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user":null}`))
	})

	ctx := ContextWithInternalAPIKey(context.Background(), "s3cr3t")
	ctx = ContextWithAuthorization(ctx, "Bearer t0k3n")

	var out map[string]any
	if err := c.Raw().Do(ctx, http.MethodGet, "/users/me", nil, nil, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotKey != "s3cr3t" || gotAuth != "Bearer t0k3n" {
		t.Fatalf("expected PayloadCMS to receive both credentials, got key=%q auth=%q", gotKey, gotAuth)
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
