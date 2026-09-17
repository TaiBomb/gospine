package pcms

// Sort directions accepted by BuildSort.
const (
	OrderAsc  = "asc"
	OrderDesc = "desc"
)

// SortSpec maps public sort parameters to PayloadCMS sorting.
type SortSpec struct {
	// Fields maps public sort keys to PayloadCMS fields.
	Fields map[string]string

	// DefaultKey replaces an unknown sort key; empty applies no sorting.
	DefaultKey string

	// DefaultOrder replaces an invalid direction; empty means ascending.
	DefaultOrder string
}

// BuildSort translates a public sort/order pair into a PayloadCMS sort list,
// or nil when the key is unknown and no default is configured.
func BuildSort(sort, order string, spec SortSpec) []string {
	field, ok := spec.Fields[sort]
	if !ok {
		if spec.DefaultKey == "" {
			return nil
		}

		field, ok = spec.Fields[spec.DefaultKey]
		if !ok {
			return nil
		}

		order = spec.DefaultOrder
	}

	if order != OrderAsc && order != OrderDesc {
		order = spec.DefaultOrder
		if order == "" {
			order = OrderAsc
		}
	}

	if order == OrderDesc {
		return []string{"-" + field}
	}

	return []string{field}
}
