package pcms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/TaiBomb/gopcms"
	"github.com/TaiBomb/gospine/logging"
	"github.com/TaiBomb/gospine/paging"
	"github.com/danielgtaylor/huma/v2"
)

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer

	previous := logging.Log
	logging.Log = &logging.CustomLogger{
		Logger: slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	t.Cleanup(func() { logging.Log = previous })

	return &buf
}

func lastLogLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")

	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &entry); err != nil {
		t.Fatalf("failed to decode the log line: %v", err)
	}

	return entry
}

func statusOf(t *testing.T, err error) int {
	t.Helper()

	var se huma.StatusError
	if !errors.As(err, &se) {
		t.Fatalf("expected a huma.StatusError, got %T", err)
	}

	return se.GetStatus()
}

func TestUpstream(t *testing.T) {
	apiNotFound := &gopcms.APIError{StatusCode: http.StatusNotFound, RawBody: []byte(`{"errors":[]}`)}
	apiFailure := &gopcms.APIError{StatusCode: http.StatusInternalServerError, RawBody: []byte("boom")}

	tests := []struct {
		name       string
		err        error
		spec       UpstreamSpec
		wantStatus int
		wantLevel  string
	}{
		{
			name:       "an upstream 404 becomes a 404 when configured",
			err:        apiNotFound,
			spec:       UpstreamSpec{LogMessage: "fetch", ClientMessage: "failed", NotFoundMessage: "missing"},
			wantStatus: http.StatusNotFound,
			wantLevel:  "DEBUG",
		},
		{
			name:       "ErrNotFound becomes a 404 when configured",
			err:        fmt.Errorf("lookup: %w", ErrNotFound),
			spec:       UpstreamSpec{LogMessage: "fetch", ClientMessage: "failed", NotFoundMessage: "missing"},
			wantStatus: http.StatusNotFound,
			wantLevel:  "DEBUG",
		},
		{
			name:       "an upstream 404 is a 502 when not configured",
			err:        apiNotFound,
			spec:       UpstreamSpec{LogMessage: "fetch", ClientMessage: "failed"},
			wantStatus: http.StatusBadGateway,
			wantLevel:  "ERROR",
		},
		{
			name:       "any other failure is a 502",
			err:        apiFailure,
			spec:       UpstreamSpec{LogMessage: "fetch", ClientMessage: "failed", NotFoundMessage: "missing"},
			wantStatus: http.StatusBadGateway,
			wantLevel:  "ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := captureLogs(t)

			err := Upstream(context.Background(), tt.err, tt.spec, "id", "42")

			if got := statusOf(t, err); got != tt.wantStatus {
				t.Fatalf("expected status %d, got %d", tt.wantStatus, got)
			}

			entry := lastLogLine(t, buf)
			if entry["level"] != tt.wantLevel {
				t.Errorf("expected level %s, got %v", tt.wantLevel, entry["level"])
			}
			if entry["id"] != "42" {
				t.Errorf("expected the extra log fields, got %v", entry)
			}
		})
	}
}

func TestUpstream_DoesNotExposeUpstreamDetails(t *testing.T) {
	captureLogs(t)

	err := Upstream(
		context.Background(),
		&gopcms.APIError{StatusCode: http.StatusInternalServerError, RawBody: []byte("secret stack trace")},
		UpstreamSpec{LogMessage: "fetch", ClientMessage: "failed to fetch data"},
	)

	if msg := err.Error(); msg != "failed to fetch data" {
		t.Fatalf("expected only the client message, got %q", msg)
	}
}

