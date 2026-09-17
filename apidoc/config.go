// Package apidoc configures the OpenAPI document a service generates from its
// own operations.
//
// Relative to the API base path, the document is published at /openapi.json
// and /openapi.yaml (3.1), /openapi-3.0.json and /openapi-3.0.yaml (3.0.3),
// /docs and /schemas/{schema}.
package apidoc

import "github.com/danielgtaylor/huma/v2"

const schemaRefPrefix = "#/components/schemas/"

// Info describes the API contract in the generated document.
type Info struct {
	Title string
	// Version is the version of the contract, not of the running service.
	Version     string
	Description string

	// PkgMarker is the import path segment marking packages whose types get an
	// area prefix, e.g. "/internal/areas/". Empty keeps Huma's default names.
	PkgMarker string
	// SchemaPrefixes maps the area following PkgMarker to its document prefix.
	SchemaPrefixes map[string]string
}

// NewConfig builds the Huma configuration for an API served under serverURL.
func NewConfig(serverURL string, info Info) huma.Config {
	cfg := huma.DefaultConfig(info.Title, info.Version)
	cfg.Info.Description = info.Description

	if info.PkgMarker != "" {
		cfg.Components.Schemas = huma.NewMapRegistry(
			schemaRefPrefix,
			SchemaNamer(info.PkgMarker, info.SchemaPrefixes),
		)
	}

	// Operations are registered relative to the base path: without it as the
	// server, generated clients would call the wrong URLs.
	cfg.Servers = []*huma.Server{
		{
			URL:         serverURL,
			Description: "This service",
		},
	}

	// Drop Huma's schema-link transformer: it injects a "$schema" property into
	// every body, changing payloads the API already serves.
	cfg.CreateHooks = nil

	return cfg
}
