package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// vendoredSchemaVersion is the only schemaVersion this package writes and
// accepts for vendored.json. Bump deliberately alongside a contract change.
const vendoredSchemaVersion = 1

// Vendored is the on-disk shape of vendored.json — the desired-state list of
// third-party skills vendored into this repo. It is a cross-repo contract: the
// out-of-scope `core` reader parses exactly this shape, so the JSON tags
// (camelCase `schemaVersion`, snake_case `internal_ref`) are load-bearing and
// must not drift. `catalog add` is the producer; `core` is the consumer.
type Vendored struct {
	SchemaVersion int             `json:"schemaVersion"`
	Skills        []VendoredEntry `json:"skills"`
}

// VendoredEntry is one source-pin in vendored.json. `version` is the label the
// user vendored at (a tag or SHA); `commit` is always the resolved 40-character
// lowercase hex SHA — the immutability property the sync workflow relies on.
type VendoredEntry struct {
	Name        string `json:"name"`
	Namespace   string `json:"namespace"`
	Repo        string `json:"repo"`
	Subpath     string `json:"subpath"`
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	InternalRef string `json:"internal_ref"`
}

// LoadVendored parses vendored.json bytes into a Vendored value. Like Load, it
// does not enforce the contract — call ValidateVendored for that. A
// missing-file case is the caller's concern (treat as empty
// Vendored{SchemaVersion: 1}).
func LoadVendored(data []byte) (Vendored, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return Vendored{}, fmt.Errorf("loading vendored: empty input")
	}
	var v Vendored
	if err := json.Unmarshal(data, &v); err != nil {
		return Vendored{}, fmt.Errorf("loading vendored: %w", err)
	}
	return v, nil
}

// ValidateVendored enforces the vendored.json contract: schemaVersion must be
// exactly 1, every entry must have all seven fields populated, and every
// `commit` must be a 40-character lowercase hex Git SHA (reusing commitPattern,
// the same guard the source-pin catalog uses). An empty skills list is valid.
func ValidateVendored(v Vendored) error {
	if v.SchemaVersion != vendoredSchemaVersion {
		return fmt.Errorf("schemaVersion: want %d, got %d", vendoredSchemaVersion, v.SchemaVersion)
	}
	for i, e := range v.Skills {
		switch {
		case e.Name == "":
			return fmt.Errorf("skills[%d].name: must not be empty", i)
		case e.Namespace == "":
			return fmt.Errorf("skills[%d].namespace: must not be empty", i)
		case e.Repo == "":
			return fmt.Errorf("skills[%d].repo: must not be empty", i)
		case e.Subpath == "":
			return fmt.Errorf("skills[%d].subpath: must not be empty", i)
		case e.Version == "":
			return fmt.Errorf("skills[%d].version: must not be empty", i)
		case e.InternalRef == "":
			return fmt.Errorf("skills[%d].internal_ref: must not be empty", i)
		}
		if !commitPattern.MatchString(e.Commit) {
			return fmt.Errorf("skills[%d].commit: must be a 40-char lowercase hex SHA, got %q", i, e.Commit)
		}
	}
	return nil
}

// UpsertVendored returns a new Vendored with e inserted: if an entry with the
// same (namespace, name) exists it is replaced (replaced = true), otherwise e
// is appended (replaced = false). The result's skills[] is always sorted by
// namespace then name so diffs stay minimal and review-friendly. The input v
// is not mutated.
func UpsertVendored(v Vendored, e VendoredEntry) (Vendored, bool) {
	out := make([]VendoredEntry, len(v.Skills))
	copy(out, v.Skills)

	replaced := false
	for i := range out {
		if out[i].Namespace == e.Namespace && out[i].Name == e.Name {
			out[i] = e
			replaced = true
			break
		}
	}
	if !replaced {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})

	schema := v.SchemaVersion
	if schema == 0 {
		schema = vendoredSchemaVersion
	}
	return Vendored{SchemaVersion: schema, Skills: out}, replaced
}

// WriteVendoredAtomic validates v, marshals it with stable key order, and
// atomically writes it via temp-file + rename (mirroring WriteCatalogAtomic).
// A failed write or invalid content leaves no partial file on disk. Stable
// entry order is the caller's responsibility (UpsertVendored guarantees it);
// the writer preserves whatever order skills[] is in.
func WriteVendoredAtomic(path string, v Vendored) error {
	if v.SchemaVersion == 0 {
		v.SchemaVersion = vendoredSchemaVersion
	}
	if err := ValidateVendored(v); err != nil {
		return fmt.Errorf("writing vendored: %w", err)
	}
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling vendored: %w", err)
	}
	body = append(body, '\n')
	return writeAtomic(path, body)
}
