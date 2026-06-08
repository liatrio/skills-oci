package scm

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DiscoverSkills walks the checkout rooted at dst, starting from relRoot (a
// forward-slash repo-relative path; "" means the repo root), and returns the
// repo-relative subpaths of every directory that directly contains a
// SKILL.md. Once a skill directory is found its descendants are pruned, so a
// skill's own nested example skills are not vendored separately. Results are
// sorted for deterministic, review-friendly output.
//
// Safety mirrors Fetch: relRoot may not contain `..` segments, and any
// candidate SKILL.md whose real path (after symlink resolution) escapes dst
// is silently excluded rather than recorded — a scan skips hostile entries,
// it does not abort on them. Symlinked directories are never descended into
// (WalkDir reports them via Lstat, so d.IsDir() is false), which excludes
// directory-symlink escapes as well.
func DiscoverSkills(dst, relRoot string) ([]string, error) {
	relRoot = strings.Trim(relRoot, "/")
	for _, seg := range strings.Split(relRoot, "/") {
		if seg == ".." {
			return nil, fmt.Errorf("discover: relRoot %q must not contain '..' segments", relRoot)
		}
	}

	start := dst
	if relRoot != "" {
		start = filepath.Join(dst, filepath.FromSlash(relRoot))
	}
	if info, err := os.Stat(start); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("discover: start path %q not found in checkout", relRoot)
	}

	var found []string
	walkErr := filepath.WalkDir(start, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		skillMD := filepath.Join(path, "SKILL.md")
		if fi, err := os.Stat(skillMD); err != nil || fi.IsDir() {
			return nil // no SKILL.md here; keep descending
		}
		// A SKILL.md whose real path escapes the checkout (symlink target
		// outside dst) is not a legitimate skill. Exclude it but keep
		// descending, so a hostile escaping file cannot also mask any genuine
		// nested skill beneath this directory.
		if ok, err := withinRoot(dst, skillMD); err != nil || !ok {
			return nil
		}
		rel, err := filepath.Rel(dst, path)
		if err != nil {
			return err
		}
		found = append(found, filepath.ToSlash(rel))
		return filepath.SkipDir // prune descendants of a discovered skill
	})
	if walkErr != nil {
		return nil, fmt.Errorf("discover: walking %q: %w", relRoot, walkErr)
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("discover: no SKILL.md found under %q", relRoot)
	}
	sort.Strings(found)
	return found, nil
}
