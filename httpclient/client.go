// Package httpclient builds the outbound HTTP client a service shares across
// all its upstream calls.
package httpclient

import (
	"net/http"

	"github.com/TaiBomb/gospine/logging"
)

// HTTPClient is a reusable wrapper around net/http.Client.
type HTTPClient struct {
	client *http.Client
}

// New creates an HTTPClient that forwards the request id of the request
// being served on every outbound call, custom Transport included.
func New(opts Options) *HTTPClient {
	opts = opts.withDefaults()

	transport := opts.Transport
	if transport == nil {
		transport = &http.Transport{
			MaxIdleConns:        opts.MaxIdleConns,
			MaxIdleConnsPerHost: opts.MaxIdleConnsPerHost,
			IdleConnTimeout:     opts.IdleConnTimeout,
		}
	}

	return &HTTPClient{
		client: &http.Client{
			Timeout:   opts.Timeout,
			Transport: requestIDForwarder{base: transport},
		},
	}
}

// HTTP returns the underlying client, so callers share its connection pool and timeout.
func (h *HTTPClient) HTTP() *http.Client {
	return h.client
}

// requestIDForwarder lets the service on the other end log under the same id,
// so a failure can be followed across the hop.
type requestIDForwarder struct {
	base http.RoundTripper
}

func (f requestIDForwarder) RoundTrip(req *http.Request) (*http.Response, error) {
	id := logging.RequestIDFromContext(req.Context())
	if id == "" || req.Header.Get(logging.RequestIDHeader) != "" {
		return f.base.RoundTrip(req)
	}

	// A RoundTripper must not modify the request it was given.
	forwarded := req.Clone(req.Context())
	forwarded.Header.Set(logging.RequestIDHeader, id)

	return f.base.RoundTrip(forwarded)
}
