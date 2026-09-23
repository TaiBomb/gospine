// Package svcclient calls the other services of the platform over HTTP: JSON
// bodies in and out, RFC 7807 problem documents read back as errors.
//
// A Client issues its calls through the shared httpclient, so they keep its
// connection pool, X-Request-ID forwarding and client metrics.
package svcclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxErrorBody caps what is read of a failed answer: problem documents are small.
const maxErrorBody = 4 << 10

// Config locates a service. Embed it under an envPrefix naming the service:
//
//	Risorse svcclient.Config `envPrefix:"risorseService."`
type Config struct {
	// BaseURL includes the context path of the service, if any.
	BaseURL string `env:"baseUrl" required:"true"`
	// Timeout bounds every call; zero keeps the timeout of the shared client.
	Timeout time.Duration `env:"timeout"`
}

// Client calls one service.
type Client struct {
	http    *http.Client
	baseURL string
}

// New returns a Client for the service cfg locates, issuing its calls through
// httpClient with the timeout of cfg.
func New(cfg Config, httpClient *http.Client) (*Client, error) {
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("invalid service base URL %q", cfg.BaseURL)
	}

	// A copy shares the transport, hence the pool, under its own timeout.
	scoped := *httpClient
	if cfg.Timeout > 0 {
		scoped.Timeout = cfg.Timeout
	}

	return &Client{
		http:    &scoped,
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
	}, nil
}

// Request is a call to the service.
type Request struct {
	Method string
	Path   string
	Query  url.Values
	Body   any
	Header http.Header
}

// Response is a 2xx answer, its body read in full.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// Do issues req. An answer outside 2xx fails with a *StatusError.
func (c *Client) Do(ctx context.Context, req Request) (*Response, error) {
	var body io.Reader
	if req.Body != nil {
		encoded, err := json.Marshal(req.Body)
		if err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
		body = bytes.NewReader(encoded)
	}

	target := c.baseURL + "/" + strings.TrimLeft(req.Path, "/")
	if len(req.Query) > 0 {
		target += "?" + req.Query.Encode()
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, target, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	for name, values := range req.Header {
		for _, value := range values {
			httpReq.Header.Add(name, value)
		}
	}
	if req.Body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", req.Method, req.Path, err)
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		head, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return nil, fmt.Errorf("%s %s: %w", req.Method, req.Path, newStatusError(resp.StatusCode, head))
	}

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s %s: read response: %w", req.Method, req.Path, err)
	}

	return &Response{StatusCode: resp.StatusCode, Header: resp.Header, Body: content}, nil
}

// GetJSON issues a GET and decodes the answer into out, when not nil.
func (c *Client) GetJSON(ctx context.Context, path string, query url.Values, out any) error {
	return c.doJSON(ctx, Request{Method: http.MethodGet, Path: path, Query: query}, out)
}

// PostJSON posts body as JSON and decodes the answer into out, when not nil.
func (c *Client) PostJSON(ctx context.Context, path string, body, out any) error {
	return c.doJSON(ctx, Request{Method: http.MethodPost, Path: path, Body: body}, out)
}

func (c *Client) doJSON(ctx context.Context, req Request, out any) error {
	req.Header = http.Header{"Accept": {"application/json"}}

	resp, err := c.Do(ctx, req)
	if err != nil || out == nil {
		return err
	}

	if err := json.Unmarshal(resp.Body, out); err != nil {
		return fmt.Errorf("%s %s: decode response: %w", req.Method, req.Path, err)
	}

	return nil
}

// Ping calls GET /health, which every gospine service answers, so a Client
// can serve as the /status probe of its caller.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.Do(ctx, Request{Method: http.MethodGet, Path: "/health"})
	return err
}
