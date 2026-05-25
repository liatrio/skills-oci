package catalog

import "time"

// Status is the three-state v2 catalog status. See the skills-platform
// data contract: a row is "published" when the registry has at least one
// valid-semver artifact, "unversioned" when artifacts exist but none
// carry a semver tag, and "unpublished" when the source repo exists but
// has produced no artifacts yet.
type Status string

const (
	StatusPublished   Status = "published"
	StatusUnversioned Status = "unversioned"
	StatusUnpublished Status = "unpublished"
)

// Visibility is the v2 catalog visibility field. Only "public" is
// permitted on the wire — private skills are excluded from the catalog
// entirely.
type Visibility string

const (
	VisibilityPublic Visibility = "public"
)

// Catalog is the on-disk shape of catalog.json — the declared inputs for
// vendoring 3rd-party skills into the internal registry. Humans and
// Renovate write this file; CI reads it; the skills-platform frontend
// validator also reads it (validate-catalog.ts), so the wire shape
// matches the v2 contract: snake_case keys, schema_version 2, a top-level
// generated_at timestamp, and the six v2 surface fields on each skill.
// Source-pin fields needed for `catalog sync` (repo, subpath, version,
// commit, internal_ref) are kept as additional fields on each Entry; the
// platform validator tolerates unknown fields, so the hybrid shape
// satisfies both consumers.
type Catalog struct {
	SchemaVersion int       `json:"schema_version"`
	GeneratedAt   time.Time `json:"generated_at"`
	Skills        []Entry   `json:"skills"`
}

// Entry is one row in catalog.json. The first six fields are the v2
// surface contract enforced by the skills-platform frontend validator.
// The remaining fields are the source-pin metadata `catalog sync` uses
// to fetch upstream and push to OCI; they ride alongside the surface
// contract and are ignored by the frontend validator.
type Entry struct {
	// v2 surface fields (validator-required).
	Namespace     string     `json:"namespace"`
	Name          string     `json:"name"`
	LatestVersion string     `json:"latest_version"`
	UpdatedAt     time.Time  `json:"updated_at"`
	Status        Status     `json:"status"`
	Visibility    Visibility `json:"visibility"`

	// v1 source-pin fields (consumed by catalog sync). Marked omitempty so
	// indexer-managed rows — which have no source-pin metadata — serialize
	// as a clean v2-only shape instead of carrying five empty strings.
	Repo        string `json:"repo,omitempty"`
	Subpath     string `json:"subpath,omitempty"`
	Version     string `json:"version,omitempty"`
	Commit      string `json:"commit,omitempty"`
	InternalRef string `json:"internal_ref,omitempty"`
}

// Lock is the on-disk shape of catalog-lock.json — the concrete push state
// produced by `catalog sync`. CI writes this file; humans read it for
// audit and drift detection.
type Lock struct {
	LockfileVersion int         `json:"lockfileVersion"`
	GeneratedAt     string      `json:"generatedAt"`
	Skills          []LockEntry `json:"skills"`
}

// LockEntry records what was actually pushed to the registry for one
// catalog entry. Commit is duplicated from Catalog into Lock so each
// lock entry is self-contained (the lock records what commit was synced,
// not just what registry digest came out).
type LockEntry struct {
	Name        string `json:"name"`
	InternalRef string `json:"internal_ref"`
	Tag         string `json:"tag"`
	Commit      string `json:"commit"`
	Digest      string `json:"digest"`
	Ref         string `json:"ref"`
	SyncedAt    string `json:"syncedAt"`
}
