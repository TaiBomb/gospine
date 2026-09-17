package apidoc

import (
	"reflect"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// SchemaNamer returns a Huma schema namer that prefixes the default name with
// the area of the type, so equal type names in different areas do not collide.
func SchemaNamer(pkgMarker string, prefixes map[string]string) func(reflect.Type, string) string {
	return func(t reflect.Type, hint string) string {
		return schemaPrefixFor(t, pkgMarker, prefixes) + huma.DefaultSchemaNamer(t, hint)
	}
}

func schemaPrefixFor(t reflect.Type, pkgMarker string, prefixes map[string]string) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	_, area, found := strings.Cut(t.PkgPath(), pkgMarker)
	if !found {
		return ""
	}

	area, _, _ = strings.Cut(area, "/")

	return prefixes[area]
}
