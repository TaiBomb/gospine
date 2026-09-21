package telemetry

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Custom metrics are declared once and automatically rebind to the provider
// installed by Setup. They remain no-ops when telemetry is disabled.

// otherValue replaces a label value outside the allowed ones, or past the cap.
const otherValue = "_OTHER"

// maxLabelValues caps the distinct values of a label declared without allowed values.
const maxLabelValues = 100

// generation changes on every Setup and shutdown, so metrics rebind to the current provider.
var generation atomic.Uint64

// MetricOption configures a custom metric.
type MetricOption func(*definition)

// WithDescription describes the metric, as shown in the # HELP line.
func WithDescription(description string) MetricOption {
	return func(d *definition) { d.description = description }
}

// WithUnit sets the UCUM unit: "s", "By", or an annotation such as "{request}".
func WithUnit(unit string) MetricOption {
	return func(d *definition) { d.unit = unit }
}

// WithBuckets sets the histogram boundaries. Without it, "s" histograms go
// from 5ms to 60s, "By" ones from 256 B to 64 MiB, the others use the SDK defaults.
func WithBuckets(boundaries ...float64) MetricOption {
	return func(d *definition) { d.buckets = boundaries }
}

// WithLabel declares a label; values are then passed in declaration order.
// A value outside allowed is recorded as _OTHER; without allowed values, a
// label keeps its first 100 distinct values and records the rest as _OTHER.
func WithLabel(name string, allowed ...string) MetricOption {
	return func(d *definition) {
		l := &label{key: attribute.Key(name), seen: map[string]struct{}{}}
		if len(allowed) > 0 {
			l.allowed = make(map[string]struct{}, len(allowed))
			for _, v := range allowed {
				l.allowed[v] = struct{}{}
			}
		}
		d.labels = append(d.labels, l)
	}
}

type definition struct {
	name        string
	description string
	unit        string
	buckets     []float64
	labels      []*label

	misuse sync.Once
}

func newDefinition(name string, opts []MetricOption) *definition {
	d := &definition{name: name}
	for _, opt := range opts {
		opt(d)
	}

	return d
}

// attributes turns label values into the attribute set of a measurement.
// A wrong number of values drops the measurement and is reported once.
func (d *definition) attributes(values []string) (metric.MeasurementOption, bool) {
	if len(values) != len(d.labels) {
		d.misuse.Do(func() {
			otel.Handle(fmt.Errorf("telemetry: %s takes %d label values, got %d", d.name, len(d.labels), len(values)))
		})
		return nil, false
	}

	kvs := make([]attribute.KeyValue, len(values))
	for i, l := range d.labels {
		kvs[i] = l.key.String(l.value(values[i]))
	}

	return metric.WithAttributeSet(attribute.NewSet(kvs...)), true
}

func (d *definition) boundaries() []float64 {
	switch {
	case d.buckets != nil:
		return d.buckets
	case d.unit == "s":
		return serverDurationBuckets
	case d.unit == "By":
		return sizeBuckets
	default:
		return nil
	}
}

type label struct {
	key     attribute.Key
	allowed map[string]struct{}

	mu   sync.RWMutex
	seen map[string]struct{}
}

