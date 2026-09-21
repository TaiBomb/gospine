package httpserver

import (
	"context"

	"github.com/TaiBomb/gospine/apidoc"
	"github.com/TaiBomb/gospine/config"
	"github.com/danielgtaylor/huma/v2"
	"github.com/gin-gonic/gin"
)

// Module is a group of operations mounted under a common prefix and tag.
type Module struct {
	Prefix string
	// Tag is added to every operation of the module; empty adds none.
	Tag      string
	Register func(huma.API)
}

// Probe reports whether a dependency is reachable.
type Probe interface {
	Ping(ctx context.Context) error
}

// ProbeFunc adapts a function to Probe, for clients whose ping has another signature.
type ProbeFunc func(ctx context.Context) error

// Ping calls f.
func (f ProbeFunc) Ping(ctx context.Context) error {
	return f(ctx)
}

// Status configures the built-in GET {contextPath}/status handler.
type Status struct {
	// Probe is pinged within PingTimeout; nil makes /status answer like /health.
	Probe Probe
	// Dependency names what Probe checks, in the error response and log line.
	Dependency string
	// ErrorFields builds the log fields of a failed ping; nil logs the error alone.
	ErrorFields func(err error, extra ...any) []any
}

// Options configures New.
type Options struct {
	Config config.Server
	APIDoc apidoc.Info

	Modules []Module
	Status  Status

	// HealthHandler replaces the built-in /health handler; nil keeps it.
	HealthHandler gin.HandlerFunc
	// StatusHandler replaces the built-in /status handler, ignoring Status; nil keeps it.
	StatusHandler gin.HandlerFunc

	// Middlewares run on every route, after the built-in request logger and
	// recovery, so what they answer is still logged and carries a request id.
	Middlewares []gin.HandlerFunc
	// APIMiddlewares run on the /api group only.
	APIMiddlewares []gin.HandlerFunc

	// DisableMetrics keeps this server out of the HTTP server metrics even
	// when telemetry is enabled.
	DisableMetrics bool
}
