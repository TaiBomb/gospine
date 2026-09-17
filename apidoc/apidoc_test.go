package apidoc

import (
	"reflect"
	"testing"

	"github.com/TaiBomb/gospine/logging"
	"github.com/danielgtaylor/huma/v2"
)

type Item struct{}

// The module's own packages stand in for areas: "/gospine/" is the marker and
// the package name the area.
const testMarker = "/gospine/"

func TestSchemaNamer(t *testing.T) {
	namer := SchemaNamer(testMarker, map[string]string{"apidoc": "Doc"})

	tests := []struct {
		name string
		typ  reflect.Type
		want string
	}{
		{"a type inside an area is prefixed", reflect.TypeFor[Item](), "DocItem"},
		{"a pointer is named after its element", reflect.TypeFor[*Item](), "DocItem"},
		{"a type in an unmapped area keeps its name", reflect.TypeFor[logging.CustomLogger](), "CustomLogger"},
		{"a type outside any area keeps its name", reflect.TypeFor[huma.ErrorModel](), "ErrorModel"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := namer(tt.typ, ""); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestNewConfig(t *testing.T) {
	info := Info{Title: "Title", Version: "1.2.3", Description: "Description"}

	cfg := NewConfig("/ctx/api", info)

	if cfg.Info.Title != "Title" || cfg.Info.Version != "1.2.3" || cfg.Info.Description != "Description" {
		t.Errorf("unexpected info: %+v", cfg.Info)
	}

	if len(cfg.Servers) != 1 || cfg.Servers[0].URL != "/ctx/api" || cfg.Servers[0].Description != "This service" {
		t.Errorf("expected the base path as the only server, got %+v", cfg.Servers)
	}

	if cfg.CreateHooks != nil {
		t.Error("expected the schema-link create hook to be dropped")
	}
}

// TestNewConfig_WithoutMarkerKeepsTheDefaultNames covers services that never
// customized schema names: their document must not change.
func TestNewConfig_WithoutMarkerKeepsTheDefaultNames(t *testing.T) {
	cfg := NewConfig("/api", Info{SchemaPrefixes: map[string]string{"apidoc": "Doc"}})

	got := cfg.Components.Schemas.Schema(reflect.TypeFor[Item](), true, "")

	if got.Ref != "#/components/schemas/Item" {
		t.Fatalf("expected the default ref, got %q", got.Ref)
	}
}

func TestNewConfig_WithMarkerPrefixesSchemas(t *testing.T) {
	cfg := NewConfig("/api", Info{
		PkgMarker:      testMarker,
		SchemaPrefixes: map[string]string{"apidoc": "Doc"},
	})

	got := cfg.Components.Schemas.Schema(reflect.TypeFor[Item](), true, "")

	if got.Ref != "#/components/schemas/DocItem" {
		t.Fatalf("expected a prefixed ref, got %q", got.Ref)
	}
}
