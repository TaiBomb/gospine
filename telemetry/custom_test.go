package telemetry_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TaiBomb/gospine/config"
	"github.com/TaiBomb/gospine/telemetry"
	"github.com/TaiBomb/gospine/telemetry/telemetrytest"
)

// Declared before any Setup, as services do.
var (
	searches = telemetry.NewCounter("test.searches",
		telemetry.WithDescription("Searches served."),
		telemetry.WithLabel("search", "structures", "nearby"),
	)
	results = telemetry.NewHistogram[int]("test.search.results",
		telemetry.WithBuckets(0, 1, 5, 10),
		telemetry.WithLabel("search", "structures", "nearby"),
	)
	steps = telemetry.NewHistogram[float64]("test.step.duration", telemetry.WithUnit("s"))

	cacheSize atomic.Int64
	_         = telemetry.NewGauge("test.cache.items", func() float64 { return float64(cacheSize.Load()) })
)

func TestCustomMetrics_AreNoOpsWithoutSetup(t *testing.T) {
	ctx := context.Background()

	searches.Inc(ctx, "structures")
	results.Record(ctx, 3, "structures")
	steps.Time(ctx)()
}

func TestCounter(t *testing.T) {
	m := telemetrytest.Collect(t)
	ctx := context.Background()

	searches.Inc(ctx, "structures")
	searches.Add(ctx, 2, "structures")
	searches.Inc(ctx, "nearby")

	if got := m.Value("test.searches", "search", "structures"); got != 3 {
		t.Errorf("expected 3 structures searches, got %v", got)
	}
	if got := m.Value("test.searches"); got != 4 {
		t.Errorf("expected 4 searches in all, got %v", got)
	}
}

func TestCounter_FollowsEverySetup(t *testing.T) {
	for i := range 2 {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			m := telemetrytest.Collect(t)

			searches.Inc(context.Background(), "nearby")

			if got := m.Value("test.searches"); got != 1 {
				t.Fatalf("expected the counter to record on this Setup's provider, got %v", got)
			}
		})
	}
}

func TestLabels_OutsideTheAllowedValues(t *testing.T) {
	m := telemetrytest.Collect(t)

	searches.Inc(context.Background(), "invented")

	if got := m.Value("test.searches", "search", "_OTHER"); got != 1 {
		t.Fatalf("expected an unknown value to be recorded as _OTHER, got %v", got)
	}
}

func TestLabels_CappedWithoutAllowedValues(t *testing.T) {
	m := telemetrytest.Collect(t)
	ctx := context.Background()

	ids := telemetry.NewCounter("test.capped", telemetry.WithLabel("id"))
	for i := range 105 {
		ids.Inc(ctx, strconv.Itoa(i))
	}

	if got := m.Value("test.capped", "id", "0"); got != 1 {
		t.Errorf("expected the first values to keep their name, got %v", got)
	}
	if got := m.Value("test.capped", "id", "_OTHER"); got != 5 {
		t.Errorf("expected the values past the cap as _OTHER, got %v", got)
	}
}

func TestLabels_WrongCountDropsTheMeasurement(t *testing.T) {
	m := telemetrytest.Collect(t)
	ctx := context.Background()

	searches.Inc(ctx)
	searches.Inc(ctx, "structures", "extra")

	if got := m.Value("test.searches"); got != 0 {
		t.Fatalf("expected nothing recorded, got %v", got)
	}
}

func TestHistogram(t *testing.T) {
	m := telemetrytest.Collect(t)
	ctx := context.Background()

	results.Record(ctx, 0, "structures")
	results.Record(ctx, 7, "structures")

	if count, sum := m.Count("test.search.results", "search", "structures"), m.Sum("test.search.results", "search", "structures"); count != 2 || sum != 7 {
		t.Fatalf("expected 2 values summing to 7, got %d and %v", count, sum)
	}
}

func TestHistogram_Time(t *testing.T) {
	m := telemetrytest.Collect(t)

	stop := steps.Time(context.Background())
	time.Sleep(10 * time.Millisecond)
	stop()

	if got := m.Count("test.step.duration"); got != 1 {
		t.Fatalf("expected one duration, got %d", got)
	}
	if got := m.Sum("test.step.duration"); got < 0.01 {
		t.Fatalf("expected at least 10ms, got %vs", got)
	}
}

func TestGauge(t *testing.T) {
	cacheSize.Store(7)
	m := telemetrytest.Collect(t)

	if got := m.Value("test.cache.items"); got != 7 {
		t.Errorf("expected 7, got %v", got)
	}

	cacheSize.Store(9)
	if got := m.Value("test.cache.items"); got != 9 {
		t.Errorf("expected the gauge to be read again, got %v", got)
	}
}

func TestGauge_DeclaredAfterSetup(t *testing.T) {
	m := telemetrytest.Collect(t)

	telemetry.NewGauge("test.late.gauge", func() float64 { return 3 })

	if got := m.Value("test.late.gauge"); got != 3 {
		t.Fatalf("expected 3, got %v", got)
	}
}

// TestCustomMetrics_InThePrometheusFormat checks the names and the scope a scrape shows.
func TestCustomMetrics_InThePrometheusFormat(t *testing.T) {
	shutdown, err := telemetry.Setup(
		context.Background(),
		config.Telemetry{Enabled: true, Exporter: telemetry.ExporterPrometheus, Path: "/metrics"},
		telemetry.Service{Name: "rt-test"},
	)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	searches.Inc(context.Background(), "structures")

	h, _, _ := telemetry.Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	for line := range strings.SplitSeq(w.Body.String(), "\n") {
		if strings.HasPrefix(line, "test_searches_total{") &&
			strings.Contains(line, `otel_scope_name="rt-test"`) &&
			strings.Contains(line, `search="structures"`) &&
			strings.HasSuffix(line, " 1") {
			return
		}
	}
	t.Fatalf("expected test_searches_total in the scrape, got:\n%s", w.Body.String())
}

func TestCustomMetrics_Concurrent(t *testing.T) {
	m := telemetrytest.Collect(t)
	ctx := context.Background()

	users := telemetry.NewCounter("test.concurrent", telemetry.WithLabel("user"))

	var wg sync.WaitGroup
	for g := range 20 {
		wg.Go(func() {
			for i := range 50 {
				users.Inc(ctx, strconv.Itoa((g*50+i)%150))
			}
		})
	}
	wg.Wait()

	if got := m.Value("test.concurrent"); got != 1000 {
		t.Fatalf("expected 1000 increments, got %v", got)
	}
	if got := m.Value("test.concurrent", "user", "_OTHER"); got == 0 {
		t.Fatal("expected the values past the cap as _OTHER")
	}
}
