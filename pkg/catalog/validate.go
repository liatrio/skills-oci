package catalog

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// commitPattern enforces the SHA-only constraint that the rest of the
// audit story depends on: a full 40-character lowercase hexadecimal
// Git SHA-1 commit. Branches, mutable tags, and SHA-256 git refs are
// rejected at validation time (SHA-256 git is forward-compatible per
// the data contract but not accepted in v1).
var commitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

// repoSlugPattern requires a bare `<owner>/<repo>` slug — exactly the
// shape Renovate's github-tags datasource consumes.
var repoSlugPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

// identifierPattern matches the v2 namespace/name contract: lowercase
// ASCII alphanumerics with single-hyphen separators. Same regex used by
// frontend/src/lib/contract/identifier.ts so the producer and consumer
// agree about which strings are valid identifiers.
var identifierPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// semverPattern matches SemVer 2.0.0 — major.minor.patch with optional
// pre-release identifiers (after a leading `-`) and build metadata
// (after a leading `+`). Matches the behavior of the platform's
// parseSemver in frontend/src/lib/contract/semver.ts. Numeric components
// must not have leading zeros; pre-release / build identifiers are
// `[0-9A-Za-z-]+` and may be dot-separated.
var semverPattern = regexp.MustCompile(
	`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)` +
		`(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?` +
		`(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`,
)

// sourceVersionPattern is the allow-list for the source-pin `version`
// field. The field accepts SemVer 2.0.0 (with optional leading `v`) or
// a 40-hex commit SHA — nothing else. Branches, moving major-only tags
// (`v1`, `v3`), calendar versions, and arbitrary strings are all
// rejected so every catalog entry pins to an immutable label.
// The orchestrator overwrites this field with the resolved SHA when
// the user supplied a branch ref, so branch inputs never reach the
// validator.
var sourceVersionPattern = regexp.MustCompile(
	`^(v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)` +
		`(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?` +
		`(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?` +
		`|[a-f0-9]{40})$`,
)

// validStatuses is the closed set of values the v2 contract allows on
// the status field. Mirrors the platform's STATUSES array in
// frontend/src/lib/contract/validate-catalog.ts.
var validStatuses = map[Status]struct{}{
	StatusPublished:   {},
	StatusUnversioned: {},
	StatusUnpublished: {},
}

// Validate enforces the v2 catalog contract on c. Each rule produces a
// field-named error so callers can surface field-level feedback. Returns
// nil if c is fully valid. Validation covers both the v2 surface fields
// (validator-required by the skills-platform frontend) and the v1
// source-pin fields (consumed by `catalog sync`); the hybrid shape must
// satisfy both consumers.
func Validate(c Catalog) error {
	if c.SchemaVersion != 2 {
		return fmt.Errorf("schema_version: want 2, got %d", c.SchemaVersion)
	}
	// generated_at is required to be RFC3339 UTC with second precision.
	// The zero value (0001-01-01T00:00:00Z) is intentionally allowed here
	// because the platform validator (isRfc3339Utc in
	// frontend/src/lib/contract/timestamp.ts) only checks the string
	// format — it tolerates a zero timestamp, and the indexer emits one
	// for `status: unpublished` rows. Producer-side hygiene (stamping a
	// real time) lives in the writers, not here.
	if c.GeneratedAt.Location() != time.UTC {
		return fmt.Errorf("generated_at: must be UTC, got %s", c.GeneratedAt.Location())
	}
	if c.GeneratedAt.Nanosecond() != 0 {
		return fmt.Errorf("generated_at: must be second precision (no fractional seconds), got %s", c.GeneratedAt.Format(time.RFC3339Nano))
	}
	// Primary key is (namespace, name). The same name across different
	// namespaces is allowed — the platform indexer can legitimately
	// surface the same skill under multiple orgs (e.g. liatrio/foo and
	// liatrio-labs/foo). Only the (namespace, name) tuple is unique.
	seenKeys := make(map[string]int, len(c.Skills))
	for i, e := range c.Skills {
		if err := validateEntry(e); err != nil {
			label := e.Name
			if label == "" {
				label = fmt.Sprintf("index %d", i)
			}
			return fmt.Errorf("entry %s: %w", label, err)
		}
		key := e.Namespace + "/" + e.Name
		if prev, ok := seenKeys[key]; ok {
			return fmt.Errorf("entry %s: duplicate (namespace, name) %s (already declared at index %d)", e.Name, key, prev)
		}
		seenKeys[key] = i
	}
	return nil
}

