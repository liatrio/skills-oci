package scm

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// FixtureFile is one file to seed into the fixture repository.
type FixtureFile struct {
	Path    string // relative to the repo root, forward slashes
	Content string
}

// FixtureRepo is the result of newFixtureRepo: a temp git repo on disk
// that can be served over file:// (via URL) or wrapped with the smart-HTTP
// protocol (via Dir + a dedicated server in another test).
type FixtureRepo struct {
	Dir           string // absolute path on disk
	URL           string // file://<Dir>
	InitialCommit string // SHA of the first commit
	V100Commit    string // SHA tagged as v1.0.0 (lightweight tag)
	V200Commit    string // SHA tagged as v2.0.0 (annotated tag, peeled to this SHA)
	V200TagObject string // SHA of the v2.0.0 annotated tag object itself
}

// newFixtureRepo builds a temp git repo with a deterministic shape that
// the resolver and fetcher tests can rely on:
//
//   - master branch with three commits (git.PlainInit defaults to master)
//   - lightweight tag v1.0.0 -> second commit
//   - annotated tag v2.0.0 -> third commit
//   - skills/example/SKILL.md exists from the second commit onward
func newFixtureRepo(t *testing.T) *FixtureRepo {
	t.Helper()
	dir := t.TempDir()

	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}

	commit := func(message string, files []FixtureFile) string {
		for _, f := range files {
			full := filepath.Join(dir, filepath.FromSlash(f.Path))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if err := os.WriteFile(full, []byte(f.Content), 0o644); err != nil {
				t.Fatalf("WriteFile %s: %v", full, err)
			}
			if _, err := wt.Add(f.Path); err != nil {
				t.Fatalf("Add %s: %v", f.Path, err)
			}
		}
		hash, err := wt.Commit(message, &git.CommitOptions{
			Author: &object.Signature{
				Name:  "fixture",
				Email: "fixture@example.com",
				When:  time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC),
			},
			AllowEmptyCommits: true,
		})
		if err != nil {
			t.Fatalf("Commit %q: %v", message, err)
		}
		return hash.String()
	}

	initial := commit("initial", []FixtureFile{
		{Path: "README.md", Content: "# fixture\n"},
	})

	v100 := commit("add example skill", []FixtureFile{
		{Path: "skills/example/SKILL.md", Content: "---\nname: example\nversion: 1.0.0\nlicense: Apache-2.0\n---\nhello v1\n"},
	})

	// Lightweight tag v1.0.0 -> v100 commit.
	lightRef := plumbing.NewHashReference(
		plumbing.NewTagReferenceName("v1.0.0"),
		plumbing.NewHash(v100),
	)
	if err := repo.Storer.SetReference(lightRef); err != nil {
		t.Fatalf("SetReference v1.0.0: %v", err)
	}

	v200 := commit("update example skill", []FixtureFile{
		{Path: "skills/example/SKILL.md", Content: "---\nname: example\nversion: 2.0.0\nlicense: Apache-2.0\n---\nhello v2\n"},
	})

	// Annotated tag v2.0.0 -> v200 commit. CreateTag with TagOptions yields
	// an annotated tag (the returned ref points at the tag object, which
	// peels to the commit).
	tagRef, err := repo.CreateTag("v2.0.0", plumbing.NewHash(v200), &git.CreateTagOptions{
		Tagger: &object.Signature{
			Name:  "fixture",
			Email: "fixture@example.com",
			When:  time.Date(2026, 5, 22, 12, 30, 0, 0, time.UTC),
		},
		Message: "release v2.0.0\n",
	})
	if err != nil {
		t.Fatalf("CreateTag v2.0.0: %v", err)
	}

	// Configure a no-op section so file:// fetching has the layout
	// other tools expect.
	cfg, err := repo.Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	cfg.Raw.AddOption("uploadpack", "", "allowReachableSHA1InWant", "true")
	if err := repo.SetConfig(cfg); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	// go-git's file:// transport requires the `.git` directory shape; a
	// PlainInit non-bare repo has it. Make sure the repo can be read by
	// a remote-style List call.
	_ = config.NewConfig // ensure config package is referenced for go.mod tidy

	return &FixtureRepo{
		Dir:           dir,
		URL:           "file://" + dir,
		InitialCommit: initial,
		V100Commit:    v100,
		V200Commit:    v200,
		V200TagObject: tagRef.Hash().String(),
	}
}

// ambiguousFixture is a repo where a tag and a branch share the same name
// but point at different commits, used to prove the "tag wins" guarantee.
type ambiguousFixture struct {
	url        string
	tagCommit  string // commit the tag refs/tags/shared points at
	headCommit string // commit the branch refs/heads/shared points at
}

