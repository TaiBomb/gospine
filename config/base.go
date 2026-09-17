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

// Base carries the settings every service reads.
type Base struct {
	LogLevel        string        `env:"logLevel"        default:"INFO"`
	ShutdownTimeout time.Duration `env:"shutdownTimeout" default:"10s"`

	// Client precedes Server so envconfig.Mask lists keys in their historical order.
	Client Client
	Server Server
}
