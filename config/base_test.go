package config

import (
	"testing"
	"time"

	"github.com/Shin-9x/envconfig"
)

type upstream struct {
	BaseURL string `env:"upstream.baseUrl" required:"true"`
	Token   string `env:"upstream.token"   sensitive:"true"`
}

type embeddedConfig struct {
	Base

	Upstream upstream
}

// flatConfig is the layout services had before Base existed.
type flatConfig struct {
	LogLevel        string        `env:"logLevel"        default:"INFO"`
	ShutdownTimeout time.Duration `env:"shutdownTimeout" default:"10s"`

	ClientTimeout time.Duration `env:"clientTimeout" default:"10s"`

	Port         int           `env:"port"          required:"true"`
	ContextPath  string        `env:"contextPath"   default:"/"`
	PingTimeout  time.Duration `env:"pingTimeout"   default:"2s"`
	ReadTimeout  time.Duration `env:"readTimeout"   default:"5s"`
	WriteTimeout time.Duration `env:"writeTimeout"  default:"5s"`

	TelemetryEnabled      bool          `env:"telemetry.enabled"       default:"false"`
	TelemetryExporter     string        `env:"telemetry.exporter"      default:"prometheus"`
	TelemetryServiceName  string        `env:"telemetry.serviceName"`
	TelemetryPort         int           `env:"telemetry.port"          default:"9464"`
	TelemetryPath         string        `env:"telemetry.path"          default:"/metrics"`
	TelemetryInterval     time.Duration `env:"telemetry.interval"      default:"30s"`
	TelemetryOTLPEndpoint string        `env:"telemetry.otlp.endpoint"`
	TelemetryOTLPInsecure bool          `env:"telemetry.otlp.insecure" default:"false"`

	Upstream upstream
}

func setEnv(t *testing.T) {
	t.Helper()

	t.Setenv("port", "8080")
	t.Setenv("contextPath", "/x")
	t.Setenv("writeTimeout", "60s")
	t.Setenv("upstream.baseUrl", "http://upstream")
	t.Setenv("upstream.token", "secret")
}

func TestBase_EmbeddedKeysStayFlat(t *testing.T) {
	setEnv(t)

	cfg, err := envconfig.Load[embeddedConfig]()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Server.Port != 8080 {
		t.Errorf("expected port 8080, got %d", cfg.Server.Port)
	}
	if cfg.Server.ContextPath != "/x" {
		t.Errorf("expected contextPath /x, got %q", cfg.Server.ContextPath)
	}
	if cfg.Server.WriteTimeout != time.Minute {
		t.Errorf("expected writeTimeout 60s, got %v", cfg.Server.WriteTimeout)
	}
	if cfg.Upstream.BaseURL != "http://upstream" {
		t.Errorf("expected the service's own keys to load, got %q", cfg.Upstream.BaseURL)
	}
}

func TestBase_Defaults(t *testing.T) {
	t.Setenv("port", "1")
	t.Setenv("upstream.baseUrl", "http://upstream")

	cfg, err := envconfig.Load[embeddedConfig]()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := Base{
		LogLevel:        "INFO",
		ShutdownTimeout: 10 * time.Second,
		Client:          Client{Timeout: 10 * time.Second},
		Server: Server{
			Port:         1,
			ContextPath:  "/",
			PingTimeout:  2 * time.Second,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 5 * time.Second,
		},
		Telemetry: Telemetry{
			Enabled:  false,
			Exporter: "prometheus",
			Port:     9464,
			Path:     "/metrics",
			Interval: 30 * time.Second,
		},
	}

	if cfg.Base != want {
		t.Fatalf("expected %+v, got %+v", want, cfg.Base)
	}
}

func TestBase_TelemetryKeys(t *testing.T) {
	t.Setenv("port", "1")
	t.Setenv("upstream.baseUrl", "http://upstream")
	t.Setenv("telemetry.enabled", "true")
	t.Setenv("telemetry.exporter", "otlp")
	t.Setenv("telemetry.serviceName", "svc")
	t.Setenv("telemetry.port", "0")
	t.Setenv("telemetry.path", "/prom")
	t.Setenv("telemetry.interval", "10s")
	t.Setenv("telemetry.otlp.endpoint", "http://collector:4318")
	t.Setenv("telemetry.otlp.insecure", "true")

	cfg, err := envconfig.Load[embeddedConfig]()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := Telemetry{
		Enabled:      true,
		Exporter:     "otlp",
		ServiceName:  "svc",
		Port:         0,
		Path:         "/prom",
		Interval:     10 * time.Second,
		OTLPEndpoint: "http://collector:4318",
		OTLPInsecure: true,
	}

	if cfg.Telemetry != want {
		t.Fatalf("expected %+v, got %+v", want, cfg.Telemetry)
	}
}

func TestBase_PortIsRequired(t *testing.T) {
	t.Setenv("upstream.baseUrl", "http://upstream")

	if _, err := envconfig.Load[embeddedConfig](); err == nil {
		t.Fatal("expected an error without port")
	}
}

// TestBase_MaskMatchesTheFlatLayout guards the config line services log at
// startup: moving to Base must not change it.
func TestBase_MaskMatchesTheFlatLayout(t *testing.T) {
	setEnv(t)

	embedded, err := envconfig.Load[embeddedConfig]()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	flat, err := envconfig.Load[flatConfig]()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := envconfig.Mask(embedded)
	want := envconfig.Mask(flat)

	// Mask prefixes the output with the type name, which differs here by design.
	const embeddedName, flatName = "embeddedConfig", "flatConfig"
	if got[len(embeddedName):] != want[len(flatName):] {
		t.Fatalf("masked config changed:\nwant %s\ngot  %s", want, got)
	}
}
