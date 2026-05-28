package scm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
)

// remoteURLForFetch builds the upstream git URL Fetch will clone from.
// It is a package-level variable so tests can redirect to file://
// fixtures; production code always returns the github.com HTTPS URL.
var remoteURLForFetch = func(owner, repo string) string {
	return "https://github.com/" + owner + "/" + repo + ".git"
}

// ownerRepoPattern is the same shape pkg/catalog.Validate enforces on
// the repo slug: a single owner segment and a single repo segment with
// safe characters only. Enforced again here so callers that constructed
// a SourceRef by hand cannot smuggle a URL through the Owner field.
var ownerRepoPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Fetch shallow-clones the upstream Git repository identified by ref into
// dst, checks out ref.Commit, and verifies that <dst>/<ref.Subpath>/SKILL.md
// exists. dst must already exist (e.g. created via os.MkdirTemp); Fetch
// owns the worktree state inside dst but does not create or delete dst
// itself. On any error after partial work, dst contents are removed so a
// retry against a fresh temp dir is safe.
func Fetch(ctx context.Context, ref SourceRef, dst string) error {
	if err := validateRef(ref); err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	if dst == "" {
		return fmt.Errorf("fetch: empty dst")
	}

	url := remoteURLForFetch(ref.Owner, ref.Repo)

	// CloneContext with NoCheckout=true does the equivalent of `git init`
	// + add remote + fetch in one call. We pin SingleBranch=false to let
	// the server send the commit even if it's not on a default branch.
	repo, err := git.PlainInit(dst, false)
	if err != nil {
		return fmt.Errorf("fetch: init repo: %w", err)
	}
	if _, err := repo.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{url},
	}); err != nil {
		return wipeAndWrap(dst, fmt.Errorf("fetch: add remote: %w", err))
	}

	commitHash := plumbing.NewHash(ref.Commit)
	// Fetch the specific commit. Depth=1 keeps history minimal. The
	// commit SHA is supplied via RefSpec so the server resolves it
	// directly (requires uploadpack.allowReachableSHA1InWant on the
	// server, which is GitHub's default for public repos and what our
	// fixture sets explicitly).
	if err := repo.FetchContext(ctx, &git.FetchOptions{
		RemoteName: "origin",
		Depth:      1,
		RefSpecs: []config.RefSpec{
			config.RefSpec(ref.Commit + ":refs/skills-oci/fetched"),
		},
	}); err != nil {
		return wipeAndWrap(dst, fmt.Errorf("fetch: fetching commit %s from %s: %w", ref.Commit, url, err))
	}

	wt, err := repo.Worktree()
	if err != nil {
		return wipeAndWrap(dst, fmt.Errorf("fetch: worktree: %w", err))
	}
	if err := wt.Checkout(&git.CheckoutOptions{Hash: commitHash}); err != nil {
		return wipeAndWrap(dst, fmt.Errorf("fetch: checkout %s: %w", ref.Commit, err))
	}

	subpathDir := filepath.Join(dst, filepath.FromSlash(ref.Subpath))
	if info, err := os.Stat(subpathDir); err != nil || !info.IsDir() {
		return wipeAndWrap(dst, fmt.Errorf("fetch: subpath %q not found at commit %s", ref.Subpath, ref.Commit))
	}
	// Defense in depth: a malicious upstream repo can ship a symlink whose
	// target lives outside the checkout. The os.Stat checks above follow
	// symlinks, so an escaping subpath/SKILL.md would otherwise pass and let
	// us vendor arbitrary host files. Reject anything that resolves outside
	// dst. The `..`-segment guard in validateRef covers literal traversal;
	// this covers symlink traversal.
	if ok, err := withinRoot(dst, subpathDir); err != nil || !ok {
		return wipeAndWrap(dst, fmt.Errorf("fetch: subpath %q at commit %s resolves outside the checkout", ref.Subpath, ref.Commit))
	}
	skillMD := filepath.Join(subpathDir, "SKILL.md")
	if _, err := os.Stat(skillMD); err != nil {
		return wipeAndWrap(dst, fmt.Errorf("fetch: subpath %q at commit %s does not contain SKILL.md", ref.Subpath, ref.Commit))
	}
	if ok, err := withinRoot(dst, skillMD); err != nil || !ok {
		return wipeAndWrap(dst, fmt.Errorf("fetch: subpath %q SKILL.md at commit %s resolves outside the checkout", ref.Subpath, ref.Commit))
	}

	return nil
}

// withinRoot reports whether candidate, after fully resolving symlinks,
// stays inside root (also symlink-resolved). Both paths must exist on
// disk. root is resolved too because temp dirs are themselves symlinks on
// some platforms (e.g. macOS /var -> /private/var), so comparing an
// unresolved root against a resolved candidate would yield false negatives.
func withinRoot(root, candidate string) (bool, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false, fmt.Errorf("resolve root %q: %w", root, err)
	}
	realCand, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return false, fmt.Errorf("resolve %q: %w", candidate, err)
	}
	rel, err := filepath.Rel(realRoot, realCand)
	if err != nil {
		return false, err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return false, nil
	}
	return true, nil
}

// validateRef enforces the safety properties that the catalog layer
// validates at load time, replicated here so callers that build a
// SourceRef by hand cannot bypass them.
func validateRef(ref SourceRef) error {
	if ref.Owner == "" || !ownerRepoPattern.MatchString(ref.Owner) {
		return fmt.Errorf("invalid owner %q (must match %s)", ref.Owner, ownerRepoPattern.String())
	}
	if ref.Repo == "" || !ownerRepoPattern.MatchString(ref.Repo) {
		return fmt.Errorf("invalid repo %q (must match %s)", ref.Repo, ownerRepoPattern.String())
	}
	if ref.Subpath == "" {
		return fmt.Errorf("invalid subpath: empty")
	}
	// Reject any `..` segment so filepath.Join(dst, subpath) cannot escape
	// the temp tree and probe arbitrary filesystem paths via os.Stat.
	for _, seg := range strings.Split(ref.Subpath, "/") {
		if seg == ".." {
			return fmt.Errorf("invalid subpath %q (must not contain '..' segments)", ref.Subpath)
		}
	}
	if !shaPattern.MatchString(ref.Commit) {
		return fmt.Errorf("invalid commit %q (must be 40-hex lowercase SHA)", ref.Commit)
	}
	return nil
}

// wipeAndWrap removes everything inside dst (leaving dst itself in place
// because the caller owns the temp-dir lifecycle) and returns err. Used
// on every error path so a retry against the same dst would succeed.
func wipeAndWrap(dst string, err error) error {
	entries, _ := os.ReadDir(dst)
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(dst, e.Name()))
	}
	return err
}
