// Package config holds the settings shared by every service, as structs meant
// to be embedded in the service's own configuration.
//
// Keys are flat: embedding Base, or naming its fields without an envPrefix tag,
// leaves the environment variables as they are.
package config

import "time"

// Server configures the HTTP server.
type Server struct {
	Port         int           `env:"port"         required:"true"`
	ContextPath  string        `env:"contextPath"  default:"/"`
	PingTimeout  time.Duration `env:"pingTimeout"  default:"2s"`
	ReadTimeout  time.Duration `env:"readTimeout"  default:"5s"`
	WriteTimeout time.Duration `env:"writeTimeout" default:"5s"`
}

// Client configures the outbound HTTP client.
type Client struct {
	Timeout time.Duration `env:"clientTimeout" default:"10s"`
}

// Telemetry configures metrics export; it is off unless Enabled.
type Telemetry struct {
	Enabled bool `env:"telemetry.enabled" default:"false"`

	// Exporter is prometheus (scraped) or otlp (pushed).
	Exporter    string `env:"telemetry.exporter" default:"prometheus"`
	ServiceName string `env:"telemetry.serviceName"`

	// Port serves Path on a dedicated listener; 0 serves it on the main HTTP server.
	Port int    `env:"telemetry.port" default:"9464"`
	Path string `env:"telemetry.path" default:"/metrics"`

	// Interval is the otlp push interval.
	Interval time.Duration `env:"telemetry.interval" default:"30s"`

	// OTLPEndpoint is the collector URL; empty falls back to OTEL_EXPORTER_OTLP_*.
	OTLPEndpoint string `env:"telemetry.otlp.endpoint"`
	OTLPInsecure bool   `env:"telemetry.otlp.insecure" default:"false"`
}

// Base carries the settings every service reads.
type Base struct {
	LogLevel        string        `env:"logLevel"        default:"INFO"`
	ShutdownTimeout time.Duration `env:"shutdownTimeout" default:"10s"`

	Client    Client
	Server    Server
	Telemetry Telemetry
}
