package pcms

import (
	"github.com/TaiBomb/gopcms"
	"github.com/TaiBomb/gospine/paging"
)

// NewPage narrows a gopcms paginated response down to a paging.Page.
func NewPage[T any](docs *gopcms.PaginatedDocs[T]) *paging.Page[T] {
	if docs == nil {
		return nil
	}

	return &paging.Page[T]{
		Docs:        docs.Docs,
		TotalDocs:   docs.TotalDocs,
		Limit:       docs.Limit,
		TotalPages:  docs.TotalPages,
		Page:        docs.Page,
		HasPrevPage: docs.HasPrevPage,
		HasNextPage: docs.HasNextPage,
	}
}

// NewMappedPage is NewPage for listings that decode PayloadCMS's own shape and
// expose another, running every document through mapDoc.
func NewMappedPage[S, T any](docs *gopcms.PaginatedDocs[S], mapDoc func(*S) T) *paging.Page[T] {
	if docs == nil {
		return nil
	}

	mapped := make([]T, 0, len(docs.Docs))
	for i := range docs.Docs {
		mapped = append(mapped, mapDoc(&docs.Docs[i]))
	}

	return &paging.Page[T]{
		Docs:        mapped,
		TotalDocs:   docs.TotalDocs,
		Limit:       docs.Limit,
		TotalPages:  docs.TotalPages,
		Page:        docs.Page,
		HasPrevPage: docs.HasPrevPage,
		HasNextPage: docs.HasNextPage,
	}
}
