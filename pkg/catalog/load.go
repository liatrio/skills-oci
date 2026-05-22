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

// LoadLock parses catalog-lock.json bytes into a Lock value.
func LoadLock(data []byte) (Lock, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return Lock{}, fmt.Errorf("loading lock: empty input")
	}
	var l Lock
	if err := json.Unmarshal(data, &l); err != nil {
		return Lock{}, fmt.Errorf("loading lock: %w", err)
	}
	return l, nil
}
