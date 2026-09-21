package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/TaiBomb/gospine/config"
	"github.com/TaiBomb/gospine/logging"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// Values of config.Telemetry.Exporter.
const (
	ExporterPrometheus = "prometheus"
	ExporterOTLP       = "otlp"
)

// otlpMetricsPath is appended to an OTLP endpoint given without a path.
const otlpMetricsPath = "/v1/metrics"

var (
	// durationBuckets are the semconv boundaries for HTTP durations, in seconds.
	durationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10}
	// serverDurationBuckets reach further for exports and PDF generation.
	serverDurationBuckets = append(slices.Clone(durationBuckets), 30, 60)
	// sizeBuckets go from 256 B to 64 MiB, by a factor of 4.
	sizeBuckets = []float64{256, 1 << 10, 4 << 10, 16 << 10, 64 << 10, 256 << 10, 1 << 20, 4 << 20, 16 << 20, 64 << 20}
)

type exporter struct {
	reader sdkmetric.Reader
	// scrape is the Prometheus handler; nil when metrics are pushed.
	scrape    http.Handler
	logFields []any
}

func validate(cfg config.Telemetry) error {
	switch cfg.Exporter {
	case ExporterPrometheus:
		if cfg.Port < 0 || cfg.Port > 65535 {
			return fmt.Errorf("telemetry: invalid telemetry.port %d", cfg.Port)
		}
		if !strings.HasPrefix(cfg.Path, "/") || path.Clean(cfg.Path) != cfg.Path {
			return fmt.Errorf("telemetry: telemetry.path %q must be a clean absolute path", cfg.Path)
		}
		if cfg.Port == 0 && clashesWithBuiltInRoutes(cfg.Path) {
			return fmt.Errorf("telemetry: telemetry.path %q clashes with the routes of the main HTTP server", cfg.Path)
		}
	case ExporterOTLP:
		if cfg.Interval <= 0 {
			return fmt.Errorf("telemetry: telemetry.interval must be positive, got %v", cfg.Interval)
		}
		if cfg.OTLPEndpoint != "" {
			if _, err := otlpEndpointURL(cfg.OTLPEndpoint); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("telemetry: unknown telemetry.exporter %q (want %s or %s)", cfg.Exporter, ExporterPrometheus, ExporterOTLP)
	}

	return nil
}

// clashesWithBuiltInRoutes reports whether p would shadow a route httpserver registers.
func clashesWithBuiltInRoutes(p string) bool {
	return p == "/health" || p == "/status" || p == "/api" || strings.HasPrefix(p, "/api/")
}

func newExporter(ctx context.Context, cfg config.Telemetry, o options) (exporter, error) {
	if o.reader != nil {
		return exporter{reader: o.reader, logFields: []any{"exporter", "reader"}}, nil
	}

	if cfg.Exporter == ExporterOTLP {
		return newOTLPExporter(ctx, cfg)
	}

	return newPrometheusExporter(cfg)
}

// newPrometheusExporter uses a registry of its own, so nothing registered on
// the prometheus default registry leaks into the scrape.
func newPrometheusExporter(cfg config.Telemetry) (exporter, error) {
	registry := prometheus.NewRegistry()

	reader, err := otelprom.New(otelprom.WithRegisterer(registry))
	if err != nil {
		return exporter{}, fmt.Errorf("telemetry: create prometheus exporter: %w", err)
	}

	scrape := promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		// One failing metric must not empty the whole scrape.
		ErrorHandling: promhttp.ContinueOnError,
		ErrorLog:      scrapeErrorLog{},
	})

	return exporter{
		reader:    reader,
		scrape:    scrape,
		logFields: []any{"exporter", ExporterPrometheus, "port", cfg.Port, "path", cfg.Path},
	}, nil
}

func newOTLPExporter(ctx context.Context, cfg config.Telemetry) (exporter, error) {
	var opts []otlpmetrichttp.Option

	endpoint := "OTEL_EXPORTER_OTLP_* environment"
	if cfg.OTLPEndpoint != "" {
		u, err := otlpEndpointURL(cfg.OTLPEndpoint)
		if err != nil {
			return exporter{}, err
		}

		endpoint = u.String()
		opts = append(opts, otlpmetrichttp.WithEndpointURL(endpoint))
	}

	if cfg.OTLPInsecure {
		opts = append(opts, otlpmetrichttp.WithInsecure())
	}

	exp, err := otlpmetrichttp.New(ctx, opts...)
	if err != nil {
		return exporter{}, fmt.Errorf("telemetry: create otlp exporter: %w", err)
	}

	return exporter{
		reader:    sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(cfg.Interval)),
		logFields: []any{"exporter", ExporterOTLP, "endpoint", endpoint, "interval", cfg.Interval.String()},
	}, nil
}

// otlpEndpointURL parses the collector URL, defaulting its path to /v1/metrics
// like OTEL_EXPORTER_OTLP_ENDPOINT does.
func otlpEndpointURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("telemetry: telemetry.otlp.endpoint %q must be an http(s) URL", raw)
	}

	if u.Path == "" || u.Path == "/" {
		u.Path = otlpMetricsPath
	}

	return u, nil
}

// views set histogram boundaries fit for each unit: the SDK defaults suit
// milliseconds, not seconds or bytes.
func views() []sdkmetric.View {
	return []sdkmetric.View{
		histogramView("http.server.request.duration", serverDurationBuckets),
		histogramView("*.client.request.duration", durationBuckets),
		histogramView("http.*.body.size", sizeBuckets),
	}
}

func histogramView(name string, boundaries []float64) sdkmetric.View {
	return sdkmetric.NewView(
		sdkmetric.Instrument{Name: name, Kind: sdkmetric.InstrumentKindHistogram},
		sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{Boundaries: boundaries}},
	)
}

// scrapeErrorLog routes promhttp errors to the service logger.
type scrapeErrorLog struct{}

func (scrapeErrorLog) Println(v ...any) {
	logging.FromContext(context.Background()).Error("Metrics scrape error", "error", fmt.Sprint(v...))
}
