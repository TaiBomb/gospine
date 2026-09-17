package pcms

import "encoding/json"

// UnmarshalRelation decodes a relation returned either as a populated document
// or as a numeric id.
// P must be T without its UnmarshalJSON method, or decoding recurses forever.
func UnmarshalRelation[T, P any](
	data []byte,
	dst *T,
	fromID func(int) T,
	fromPlain func(P) T,
) error {
	var id int
	if err := json.Unmarshal(data, &id); err == nil {
		*dst = fromID(id)
		return nil
	}

	var plain P
	if err := json.Unmarshal(data, &plain); err != nil {
		return err
	}

	*dst = fromPlain(plain)
	return nil
}
