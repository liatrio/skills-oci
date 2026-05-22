package catalog

// Catalog is the on-disk shape of catalog.json — the declared inputs for
// vendoring 3rd-party skills into the internal registry. Humans and
// Renovate write this file; CI reads it.
type Catalog struct {
	SchemaVersion int     `json:"schemaVersion"`
	Skills        []Entry `json:"skills"`
}

// Entry is one row in catalog.json. Each field has a distinct consumer:
// Repo is consumed by Renovate's github-tags datasource; Subpath by the
// fetcher; Version by humans and the InternalRef tag; Commit by the
// actual checkout. Combining any of them forces consumers to do parsing.
type Entry struct {
	Name        string `json:"name"`
	Repo        string `json:"repo"`
	Subpath     string `json:"subpath"`
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	InternalRef string `json:"internal_ref"`
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
