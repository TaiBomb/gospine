package paging

// PagedResponse is the response envelope shared by every paginated endpoint.
// Do not rename it: OpenAPI schema names of aliases such as
// "type X = PagedResponse[T]" are derived from it.
type PagedResponse[T any] struct {
	Page        int  `json:"page"        doc:"The page this response carries, starting at 1"`
	Limit       int  `json:"limit"       doc:"The page size actually applied, after defaults and capping"`
	TotalDocs   int  `json:"totalDocs"   doc:"Total number of documents matching the request, across every page"`
	TotalPages  int  `json:"totalPages"  doc:"Total number of pages available at this page size"`
	HasPrevPage bool `json:"hasPrevPage" doc:"Whether a page exists before this one"`
	HasNextPage bool `json:"hasNextPage" doc:"Whether a page exists after this one"`
	Results     []T  `json:"results"     doc:"The documents on this page"`
}

// NewPagedResponse renders page as an envelope; a nil page yields the zero envelope.
func NewPagedResponse[T any](page *Page[T]) PagedResponse[T] {
	if page == nil {
		return PagedResponse[T]{}
	}

	return PagedResponse[T]{
		Page:        page.Page,
		Limit:       page.Limit,
		TotalDocs:   page.TotalDocs,
		TotalPages:  page.TotalPages,
		HasPrevPage: page.HasPrevPage,
		HasNextPage: page.HasNextPage,
		Results:     page.Docs,
	}
}
