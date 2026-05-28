package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Load parses catalog.json bytes into a Catalog value. Unknown top-level
// fields are tolerated so additive schema changes in future minor versions
// do not break older readers; the contract document is the source of truth
// for which fields are required.
func Load(data []byte) (Catalog, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return Catalog{}, fmt.Errorf("loading catalog: empty input")
	}
	var c Catalog
	if err := json.Unmarshal(data, &c); err != nil {
		return Catalog{}, fmt.Errorf("loading catalog: %w", err)
	}
	return c, nil
}
