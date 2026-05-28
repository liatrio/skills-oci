package scm

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage/memory"
)

// shaPattern matches a 40-hex lowercase Git SHA-1 commit — the same shape
// pkg/catalog.Validate enforces. SHA-256 git refs are not accepted in v1.
var shaPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

// slugPattern matches a two-segment `<owner>/<repo>` slug with safe
// characters only. It is the sole accepted shape for repo: a slug cannot
// carry a scheme (`://`), an `@host` suffix, or extra path segments, so it
// cannot smuggle an arbitrary ls-remote URL (SSRF, file:// reads,
// host-smuggling). Production always pins github.com via remoteURLForResolve.
var slugPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

// remoteURLForResolve builds the upstream git URL the resolver ls-remotes
// against. It is a package-level variable so tests can redirect to file://
// fixtures; production code always returns the github.com HTTPS URL. This
// mirrors remoteURLForFetch in fetch.go.
var remoteURLForResolve = func(repo string) string {
	return "https://github.com/" + strings.TrimSuffix(repo, ".git") + ".git"
}

// ResolveTag returns a commit SHA for tag under repo. If tag is already a
// 40-hex SHA it is returned unchanged with zero network activity so
// callers do not need to special-case the "already-a-SHA" path.
// Otherwise ResolveTag does an HTTPS ls-remote (in-memory storage, no
// disk side effects) and returns the peeled commit for annotated tags,
// or the direct hash for lightweight tags.
//
// repo must be an `<owner>/<repo>` slug; it is rewritten to
// `https://github.com/<owner>/<repo>.git`. Arbitrary URLs (including
// `file://`) are rejected so a hostile slug cannot smuggle an ls-remote
// target; tests override the remoteURLForResolve seam to reach fixtures.
//
// Branches are not considered by ResolveTag. Use ResolveRef for callers
// that want to accept branches as well.
func ResolveTag(ctx context.Context, repo, tag string) (string, error) {
	if tag == "" {
		return "", fmt.Errorf("resolving tag: empty tag")
	}
	if shaPattern.MatchString(tag) {
		return tag, nil
	}
	if repo == "" {
		return "", fmt.Errorf("resolving tag %q: empty repo", tag)
	}
	if !slugPattern.MatchString(repo) {
		return "", fmt.Errorf("resolving tag %q: invalid repo %q (must be an <owner>/<repo> slug matching %s)", tag, repo, slugPattern.String())
	}
	sha, _, err := resolveRefKinds(ctx, repo, tag, refKindTag)
	if err != nil {
		return "", err
	}
	return sha, nil
}

// ResolveRef is the branch-tolerant variant of ResolveTag. It accepts a
// 40-hex SHA, a tag name, or a branch name and returns the commit SHA.
// The immutable bool reports whether the input ref names an immutable
// commit: true for SHAs and tags, false for branches. Callers that need
// to record an audit-stable label (catalog vendoring, for example)
// should overwrite their captured ref string with the returned SHA when
// immutable is false.
//
// When both a tag and a branch share the same name, the tag wins — this
// matches Git's own preference for `refs/tags/*` over `refs/heads/*`
// during ambiguous-ref resolution and keeps existing tag-only callers
// behaviorally unchanged.
func ResolveRef(ctx context.Context, repo, ref string) (sha string, immutable bool, err error) {
	if ref == "" {
		return "", false, fmt.Errorf("resolving ref: empty ref")
	}
	if shaPattern.MatchString(ref) {
		return ref, true, nil
	}
	if repo == "" {
		return "", false, fmt.Errorf("resolving ref %q: empty repo", ref)
	}
	if !slugPattern.MatchString(repo) {
		return "", false, fmt.Errorf("resolving ref %q: invalid repo %q (must be an <owner>/<repo> slug matching %s)", ref, repo, slugPattern.String())
	}
	sha, kind, err := resolveRefKinds(ctx, repo, ref, refKindTag|refKindBranch)
	if err != nil {
		return "", false, err
	}
	return sha, kind == refKindTag, nil
}

type refKind int

const (
	refKindTag refKind = 1 << iota
	refKindBranch
)

// resolveRefKinds is the shared body of ResolveTag and ResolveRef. It
// performs the ls-remote and returns the first matching ref kind in the
// caller's allowed set, preferring tags when both are allowed. SHA
// passthrough, slug validation, and other argument validation are handled
// by the callers so each public function can keep its own error-message
// vocabulary; repo is always a validated <owner>/<repo> slug by this point.
func resolveRefKinds(ctx context.Context, repo, ref string, allowed refKind) (string, refKind, error) {
	remoteURL := remoteURLForResolve(repo)

	remote := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{
		Name: "origin",
		URLs: []string{remoteURL},
	})
	refs, err := remote.ListContext(ctx, &git.ListOptions{PeelingOption: git.AppendPeeled})
	if err != nil {
		return "", 0, fmt.Errorf("resolving ref %q on %s: %w", ref, repo, err)
	}

	tagRefName := plumbing.NewTagReferenceName(ref)
	peeledName := plumbing.ReferenceName(string(tagRefName) + "^{}")
	branchRefName := plumbing.NewBranchReferenceName(ref)

	var tagHash, peeledHash, branchHash string
	for _, r := range refs {
		switch r.Name() {
		case tagRefName:
			tagHash = r.Hash().String()
		case peeledName:
			peeledHash = r.Hash().String()
		case branchRefName:
			branchHash = r.Hash().String()
		}
	}

	if allowed&refKindTag != 0 {
		if peeledHash != "" {
			return peeledHash, refKindTag, nil
		}
		if tagHash != "" {
			return tagHash, refKindTag, nil
		}
	}
	if allowed&refKindBranch != 0 && branchHash != "" {
		return branchHash, refKindBranch, nil
	}

	if allowed == refKindTag {
		return "", 0, fmt.Errorf("tag %q not found on %s", ref, repo)
	}
	return "", 0, fmt.Errorf("ref %q not found on %s (looked for tag and branch)", ref, repo)
}