func TestErrorLogFields(t *testing.T) {
	plain := errors.New("dial tcp: refused")
	if got := ErrorLogFields(plain, "k", "v"); !reflect.DeepEqual(got, []any{"error", plain, "k", "v"}) {
		t.Errorf("unexpected fields for a plain error: %v", got)
	}

	apiErr := &gopcms.APIError{StatusCode: http.StatusBadRequest, RawBody: []byte("bad")}
	want := []any{"error", error(apiErr), "statusCode", http.StatusBadRequest, "responseBody", "bad"}
	if got := ErrorLogFields(apiErr); !reflect.DeepEqual(got, want) {
		t.Errorf("unexpected fields for an API error: %v", got)
	}
}

func TestNewPage(t *testing.T) {
	if NewPage[int](nil) != nil {
		t.Fatal("expected nil for a nil response")
	}

	docs := &gopcms.PaginatedDocs[int]{Docs: []int{1, 2}, TotalDocs: 4, Limit: 2, TotalPages: 2, Page: 1, HasNextPage: true}
	want := &paging.Page[int]{Docs: []int{1, 2}, TotalDocs: 4, Limit: 2, TotalPages: 2, Page: 1, HasNextPage: true}

	if got := NewPage(docs); !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %+v, got %+v", want, got)
	}
}

func TestNewMappedPage(t *testing.T) {
	if NewMappedPage[int](nil, func(*int) string { return "" }) != nil {
		t.Fatal("expected nil for a nil response")
	}

	docs := &gopcms.PaginatedDocs[int]{Docs: []int{1, 2}, TotalDocs: 3, Limit: 2, TotalPages: 2, Page: 2, HasPrevPage: true}
	want := &paging.Page[string]{Docs: []string{"#1", "#2"}, TotalDocs: 3, Limit: 2, TotalPages: 2, Page: 2, HasPrevPage: true}

	got := NewMappedPage(docs, func(n *int) string { return fmt.Sprintf("#%d", *n) })
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %+v, got %+v", want, got)
	}

	empty := NewMappedPage(&gopcms.PaginatedDocs[int]{}, func(*int) string { return "" })
	if empty.Docs == nil {
		t.Fatal("expected an empty, non-nil list")
	}
}

type author struct {
	ID   int
	Name string
}

type plainAuthor author

func (a *author) UnmarshalJSON(data []byte) error {
	return UnmarshalRelation(
		data,
		a,
		func(id int) author { return author{ID: id} },
		func(p plainAuthor) author { return author(p) },
	)
}

func TestUnmarshalRelation(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want author
	}{
		{"an id", `7`, author{ID: 7}},
		{"a populated document", `{"ID":7,"Name":"Ada"}`, author{ID: 7, Name: "Ada"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got author
			if err := json.Unmarshal([]byte(tt.in), &got); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got != tt.want {
				t.Fatalf("expected %+v, got %+v", tt.want, got)
			}
		})
	}

	var got author
	if err := json.Unmarshal([]byte(`"nope"`), &got); err == nil {
		t.Fatal("expected an error for a value that is neither")
	}
}

func TestBuildSort(t *testing.T) {
	spec := SortSpec{
		Fields:       map[string]string{"name": "title", "date": "publishedAt"},
		DefaultKey:   "date",
		DefaultOrder: OrderDesc,
	}

	tests := []struct {
		name        string
		sort, order string
		spec        SortSpec
		want        []string
	}{
		{"a known key ascending", "name", OrderAsc, spec, []string{"title"}},
		{"a known key descending", "name", OrderDesc, spec, []string{"-title"}},
		{"an invalid order takes the default", "name", "sideways", spec, []string{"-title"}},
		{"an unknown key takes the default key and order", "nope", OrderAsc, spec, []string{"-publishedAt"}},
		{"no default key applies no sorting", "nope", OrderAsc, SortSpec{Fields: spec.Fields}, nil},
		{"a default key missing from fields applies no sorting", "nope", OrderAsc, SortSpec{Fields: spec.Fields, DefaultKey: "x"}, nil},
		{"no default order means ascending", "name", "", SortSpec{Fields: spec.Fields}, []string{"title"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BuildSort(tt.sort, tt.order, tt.spec); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}
