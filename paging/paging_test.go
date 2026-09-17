package paging

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

func TestNormalizePagination(t *testing.T) {
	tests := []struct {
		name                string
		page, limit         int
		wantPage, wantLimit int
	}{
		{"values in range are kept", 3, 20, 3, 20},
		{"a page below 1 becomes the first", 0, 20, 1, 20},
		{"a negative page becomes the first", -4, 20, 1, 20},
		{"a missing limit takes the default", 1, 0, 1, 10},
		{"a limit over the cap is capped", 1, 500, 1, 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, limit := NormalizePagination(tt.page, tt.limit, 10, 100)

			if page != tt.wantPage || limit != tt.wantLimit {
				t.Fatalf("expected (%d, %d), got (%d, %d)", tt.wantPage, tt.wantLimit, page, limit)
			}
		})
	}
}

func TestNewInMemoryPage(t *testing.T) {
	docs := []int{1, 2, 3, 4, 5}

	tests := []struct {
		name        string
		page, limit int
		want        Page[int]
	}{
		{
			name: "first page",
			page: 1, limit: 2,
			want: Page[int]{Docs: []int{1, 2}, TotalDocs: 5, Limit: 2, TotalPages: 3, Page: 1, HasNextPage: true},
		},
		{
			name: "last partial page",
			page: 3, limit: 2,
			want: Page[int]{Docs: []int{5}, TotalDocs: 5, Limit: 2, TotalPages: 3, Page: 3, HasPrevPage: true},
		},
		{
			name: "past the end",
			page: 9, limit: 2,
			want: Page[int]{Docs: []int{}, TotalDocs: 5, Limit: 2, TotalPages: 3, Page: 9, HasPrevPage: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewInMemoryPage(docs, tt.page, tt.limit)

			if !reflect.DeepEqual(*got, tt.want) {
				t.Fatalf("expected %+v, got %+v", tt.want, *got)
			}
		})
	}
}

func TestNewInMemoryPage_EmptySetSerializesAnEmptyList(t *testing.T) {
	body, err := json.Marshal(NewInMemoryPage[int](nil, 1, 10))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded map[string]any
	_ = json.Unmarshal(body, &decoded)

	if docs, ok := decoded["docs"].([]any); !ok || len(docs) != 0 {
		t.Fatalf("expected docs to be an empty list, got %v", decoded["docs"])
	}
}

func TestNewPagedResponse(t *testing.T) {
	if got := NewPagedResponse[int](nil); !reflect.DeepEqual(got, PagedResponse[int]{}) {
		t.Fatalf("expected the zero envelope for a nil page, got %+v", got)
	}

	page := &Page[int]{Docs: []int{7}, TotalDocs: 11, Limit: 1, TotalPages: 11, Page: 2, HasPrevPage: true, HasNextPage: true}
	want := PagedResponse[int]{Page: 2, Limit: 1, TotalDocs: 11, TotalPages: 11, HasPrevPage: true, HasNextPage: true, Results: []int{7}}

	if got := NewPagedResponse(page); !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %+v, got %+v", want, got)
	}
}

type article struct{}

// TestPagedResponse_SchemaName pins the OpenAPI schema name services already
// publish for aliases of the envelope.
func TestPagedResponse_SchemaName(t *testing.T) {
	type alias = PagedResponse[article]

	if got := huma.DefaultSchemaNamer(reflect.TypeFor[alias](), ""); got != "PagedResponseArticle" {
		t.Fatalf("expected schema name PagedResponseArticle, got %q", got)
	}
}
