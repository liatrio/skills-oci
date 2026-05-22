package catalog

import (
	"fmt"
	"regexp"
	"strings"
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

// forbiddenVersions are refs that look like versions but are mutable
// in practice. Accepting any of them defeats the immutable-ref security
// property of catalog vendoring.
var forbiddenVersions = map[string]struct{}{
	"":       {},
	"latest": {},
	"main":   {},
	"master": {},
	"HEAD":   {},
}

// Validate enforces the v1 catalog contract on c. Each rule produces a
// field-named error so callers can surface field-level feedback. Returns
// nil if c is fully valid.
func Validate(c Catalog) error {
	if c.SchemaVersion != 1 {
		return fmt.Errorf("schemaVersion: want 1, got %d", c.SchemaVersion)
	}
	seenNames := make(map[string]int, len(c.Skills))
	for i, e := range c.Skills {
		if err := validateEntry(e); err != nil {
			label := e.Name
			if label == "" {
				label = fmt.Sprintf("index %d", i)
			}
			return fmt.Errorf("entry %s: %w", label, err)
		}
		if prev, ok := seenNames[e.Name]; ok {
			return fmt.Errorf("entry %s: duplicate name (already declared at index %d)", e.Name, prev)
		}
		seenNames[e.Name] = i
	}
	return nil
}

func validateEntry(e Entry) error {
	if e.Name == "" {
		return fmt.Errorf("name: must not be empty")
	}
	if !repoSlugPattern.MatchString(e.Repo) {
		return fmt.Errorf("repo: must be a bare <owner>/<repo> slug, got %q", e.Repo)
	}
	if e.Subpath == "" {
		return fmt.Errorf("subpath: must not be empty")
	}
	if strings.HasPrefix(e.Subpath, "/") {
		return fmt.Errorf("subpath: must not have a leading slash, got %q", e.Subpath)
	}
	if strings.Contains(e.Subpath, "\\") {
		return fmt.Errorf("subpath: must use forward slashes, got %q", e.Subpath)
	}
	if _, forbidden := forbiddenVersions[e.Version]; forbidden {
		return fmt.Errorf("version: must be an immutable tag, got %q", e.Version)
	}
	if !commitPattern.MatchString(e.Commit) {
		return fmt.Errorf("commit: must be a 40-char lowercase hex SHA, got %q", e.Commit)
	}
	if e.InternalRef == "" {
		return fmt.Errorf("internal_ref: must not be empty")
	}
	return nil
}
