package scm

import (
	"fmt"
	"net/url"
	"strings"
)

// ParseGitHubTreeURL extracts the owner, repo, ref (tag/branch/SHA), and
// subpath from a GitHub tree URL of the form
// `https://github.com/<owner>/<repo>/tree/<ref>/<subpath>`. Only
// github.com is accepted in v1; other forges return a host error. The
// subpath must be non-empty (the URL must point at a directory inside the
// repo, not the repo root). Trailing slashes on the subpath are tolerated.
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
	if len(parts) < 2 {
		return "", "", "", "", fmt.Errorf("parsing tree url: path must contain owner/repo, got %q", u.Path)
	}
	if len(parts) < 3 || parts[2] != "tree" {
		return "", "", "", "", fmt.Errorf("parsing tree url: third segment must be 'tree', got %q", strings.Join(parts, "/"))
	}
	if len(parts) < 4 {
		return "", "", "", "", fmt.Errorf("parsing tree url: missing ref after 'tree'")
	}
	if len(parts) < 5 {
		return "", "", "", "", fmt.Errorf("parsing tree url: missing subpath (URL must point at a directory inside the repo, not the repo root)")
	}

	owner = parts[0]
	repo = parts[1]
	refOrCommit = parts[3]
	subpath = strings.Join(parts[4:], "/")
	return owner, repo, refOrCommit, subpath, nil
}
