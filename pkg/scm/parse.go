package scm

import (
	"fmt"
	"net/url"
	"strings"
)

// ParseGitHubTreeURL extracts the owner, repo, ref (tag/branch/SHA), and
// subpath from a GitHub URL. Three shapes are accepted, all on github.com
// (other forges return a host error):
//
//   - `https://github.com/<owner>/<repo>/tree/<ref>/<subpath>` — a specific
//     directory at a ref. Returns all four fields.
//   - `https://github.com/<owner>/<repo>/tree/<ref>` — a ref with no
//     subpath (the repo root at that ref). Returns subpath="" so the caller
//     can discover skills across the whole tree.
//   - `https://github.com/<owner>/<repo>` — a bare repo with no ref and no
//     subpath. Returns refOrCommit="" and subpath="" so the caller can
//     resolve the default branch and discover skills repo-wide.
//
// Trailing slashes are tolerated. An empty subpath and an empty ref are
// both valid outputs; the caller decides what to do with them.
func ParseGitHubTreeURL(rawURL string) (owner, repo, refOrCommit, subpath string, err error) {
	if rawURL == "" {
		return "", "", "", "", fmt.Errorf("parsing tree url: empty url")
	}
	u, parseErr := url.Parse(rawURL)
	if parseErr != nil {
		return "", "", "", "", fmt.Errorf("parsing tree url: %w", parseErr)
	}
	if u.Scheme != "https" {
		return "", "", "", "", fmt.Errorf("parsing tree url: scheme must be https, got %q", u.Scheme)
	}
	if u.Host != "github.com" {
		return "", "", "", "", fmt.Errorf("parsing tree url: host must be github.com, got %q", u.Host)
	}

	// Split the path into clean segments.
	trimmed := strings.Trim(u.Path, "/")
	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", "", fmt.Errorf("parsing tree url: path must contain owner/repo, got %q", u.Path)
	}
	owner = parts[0]
	repo = parts[1]

	// Bare repo form: `github.com/<owner>/<repo>` with nothing after. No
	// ref, no subpath — the caller resolves the default branch and scans
	// the whole tree.
	if len(parts) == 2 {
		return owner, repo, "", "", nil
	}

	if parts[2] != "tree" {
		return "", "", "", "", fmt.Errorf("parsing tree url: third segment must be 'tree', got %q", strings.Join(parts, "/"))
	}
	if len(parts) < 4 {
		return "", "", "", "", fmt.Errorf("parsing tree url: missing ref after 'tree'")
	}
	refOrCommit = parts[3]
	// parts[4:] is empty for the repo-root-at-ref form; Join yields "".
	subpath = strings.Join(parts[4:], "/")
	return owner, repo, refOrCommit, subpath, nil
}