func validateEntry(e Entry) error {
	// v2 surface fields. Order chosen to surface the most actionable
	// error first (identifier issues are easier to diagnose than semver
	// or timestamp issues).
	if e.Name == "" {
		return fmt.Errorf("name: must not be empty")
	}
	if !identifierPattern.MatchString(e.Name) {
		return fmt.Errorf("name: must match %s, got %q", identifierPattern, e.Name)
	}
	if !identifierPattern.MatchString(e.Namespace) {
		return fmt.Errorf("namespace: must match %s, got %q", identifierPattern, e.Namespace)
	}
	if _, ok := validStatuses[e.Status]; !ok {
		return fmt.Errorf("status: must be one of published/unversioned/unpublished, got %q", e.Status)
	}
	if e.Status == StatusPublished {
		if !semverPattern.MatchString(e.LatestVersion) {
			return fmt.Errorf("latest_version: must be SemVer 2.0.0 for published rows, got %q", e.LatestVersion)
		}
	}
	// updated_at: format-only check, mirroring the platform validator.
	// The zero value (0001-01-01T00:00:00Z) is intentionally allowed
	// because the indexer emits it for unpublished rows with no known
	// activity timestamp.
	if e.UpdatedAt.Location() != time.UTC {
		return fmt.Errorf("updated_at: must be UTC, got %s", e.UpdatedAt.Location())
	}
	if e.UpdatedAt.Nanosecond() != 0 {
		return fmt.Errorf("updated_at: must be second precision (no fractional seconds), got %s", e.UpdatedAt.Format(time.RFC3339Nano))
	}
	if e.Visibility != VisibilityPublic {
		return fmt.Errorf("visibility: must be %q, got %q", VisibilityPublic, e.Visibility)
	}

	// v1 source-pin fields are collectively optional. The catalog can hold
	// two kinds of rows side-by-side:
	//
	//   - indexer-managed rows (e.g. those written by the skills-platform
	//     discovery indexer when it walks GHCR): no source-pin fields,
	//     because the artifact already exists in the registry and there
	//     is nothing for `catalog sync` to fetch or push for them.
	//   - vendor-managed rows (added via `catalog add`): the full source-
	//     pin set, so `catalog sync` can fetch upstream and republish.
	//
	// A row with a partial source-pin set is malformed — either everything
	// is there or nothing is.
	if HasSourcePin(e) {
		if err := validateSourcePin(e); err != nil {
			return err
		}
	} else if hasAnySourcePin(e) {
		return fmt.Errorf("source-pin fields must be all present or all absent (got partial: repo=%q subpath=%q version=%q commit=%q internal_ref=%q)",
			e.Repo, e.Subpath, e.Version, e.Commit, e.InternalRef)
	}
	return nil
}

// HasSourcePin reports whether e carries the full set of source-pin
// fields `catalog sync` needs to fetch upstream and republish. Rows
// without source-pin fields are indexer-managed and skipped by sync.
func HasSourcePin(e Entry) bool {
	return e.Repo != "" && e.Subpath != "" && e.Version != "" && e.Commit != "" && e.InternalRef != ""
}

// hasAnySourcePin is true when at least one source-pin field is set.
// Used to detect partial source-pin entries, which are rejected.
func hasAnySourcePin(e Entry) bool {
	return e.Repo != "" || e.Subpath != "" || e.Version != "" || e.Commit != "" || e.InternalRef != ""
}

// validateSourcePin enforces the full vendor-managed contract on the
// five source-pin fields. Called only when HasSourcePin(e) is true.
func validateSourcePin(e Entry) error {
	if !repoSlugPattern.MatchString(e.Repo) {
		return fmt.Errorf("repo: must be a bare <owner>/<repo> slug, got %q", e.Repo)
	}
	if strings.HasPrefix(e.Subpath, "/") {
		return fmt.Errorf("subpath: must not have a leading slash, got %q", e.Subpath)
	}
	if strings.Contains(e.Subpath, "\\") {
		return fmt.Errorf("subpath: must use forward slashes, got %q", e.Subpath)
	}
	if strings.Contains(e.Subpath, "..") {
		return fmt.Errorf("subpath: must not contain '..' path segments, got %q", e.Subpath)
	}
	if !sourceVersionPattern.MatchString(e.Version) {
		return fmt.Errorf("version: must be SemVer 2.0.0 (with optional leading 'v') or a 40-char lowercase hex SHA, got %q", e.Version)
	}
	if !commitPattern.MatchString(e.Commit) {
		return fmt.Errorf("commit: must be a 40-char lowercase hex SHA, got %q", e.Commit)
	}
	return nil
}
