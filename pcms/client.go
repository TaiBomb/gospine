// Package pcms connects a service to PayloadCMS through gopcms, and holds the
// helpers shared by the area packages built on top of it.
//
// The Client only exposes area-independent operations: area packages build
// their collections on Raw, which also lets a service compose its own types
// (a template cache, say) around the shared connection.
package pcms

import (
	"context"

	"github.com/TaiBomb/gopcms"
)

// Client exposes the operations shared by every PayloadCMS area.
type Client interface {
	// Ping reports whether PayloadCMS is reachable and responding.
	Ping(ctx context.Context) error

	// Raw returns the underlying gopcms client, for area packages to build on.
	Raw() *gopcms.Client
}
