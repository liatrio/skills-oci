package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WriteCatalogAtomic validates c, marshals it as JSON with stable key
// order, writes the bytes to a temp file in the same directory as path,
// and renames into place. Stable key order is a property of the typed
// struct in types.go — the writer does not need to sort. The temp file
// is cleaned up on any error so a failed write leaves no partial state.
func WriteCatalogAtomic(path string, c Catalog) error {
	if err := Validate(c); err != nil {
		return fmt.Errorf("writing catalog: %w", err)
	}
	body, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling catalog: %w", err)
	}
	// Match the existing skills.json convention of a trailing newline so
	// editors and `git diff` behave nicely.
	body = append(body, '\n')
	return writeAtomic(path, body)
}

// writeAtomic is the shared temp-file + rename helper used by both
// WriteCatalogAtomic and WriteLockAtomic.
func writeAtomic(path string, body []byte) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, base+".tmp.*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("renaming temp file into place: %w", err)
	}
	return nil
}
