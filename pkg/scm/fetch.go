package scm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

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
	skillMD := filepath.Join(subpathDir, "SKILL.md")
	if _, err := os.Stat(skillMD); err != nil {
		return wipeAndWrap(dst, fmt.Errorf("fetch: subpath %q at commit %s does not contain SKILL.md", ref.Subpath, ref.Commit))
	}

	return nil
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
