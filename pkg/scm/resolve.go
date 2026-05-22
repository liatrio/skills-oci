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

// ResolveTag returns a commit SHA for tag under repo. If tag is already a
// 40-hex SHA it is returned unchanged with zero network activity so
// callers do not need to special-case the "already-a-SHA" path.
// Otherwise ResolveTag does an HTTPS ls-remote (in-memory storage, no
// disk side effects) and returns the peeled commit for annotated tags,
// or the direct hash for lightweight tags.
//
// repo may be either a `<owner>/<repo>` slug (which is rewritten to
// `https://github.com/<owner>/<repo>.git`) or a fully-qualified URL
// (including `file://` for tests).
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

	remoteURL := repo
	if !strings.Contains(repo, "://") {
		remoteURL = "https://github.com/" + strings.TrimSuffix(repo, ".git") + ".git"
	}

	remote := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{
		Name: "origin",
		URLs: []string{remoteURL},
	})
	refs, err := remote.ListContext(ctx, &git.ListOptions{PeelingOption: git.AppendPeeled})
	if err != nil {
		return "", fmt.Errorf("resolving tag %q on %s: %w", tag, repo, err)
	}

	tagRefName := plumbing.NewTagReferenceName(tag)
	peeledName := plumbing.ReferenceName(string(tagRefName) + "^{}")

	var rawHash, peeledHash string
	for _, r := range refs {
		switch r.Name() {
		case tagRefName:
			rawHash = r.Hash().String()
		case peeledName:
			peeledHash = r.Hash().String()
		}
	}

	if peeledHash != "" {
		return peeledHash, nil
	}
	if rawHash != "" {
		return rawHash, nil
	}
	return "", fmt.Errorf("tag %q not found on %s", tag, repo)
}
