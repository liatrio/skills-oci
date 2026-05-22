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
//   - main branch with three commits
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
