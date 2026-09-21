// Package telemetry sets up OpenTelemetry metrics once per process and
// installs the global MeterProvider that httpserver, httpclient and pcms
// record on.
//
// Setup does nothing unless config.Telemetry.Enabled: the global provider
// stays the no-op one, instruments cost nothing and no port is opened.
package telemetry

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/TaiBomb/gospine/config"
	"github.com/TaiBomb/gospine/logging"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// Service identifies the process in the exported metrics.
type Service struct {
	// Name is the service.name; telemetry.serviceName and OTEL_SERVICE_NAME win over it.
	Name string
	// Version is the service.version; empty falls back to the module version in the build info.
	Version string
}

// Option customizes Setup.
type Option func(*options)

type options struct {
	reader sdkmetric.Reader
}

// WithReader collects metrics through r instead of the configured exporter,
// and serves nothing. Meant for tests, with sdkmetric.NewManualReader.
func WithReader(r sdkmetric.Reader) Option {
	return func(o *options) {
		o.reader = r
	}
}

// state is what Setup installed; nil while telemetry is off.
type state struct {
	// scrape is set when the main HTTP server must serve the metrics.
	scrape http.Handler
	path   string
}

var (
	setupMu   sync.Mutex
	installed atomic.Pointer[state]
)

// Setup installs the global MeterProvider when cfg.Enabled; otherwise it is a no-op.
// The returned shutdown flushes pending metrics and stops the metrics server;
// calling it more than once is harmless.
//
// Call it before building the HTTP client and server, which check Enabled.
func Setup(ctx context.Context, cfg config.Telemetry, svc Service, opts ...Option) (shutdown func(context.Context) error, err error) {
	if !cfg.Enabled {
		return func(context.Context) error { return nil }, nil
	}

	var o options
	for _, opt := range opts {
		opt(&o)
	}

	if err := validate(cfg); err != nil {
		return nil, err
	}

	setupMu.Lock()
	defer setupMu.Unlock()

	if installed.Load() != nil {
		return nil, errors.New("telemetry: already set up")
	}

	res, err := newResource(ctx, cfg, svc)
	if err != nil {
		return nil, err
	}

	exp, err := newExporter(ctx, cfg, o)
	if err != nil {
		return nil, err
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(exp.reader),
		sdkmetric.WithView(views()...),
	)

	var server *http.Server
	if exp.scrape != nil && cfg.Port > 0 {
		if server, err = startServer(cfg.Port, cfg.Path, exp.scrape); err != nil {
			return nil, errors.Join(err, mp.Shutdown(ctx))
		}
	}

	if err := runtime.Start(runtime.WithMeterProvider(mp)); err != nil {
		return nil, errors.Join(err, stopServer(ctx, server), mp.Shutdown(ctx))
	}

	log := logging.FromContext(ctx)

	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		log.Error("OpenTelemetry error", "error", err)
	}))
	otel.SetMeterProvider(mp)

	st := &state{}
	if exp.scrape != nil && cfg.Port == 0 {
		st.scrape, st.path = exp.scrape, cfg.Path
	}
	installed.Store(st)

	serviceName, _ := res.Set().Value(semconv.ServiceNameKey)
	log.Info("Telemetry enabled", append([]any{"service", serviceName.AsString()}, exp.logFields...)...)

	if st.scrape != nil {
		log.Warn("Metrics served on the main HTTP server: reachable wherever its port is", "path", cfg.Path)
	}

	var once sync.Once
	return func(ctx context.Context) error {
		var err error
		once.Do(func() {
			installed.CompareAndSwap(st, nil)
			err = errors.Join(stopServer(ctx, server), mp.Shutdown(ctx))
		})
		return err
	}, nil
}

// Enabled reports whether Setup installed a provider.
func Enabled() bool {
	return installed.Load() != nil
}

// Meter returns a meter from the global provider, for service-specific metrics.
// While telemetry is off it is a no-op meter.
func Meter(name string, opts ...metric.MeterOption) metric.Meter {
	return otel.GetMeterProvider().Meter(name, opts...)
}

// Handler returns the scrape handler and its path when metrics must be served
// by the main HTTP server (prometheus exporter with port 0); ok is false otherwise.
func Handler() (h http.Handler, path string, ok bool) {
	st := installed.Load()
	if st == nil || st.scrape == nil {
		return nil, "", false
	}

	return st.scrape, st.path, true
}
