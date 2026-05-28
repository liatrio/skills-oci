package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SkillDetail is the on-disk shape of a per-skill detail file at
// data/skills/<namespace>/<name>.json. Wire format mirrors the
// skills-platform's catalog.SkillEntry (snake_case keys) so the
// platform's frontend validateSkillDetail() at
// frontend/src/lib/contract/validate-detail.ts accepts it without
// modification. The platform's discovery indexer is the other producer
// of this shape; skills-oci writes it during `catalog add` so the
// detail page works the moment the catalog row appears.
type SkillDetail struct {
	SchemaVersion int            `json:"schema_version"`
	Namespace     string         `json:"namespace"`
	Name          string         `json:"name"`
	LatestVersion string         `json:"latest_version"`
	Visibility    Visibility     `json:"visibility"`
	Status        Status         `json:"status,omitempty"`
	Description   string         `json:"description,omitempty"`
	RepoURL       string         `json:"repo_url,omitempty"`
	ReadmeURL     string         `json:"readme_url,omitempty"`
	OCIRef        string         `json:"oci_ref"`
	Versions      []SkillVersion `json:"versions"`
}

// SkillVersion is one element of SkillDetail.Versions. Body is the
// verbatim SKILL.md content (frontmatter included) for that version.
type SkillVersion struct {
	Version     string    `json:"version"`
	PublishedAt time.Time `json:"published_at"`
	Body        string    `json:"body"`
}

// ValidateSkillDetail enforces the wire-format contract that the
// platform's validate-detail.ts also enforces: schema_version 2,
// identifier regex on namespace/name, SemVer 2.0.0 on latest_version and
// every entry in versions[], non-empty versions[], latest_version present
// somewhere in versions[], and a non-empty oci_ref. Producer-side check so
// we never write a detail file the frontend would later reject.
//
// schema_version must be exactly 2. WriteSkillDetailAtomic bootstraps a
// zero value to 2 before calling this, so callers may leave it unset; any
// other non-2 value is rejected.
func ValidateSkillDetail(d SkillDetail) error {
	if d.SchemaVersion != 2 {
		return fmt.Errorf("schema_version: want 2, got %d", d.SchemaVersion)
	}
	if !identifierPattern.MatchString(d.Namespace) {
		return fmt.Errorf("namespace: must match %s, got %q", identifierPattern, d.Namespace)
	}
	if !identifierPattern.MatchString(d.Name) {
		return fmt.Errorf("name: must match %s, got %q", identifierPattern, d.Name)
	}
	if !semverPattern.MatchString(d.LatestVersion) {
		return fmt.Errorf("latest_version: must be SemVer 2.0.0, got %q", d.LatestVersion)
	}
	if d.Visibility != VisibilityPublic {
		return fmt.Errorf("visibility: must be %q, got %q", VisibilityPublic, d.Visibility)
	}
	if d.OCIRef == "" {
		return fmt.Errorf("oci_ref: must not be empty")
	}
	if len(d.Versions) == 0 {
		return fmt.Errorf("versions: must contain at least one entry")
	}
	found := false
	for i, v := range d.Versions {
		if !semverPattern.MatchString(v.Version) {
			return fmt.Errorf("versions[%d].version: must be SemVer 2.0.0, got %q", i, v.Version)
		}
		if v.PublishedAt.Location() != time.UTC {
			return fmt.Errorf("versions[%d].published_at: must be UTC, got %s", i, v.PublishedAt.Location())
		}
		if v.PublishedAt.Nanosecond() != 0 {
			return fmt.Errorf("versions[%d].published_at: must be second precision, got %s", i, v.PublishedAt.Format(time.RFC3339Nano))
		}
		if v.Version == d.LatestVersion {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("latest_version %q not present in versions[]", d.LatestVersion)
	}
	return nil
}

// WriteSkillDetailAtomic validates d, marshals it with stable key order,
// MkdirAlls the parent directory if missing (the per-namespace folder
// likely doesn't exist on first add), and atomically renames the temp
// file into place. A failed write leaves no partial file on disk.
func WriteSkillDetailAtomic(path string, d SkillDetail) error {
	if d.SchemaVersion == 0 {
		d.SchemaVersion = 2
	}
	if err := ValidateSkillDetail(d); err != nil {
		return fmt.Errorf("writing skill detail: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating skill detail dir: %w", err)
	}
	body, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling skill detail: %w", err)
	}
	body = append(body, '\n')
	return writeAtomic(path, body)
}
