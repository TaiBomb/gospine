package httpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TaiBomb/gospine/logging"
)

func transportOf(t *testing.T, c *HTTPClient) *http.Transport {
	t.Helper()

	forwarder, ok := c.client.Transport.(requestIDForwarder)
	if !ok {
		t.Fatalf("expected the transport to forward the request id, got %T", c.client.Transport)
	}

	transport, ok := forwarder.base.(*http.Transport)
	if !ok {
		t.Fatalf("expected an *http.Transport underneath, got %T", forwarder.base)
	}

	return transport
}

func TestNew(t *testing.T) {
	t.Run("Default options", func(t *testing.T) {
		client := New(Options{})

		if client == nil {
			t.Fatal("Expected HTTPClient instance, got nil")
		}

		if client.client.Timeout != 10*time.Second {
			t.Errorf("Expected default timeout of 10s, got %v", client.client.Timeout)
		}

		transport := transportOf(t, client)
		if transport.MaxIdleConns != 50 || transport.MaxIdleConnsPerHost != 10 || transport.IdleConnTimeout != 90*time.Second {
			t.Errorf("unexpected transport defaults: %d, %d, %v",
				transport.MaxIdleConns, transport.MaxIdleConnsPerHost, transport.IdleConnTimeout)
		}
	})

	t.Run("Custom options", func(t *testing.T) {
		client := New(Options{
			Timeout:             5 * time.Second,
			MaxIdleConns:        7,
			MaxIdleConnsPerHost: 3,
			IdleConnTimeout:     time.Second,
		})

		if client.client.Timeout != 5*time.Second {
			t.Errorf("Expected custom timeout of 5s, got %v", client.client.Timeout)
		}

		transport := transportOf(t, client)
		if transport.MaxIdleConns != 7 || transport.MaxIdleConnsPerHost != 3 || transport.IdleConnTimeout != time.Second {
			t.Errorf("unexpected transport settings: %d, %d, %v",
				transport.MaxIdleConns, transport.MaxIdleConnsPerHost, transport.IdleConnTimeout)
		}
	})
}

func TestHTTPClient_ForwardsTheRequestID(t *testing.T) {
	tests := []struct {
		name    string
		ctx     func() context.Context
		headers map[string]string
		want    string
	}{
		{
			name: "the id in the context is forwarded",
			ctx: func() context.Context {
				return logging.ContextWithRequestID(context.Background(), "id-from-the-request")
			},
			want: "id-from-the-request",
		},
		{
			name: "a call outside any request forwards nothing",
			ctx:  context.Background,
			want: "",
		},
		{
			name: "an explicit header wins over the context",
			ctx: func() context.Context {
				return logging.ContextWithRequestID(context.Background(), "id-from-the-request")
			},
			headers: map[string]string{logging.RequestIDHeader: "id-set-by-the-caller"},
			want:    "id-set-by-the-caller",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string

			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Get(logging.RequestIDHeader)
				w.WriteHeader(http.StatusOK)
			}))
			defer ts.Close()

			req, err := http.NewRequestWithContext(tt.ctx(), http.MethodGet, ts.URL, nil)
			if err != nil {
				t.Fatalf("failed to build the request: %v", err)
			}

			for name, value := range tt.headers {
				req.Header.Set(name, value)
			}

			resp, err := New(Options{}).HTTP().Do(req)
			if err != nil {
				t.Fatalf("the call returned an unexpected error: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if got != tt.want {
				t.Fatalf("the callee saw %q, expected %q", got, tt.want)
			}

			if tt.headers == nil && req.Header.Get(logging.RequestIDHeader) != "" {
				t.Fatal("expected the caller's request to be left untouched")
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestNew_CustomTransport(t *testing.T) {
	var (
		calls  int
		gotID  string
		custom = http.DefaultTransport
	)

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		gotID = req.Header.Get(logging.RequestIDHeader)
		return custom.RoundTrip(req)
	})

	client := New(Options{Transport: transport, Timeout: 3 * time.Second})

	forwarder, ok := client.client.Transport.(requestIDForwarder)
	if !ok {
		t.Fatalf("expected the custom transport to be wrapped by the forwarder, got %T", client.client.Transport)
	}
	if _, ok := forwarder.base.(roundTripFunc); !ok {
		t.Fatalf("expected the custom transport underneath, got %T", forwarder.base)
	}
	if client.client.Timeout != 3*time.Second {
		t.Errorf("expected the timeout to apply with a custom transport, got %v", client.client.Timeout)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ts.Close()

	ctx := logging.ContextWithRequestID(context.Background(), "id-1")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to build the request: %v", err)
	}

	resp, err := client.HTTP().Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = resp.Body.Close()

	if calls != 1 {
		t.Errorf("expected the custom transport to carry the call, got %d calls", calls)
	}
	if gotID != "id-1" {
		t.Errorf("expected the request id to be forwarded through the custom transport, got %q", gotID)
	}
}
