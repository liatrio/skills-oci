package scm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pointFetchAt swaps the package-level remote-URL builder so Fetch
// targets a fixture repo instead of github.com for the duration of the
// test. The original builder is restored on cleanup.
func pointFetchAt(t *testing.T, url string) {
	t.Helper()
	orig := remoteURLForFetch
	remoteURLForFetch = func(owner, repo string) string { return url }
	t.Cleanup(func() { remoteURLForFetch = orig })
}

func TestFetch_HappyPath(t *testing.T) {
	fixture := newFixtureRepo(t)
	pointFetchAt(t, fixture.URL)

	dst := t.TempDir()
	ref := SourceRef{
		Owner:   "fixture",
		Repo:    "fixture",
		Subpath: "skills/example",
		Commit:  fixture.V100Commit,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := Fetch(ctx, ref, dst); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	skillMD := filepath.Join(dst, "skills", "example", "SKILL.md")
	body, err := os.ReadFile(skillMD)
	if err != nil {
		t.Fatalf("expected SKILL.md at %s: %v", skillMD, err)
	}
	if !strings.Contains(string(body), "name: example") {
		t.Errorf("SKILL.md missing expected content; got:\n%s", body)
	}
}

func TestFetch_SubpathMissingAtCommit(t *testing.T) {
	fixture := newFixtureRepo(t)
	pointFetchAt(t, fixture.URL)

	dst := t.TempDir()
	// At the initial commit, skills/example does not yet exist.
	ref := SourceRef{
		Owner:   "fixture",
		Repo:    "fixture",
		Subpath: "skills/example",
		Commit:  fixture.InitialCommit,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := Fetch(ctx, ref, dst)
	if err == nil {
		t.Fatal("Fetch accepted missing subpath, want error")
	}
	if !strings.Contains(err.Error(), "skills/example") {
		t.Errorf("error %q should reference the missing subpath", err.Error())
	}
}

func TestFetch_SubpathExistsButNoSKILLMD(t *testing.T) {
	// Build a fixture with a directory containing no SKILL.md.
	fixture := newFixtureRepoWithoutSkillMD(t)
	pointFetchAt(t, fixture.url)

	dst := t.TempDir()
	ref := SourceRef{
		Owner:   "fixture",
		Repo:    "fixture",
		Subpath: "no-skill",
		Commit:  fixture.commit,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := Fetch(ctx, ref, dst)
	if err == nil {
		t.Fatal("Fetch accepted missing SKILL.md, want error")
	}
	if !strings.Contains(err.Error(), "SKILL.md") {
		t.Errorf("error %q should mention SKILL.md", err.Error())
	}
}

func TestFetch_RejectsBadOwner(t *testing.T) {
	dst := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cases := []struct {
		name  string
		owner string
		repo  string
	}{
		{"url-injection in owner", "https://evil.com/x", "skills"},
		{"slash in owner", "anthropics/extra", "skills"},
		{"slash in repo", "anthropics", "skills/extra"},
		{"colon in owner", "host:port", "skills"},
		{"empty owner", "", "skills"},
		{"empty repo", "anthropics", ""},
		{"dot-dot in owner", "..", "skills"},
		{"dot-dot in repo", "anthropics", ".."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref := SourceRef{
				Owner:   tc.owner,
				Repo:    tc.repo,
				Subpath: "skills/example",
				Commit:  "bc6708cbbc37adb919157f04d31e601e68f4b9c2",
			}
			err := Fetch(ctx, ref, dst)
			if err == nil {
				t.Fatal("Fetch accepted unsafe owner/repo, want error")
			}
		})
	}
}

func TestFetch_RejectsBadCommit(t *testing.T) {
	dst := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ref := SourceRef{
		Owner:   "anthropics",
		Repo:    "skills",
		Subpath: "skills/example",
		Commit:  "not-a-sha",
	}
	err := Fetch(ctx, ref, dst)
	if err == nil {
		t.Fatal("Fetch accepted non-SHA commit, want error")
	}
	if !strings.Contains(err.Error(), "commit") {
		t.Errorf("error %q lacks 'commit' context", err.Error())
	}
}

func TestFetch_RejectsEmptySubpath(t *testing.T) {
	dst := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	ref := SourceRef{
		Owner:   "anthropics",
		Repo:    "skills",
		Subpath: "",
		Commit:  "bc6708cbbc37adb919157f04d31e601e68f4b9c2",
	}
	err := Fetch(ctx, ref, dst)
	if err == nil {
		t.Fatal("Fetch accepted empty subpath, want error")
	}
	if !strings.Contains(err.Error(), "subpath") {
		t.Errorf("error %q lacks 'subpath' context", err.Error())
	}
}

func TestFetch_RejectsEmptyDst(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	ref := SourceRef{
		Owner:   "anthropics",
		Repo:    "skills",
		Subpath: "skills/example",
		Commit:  "bc6708cbbc37adb919157f04d31e601e68f4b9c2",
	}
	err := Fetch(ctx, ref, "")
	if err == nil {
		t.Fatal("Fetch accepted empty dst, want error")
	}
	if !strings.Contains(err.Error(), "dst") {
		t.Errorf("error %q lacks 'dst' context", err.Error())
	}
}

func TestFetch_ContextCancellationCleansUp(t *testing.T) {
	fixture := newFixtureRepo(t)
	pointFetchAt(t, fixture.URL)

	dst := t.TempDir()
	ref := SourceRef{
		Owner:   "fixture",
		Repo:    "fixture",
		Subpath: "skills/example",
		Commit:  fixture.V100Commit,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before Fetch starts

	err := Fetch(ctx, ref, dst)
	if err == nil {
		t.Fatal("Fetch ran to completion with cancelled context")
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "context") {
		t.Errorf("error %q should reflect context cancellation", err.Error())
	}
}