func (l *label) value(v string) string {
	if l.allowed != nil {
		if _, ok := l.allowed[v]; ok {
			return v
		}
		return otherValue
	}

	l.mu.RLock()
	_, seen := l.seen[v]
	l.mu.RUnlock()
	if seen {
		return v
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if _, seen := l.seen[v]; !seen {
		if len(l.seen) >= maxLabelValues {
			return otherValue
		}
		l.seen[v] = struct{}{}
	}

	return v
}

// binding holds the instrument created on the current provider.
type binding[T any] struct {
	current atomic.Pointer[bound[T]]
}

type bound[T any] struct {
	generation uint64
	instrument T
}

func (b *binding[T]) get(create func(metric.Meter) (T, error)) T {
	gen := generation.Load()
	if cur := b.current.Load(); cur != nil && cur.generation == gen {
		return cur.instrument
	}

	// On error the instrument is a no-op one.
	instrument, err := create(Meter(scope()))
	if err != nil {
		otel.Handle(err)
	}
	b.current.Store(&bound[T]{generation: gen, instrument: instrument})

	return instrument
}

// scope names the meter of the custom metrics after the service.
func scope() string {
	if st := installed.Load(); st != nil {
		return st.service
	}

	return "gospine"
}

// Counter is a monotonic sum, such as searches served or errors seen.
type Counter struct {
	def  *definition
	inst binding[metric.Int64Counter]
}

// NewCounter declares a counter.
func NewCounter(name string, opts ...MetricOption) *Counter {
	return &Counter{def: newDefinition(name, opts)}
}

// Inc adds one.
func (c *Counter) Inc(ctx context.Context, labels ...string) {
	c.Add(ctx, 1, labels...)
}

// Add adds n, which must not be negative.
func (c *Counter) Add(ctx context.Context, n int, labels ...string) {
	attrs, ok := c.def.attributes(labels)
	if !ok {
		return
	}

	c.inst.get(func(m metric.Meter) (metric.Int64Counter, error) {
		return m.Int64Counter(c.def.name, metric.WithDescription(c.def.description), metric.WithUnit(c.def.unit))
	}).Add(ctx, int64(n), attrs)
}

// Number is a value a Histogram records.
type Number interface {
	~int | ~int64 | ~float64
}

// Histogram records the distribution of a value, such as results per search
// or the duration of a step.
type Histogram[N Number] struct {
	def  *definition
	inst binding[metric.Float64Histogram]
}

// NewHistogram declares a histogram of N values.
func NewHistogram[N Number](name string, opts ...MetricOption) *Histogram[N] {
	return &Histogram[N]{def: newDefinition(name, opts)}
}

// Record records v.
func (h *Histogram[N]) Record(ctx context.Context, v N, labels ...string) {
	h.record(ctx, float64(v), labels)
}

// Time starts a timer; calling the returned function records the elapsed
// seconds. Meant for histograms in "s":
//
//	defer render.Time(ctx, "wkhtmltopdf")()
func (h *Histogram[N]) Time(ctx context.Context, labels ...string) func() {
	start := time.Now()

	return func() {
		h.record(ctx, time.Since(start).Seconds(), labels)
	}
}

func (h *Histogram[N]) record(ctx context.Context, v float64, labels []string) {
	attrs, ok := h.def.attributes(labels)
	if !ok {
		return
	}

	h.inst.get(func(m metric.Meter) (metric.Float64Histogram, error) {
		opts := []metric.Float64HistogramOption{metric.WithDescription(h.def.description), metric.WithUnit(h.def.unit)}
		if b := h.def.boundaries(); b != nil {
			opts = append(opts, metric.WithExplicitBucketBoundaries(b...))
		}
		return m.Float64Histogram(h.def.name, opts...)
	}).Record(ctx, v, attrs)
}

// Gauge reports a value read at every collection, such as the size of a cache.
type Gauge struct {
	def   *definition
	value func() float64
}

var (
	gaugesMu sync.Mutex
	gauges   []*Gauge
)

// NewGauge declares a gauge whose value is read at every collection; value
// must be safe for concurrent use. Labels are not supported.
func NewGauge(name string, value func() float64, opts ...MetricOption) *Gauge {
	g := &Gauge{def: newDefinition(name, opts), value: value}

	gaugesMu.Lock()
	defer gaugesMu.Unlock()

	gauges = append(gauges, g)
	if Enabled() {
		g.register()
	}

	return g
}

func (g *Gauge) register() {
	_, err := Meter(scope()).Float64ObservableGauge(
		g.def.name,
		metric.WithDescription(g.def.description),
		metric.WithUnit(g.def.unit),
		metric.WithFloat64Callback(func(_ context.Context, o metric.Float64Observer) error {
			o.Observe(g.value())
			return nil
		}),
	)
	if err != nil {
		otel.Handle(err)
	}
}

// registerGauges binds the declared gauges to the provider Setup just installed.
func registerGauges() {
	gaugesMu.Lock()
	defer gaugesMu.Unlock()

	for _, g := range gauges {
		g.register()
	}
}
