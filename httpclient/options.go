package httpclient

import (
	"net/http"
	"time"
)

// Options configures New. A zero field takes its default.
type Options struct {
	// Timeout bounds every call. Default 10s.
	Timeout time.Duration

	// Transport replaces the default transport, e.g. for TLS or proxy settings.
	// The Max* and IdleConnTimeout fields below are then ignored.
	Transport http.RoundTripper

	// MaxIdleConns caps idle connections across all hosts. Default 50.
	MaxIdleConns int
	// MaxIdleConnsPerHost caps idle connections per host. Default 10.
	MaxIdleConnsPerHost int
	// IdleConnTimeout is how long an idle connection is kept. Default 90s.
	IdleConnTimeout time.Duration
}

func (o Options) withDefaults() Options {
	if o.Timeout == 0 {
		o.Timeout = 10 * time.Second
	}
	if o.MaxIdleConns == 0 {
		o.MaxIdleConns = 50
	}
	if o.MaxIdleConnsPerHost == 0 {
		o.MaxIdleConnsPerHost = 10
	}
	if o.IdleConnTimeout == 0 {
		o.IdleConnTimeout = 90 * time.Second
	}

	return o
}
