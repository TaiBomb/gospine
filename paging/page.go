// Package paging holds the page of a paginated listing and the HTTP envelope
// it is served in.
package paging

// Page is a single page of a paginated listing.
type Page[T any] struct {
	Docs        []T  `json:"docs"`
	TotalDocs   int  `json:"totalDocs"`
	Limit       int  `json:"limit"`
	TotalPages  int  `json:"totalPages"`
	Page        int  `json:"page"`
	HasPrevPage bool `json:"hasPrevPage"`
	HasNextPage bool `json:"hasNextPage"`
}

// NormalizePagination turns a page below 1 into 1, a limit below 1 into
// defaultLimit, and caps the limit at maxLimit.
// Defaults and caps stay per listing: documents differ in weight on the wire.
func NormalizePagination(page, limit, defaultLimit, maxLimit int) (int, int) {
	if page <= 0 {
		page = 1
	}

	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	return page, limit
}

// NewInMemoryPage cuts the requested page out of a full result set, for
// listings sorted locally on values the upstream cannot sort by.
func NewInMemoryPage[T any](docs []T, page, limit int) *Page[T] {
	totalDocs := len(docs)

	totalPages := 0
	if limit > 0 {
		totalPages = (totalDocs + limit - 1) / limit
	}

	start := min((page-1)*limit, totalDocs)
	end := min(start+limit, totalDocs)

	items := docs[start:end]
	if items == nil {
		items = []T{}
	}

	return &Page[T]{
		Docs:        items,
		TotalDocs:   totalDocs,
		Limit:       limit,
		TotalPages:  totalPages,
		Page:        page,
		HasPrevPage: page > 1,
		HasNextPage: end < totalDocs,
	}
}