// newFixtureRepoAmbiguousRef builds a repo with BOTH refs/tags/shared and
// refs/heads/shared, each on a DIFFERENT commit, so ResolveRef's documented
// "tag wins over same-named branch" preference can be asserted.
func newFixtureRepoAmbiguousRef(t *testing.T) ambiguousFixture {
	t.Helper()
	dir := t.TempDir()

	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}

	commit := func(message, path, content string) string {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", full, err)
		}
		if _, err := wt.Add(path); err != nil {
			t.Fatalf("Add %s: %v", path, err)
		}
		hash, err := wt.Commit(message, &git.CommitOptions{
			Author: &object.Signature{
				Name:  "fixture",
				Email: "fixture@example.com",
				When:  time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC),
			},
		})
		if err != nil {
			t.Fatalf("Commit %q: %v", message, err)
		}
		return hash.String()
	}

	tagCommit := commit("tag target", "tagged.txt", "tagged\n")
	headCommit := commit("branch target", "branched.txt", "branched\n")

	// Lightweight tag refs/tags/shared -> first commit.
	tagRef := plumbing.NewHashReference(
		plumbing.NewTagReferenceName("shared"),
		plumbing.NewHash(tagCommit),
	)
	if err := repo.Storer.SetReference(tagRef); err != nil {
		t.Fatalf("SetReference tag shared: %v", err)
	}

	// Branch refs/heads/shared -> second (different) commit.
	branchRef := plumbing.NewHashReference(
		plumbing.NewBranchReferenceName("shared"),
		plumbing.NewHash(headCommit),
	)
	if err := repo.Storer.SetReference(branchRef); err != nil {
		t.Fatalf("SetReference branch shared: %v", err)
	}

	cfg, err := repo.Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	cfg.Raw.AddOption("uploadpack", "", "allowReachableSHA1InWant", "true")
	if err := repo.SetConfig(cfg); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	return ambiguousFixture{
		url:        "file://" + dir,
		tagCommit:  tagCommit,
		headCommit: headCommit,
	}
}

// symlinkEscapeFixture is a repo whose checkout contains symlinks whose
// targets live OUTSIDE the repo root, used to prove Fetch rejects
// symlink-based escapes that os.Stat would otherwise silently follow.
type symlinkEscapeFixture struct {
	url    string
	commit string
}

// newFixtureRepoWithSymlinkEscape builds a repo containing:
//
//   - linkdir          -> symlink to an external directory that holds a SKILL.md
//   - realdir/SKILL.md -> symlink to an external file
//
// Both external targets exist on disk for the lifetime of the test, so a
// fetcher that naively follows symlinks (os.Stat) would accept either
// subpath and vendor content from outside the checkout. The fix must
// reject both.
func newFixtureRepoWithSymlinkEscape(t *testing.T) symlinkEscapeFixture {
	t.Helper()
	dir := t.TempDir()

	// External targets, deliberately outside the repo root. A SKILL.md in
	// an external dir makes the "subpath is a symlink" case look valid to a
	// naive checker; an external file makes the "SKILL.md is a symlink" case
	// look valid too.
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "SKILL.md"),
		[]byte("---\nname: evil\nversion: 1.0.0\nlicense: Apache-2.0\n---\npwned\n"), 0o644); err != nil {
		t.Fatalf("write outside SKILL.md: %v", err)
	}
	outsideFile := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}

	// linkdir -> external directory.
	if err := os.Symlink(outsideDir, filepath.Join(dir, "linkdir")); err != nil {
		t.Fatalf("symlink linkdir: %v", err)
	}
	if _, err := wt.Add("linkdir"); err != nil {
		t.Fatalf("Add linkdir: %v", err)
	}

	// realdir/SKILL.md -> external file.
	if err := os.MkdirAll(filepath.Join(dir, "realdir"), 0o755); err != nil {
		t.Fatalf("MkdirAll realdir: %v", err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(dir, "realdir", "SKILL.md")); err != nil {
		t.Fatalf("symlink realdir/SKILL.md: %v", err)
	}
	if _, err := wt.Add("realdir/SKILL.md"); err != nil {
		t.Fatalf("Add realdir/SKILL.md: %v", err)
	}

	hash, err := wt.Commit("seed symlink escapes", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "fixture",
			Email: "fixture@example.com",
			When:  time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC),
		},
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}

	cfg, err := repo.Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	cfg.Raw.AddOption("uploadpack", "", "allowReachableSHA1InWant", "true")
	if err := repo.SetConfig(cfg); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	return symlinkEscapeFixture{url: "file://" + dir, commit: hash.String()}
}

// minimalFixture is a smaller helper for tests that only need a directory
// at a commit, without the multi-tag scaffolding.
type minimalFixture struct {
	url    string
	commit string
}

// newFixtureRepoWithoutSkillMD creates a fixture where a directory exists
// but does NOT contain a SKILL.md, so the fetcher's SKILL.md verification
// path can be exercised.
func newFixtureRepoWithoutSkillMD(t *testing.T) minimalFixture {
	t.Helper()
	dir := t.TempDir()

	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}

	// Write a file under no-skill/ that is NOT SKILL.md.
	noSkillFile := filepath.Join(dir, "no-skill", "readme.md")
	if err := os.MkdirAll(filepath.Dir(noSkillFile), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(noSkillFile, []byte("not a skill"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := wt.Add("no-skill/readme.md"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	hash, err := wt.Commit("seed without SKILL.md", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "fixture",
			Email: "fixture@example.com",
			When:  time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC),
		},
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Match the same uploadpack config the multi-tag fixture uses so
	// Fetch-by-SHA works against this fixture too.
	cfg, err := repo.Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	cfg.Raw.AddOption("uploadpack", "", "allowReachableSHA1InWant", "true")
	if err := repo.SetConfig(cfg); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	return minimalFixture{url: "file://" + dir, commit: hash.String()}
}
