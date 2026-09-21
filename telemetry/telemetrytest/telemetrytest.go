// Package telemetrytest reads the metrics a test records, gospine's and the
// service's own alike.
//
//	m := telemetrytest.Collect(t)
//	// ... exercise the code ...
//	if got := m.Value("rsa.searches", "search", "structures"); got != 1 { ... }
//
// Collect changes process-wide state: tests using it must not run in parallel.
package telemetrytest

import (
	"context"
	"testing"

	"github.com/TaiBomb/gospine/config"
	"github.com/TaiBomb/gospine/telemetry"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// Metrics reads the metrics recorded since Collect.
type Metrics struct {
	t      testing.TB
	reader *sdkmetric.ManualReader
}

// Collect enables telemetry until the test ends.
func Collect(t testing.TB) *Metrics {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	shutdown, err := telemetry.Setup(
		context.Background(),
		config.Telemetry{Enabled: true, Exporter: telemetry.ExporterPrometheus, Path: "/metrics"},
		telemetry.Service{Name: "test"},
		telemetry.WithReader(reader),
	)
	if err != nil {
		t.Fatalf("telemetrytest: %v", err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	return &Metrics{t: t, reader: reader}
}

// Value returns the value of a counter or gauge, summed over the series that
// carry the given attributes, passed as key, value pairs.
func (m *Metrics) Value(name string, attrs ...string) float64 {
	m.t.Helper()

	match := m.matcher(attrs)

	switch data := m.find(name).(type) {
	case metricdata.Sum[int64]:
		return sumOf(data.DataPoints, match)
	case metricdata.Sum[float64]:
		return sumOf(data.DataPoints, match)
	case metricdata.Gauge[int64]:
		return sumOf(data.DataPoints, match)
	case metricdata.Gauge[float64]:
		return sumOf(data.DataPoints, match)
	default:
		return 0
	}
}

// Count returns how many values a histogram recorded on the matching series.
func (m *Metrics) Count(name string, attrs ...string) uint64 {
	m.t.Helper()

	count, _ := m.histogram(name, attrs)
	return count
}

// Sum returns the total of the values a histogram recorded on the matching series.
func (m *Metrics) Sum(name string, attrs ...string) float64 {
	m.t.Helper()

	_, sum := m.histogram(name, attrs)
	return sum
}

func (m *Metrics) histogram(name string, attrs []string) (count uint64, sum float64) {
	match := m.matcher(attrs)

	switch data := m.find(name).(type) {
	case metricdata.Histogram[int64]:
		for _, dp := range data.DataPoints {
			if match(dp.Attributes) {
				count, sum = count+dp.Count, sum+float64(dp.Sum)
			}
		}
	case metricdata.Histogram[float64]:
		for _, dp := range data.DataPoints {
			if match(dp.Attributes) {
				count, sum = count+dp.Count, sum+dp.Sum
			}
		}
	}

	return count, sum
}

// find returns the data of the named metric; nil when nothing was recorded.
func (m *Metrics) find(name string) metricdata.Aggregation {
	m.t.Helper()

	var rm metricdata.ResourceMetrics
	if err := m.reader.Collect(context.Background(), &rm); err != nil {
		m.t.Fatalf("telemetrytest: collect: %v", err)
	}

	for _, sm := range rm.ScopeMetrics {
		for _, metric := range sm.Metrics {
			if metric.Name == name {
				return metric.Data
			}
		}
	}

	return nil
}

func (m *Metrics) matcher(attrs []string) func(attribute.Set) bool {
	m.t.Helper()

	if len(attrs)%2 != 0 {
		m.t.Fatalf("telemetrytest: attributes go in key, value pairs, got %q", attrs)
	}

	return func(set attribute.Set) bool {
		for i := 0; i < len(attrs); i += 2 {
			if v, ok := set.Value(attribute.Key(attrs[i])); !ok || v.Emit() != attrs[i+1] {
				return false
			}
		}
		return true
	}
}

func sumOf[N int64 | float64](points []metricdata.DataPoint[N], match func(attribute.Set) bool) float64 {
	var total float64
	for _, dp := range points {
		if match(dp.Attributes) {
			total += float64(dp.Value)
		}
	}

	return total
}
