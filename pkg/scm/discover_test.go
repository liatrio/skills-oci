package scm

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// writeTree seeds a set of files (forward-slash relative paths) under root,
// creating parent directories as needed. Content is arbitrary; only its
// presence matters to the discovery walk.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", full, err)
		}
	}
}

const skillMD = "---\nname: x\nversion: 1.0.0\nlicense: Apache-2.0\n---\nbody\n"

func TestDiscoverSkills_MultipleUnderSkillsDir(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"skills/alpha/SKILL.md":                skillMD,
		"skills/beta/SKILL.md":                 skillMD,
		"skills/beta/examples/nested/SKILL.md": skillMD, // must be pruned
		"README.md":                            "# repo\n",
	})

	got, err := DiscoverSkills(root, "")
	if err != nil {
		t.Fatalf("DiscoverSkills: %v", err)
	}
	want := []string{"skills/alpha", "skills/beta"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v (nested skill should be pruned, results sorted)", got, want)
	}
}

func TestDiscoverSkills_TopLevelSkillDirs(t *testing.T) {
	// The anthropics/skills layout: each skill is a top-level directory.
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"docx/SKILL.md":            skillMD,
		"pdf/SKILL.md":             skillMD,
		".github/workflows/ci.yml": "on: push\n",
		"docs/contributing.md":     "# docs\n",
	})

	got, err := DiscoverSkills(root, "")
	if err != nil {
		t.Fatalf("DiscoverSkills: %v", err)
	}
	want := []string{"docx", "pdf"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDiscoverSkills_TargetIsItselfASkill(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"skills/alpha/SKILL.md": skillMD,
		"skills/beta/SKILL.md":  skillMD,
	})

	// Scanning from a directory that is itself a skill yields exactly that
	// one entry — this is the single-skill (today's) behavior.
	got, err := DiscoverSkills(root, "skills/alpha")
	if err != nil {
		t.Fatalf("DiscoverSkills: %v", err)
	}
	want := []string{"skills/alpha"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDiscoverSkills_NoneFound(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"docs/readme.md": "# nothing here\n",
	})

	_, err := DiscoverSkills(root, "")
	if err == nil {
		t.Fatal("DiscoverSkills accepted a tree with no SKILL.md, want error")
	}
	if !strings.Contains(err.Error(), "SKILL.md") {
		t.Errorf("error %q should mention SKILL.md", err.Error())
	}
}

func TestDiscoverSkills_RelRootMissing(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"skills/alpha/SKILL.md": skillMD})

	if _, err := DiscoverSkills(root, "does/not/exist"); err == nil {
		t.Fatal("DiscoverSkills accepted a missing relRoot, want error")
	}
}

func TestDiscoverSkills_RejectsTraversal(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"skills/alpha/SKILL.md": skillMD})

	// A `..` segment in relRoot must be rejected before the walk so it cannot
	// escape the checkout tree.
	if _, err := DiscoverSkills(root, "../.."); err == nil {
		t.Fatal("DiscoverSkills accepted a relRoot with '..', want error")
	}
}

func TestDiscoverSkills_ExcludesSymlinkEscapes(t *testing.T) {
	root := t.TempDir()

	// A legitimate skill that must be discovered.
	writeTree(t, root, map[string]string{"skills/good/SKILL.md": skillMD})

	// Escape 1: a directory symlink pointing outside the root, holding a
	// SKILL.md. WalkDir does not descend into symlinked dirs, so it is never
	// even considered.
	outsideDir := t.TempDir()
	writeTree(t, outsideDir, map[string]string{"SKILL.md": skillMD})
	if err := os.Symlink(outsideDir, filepath.Join(root, "linkdir")); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	// Escape 2: a real directory whose SKILL.md is a symlink to a file
	// outside the root. os.Stat would follow it; the within-root check must
	// reject it so the dir is not recorded as a skill.
	if err := os.MkdirAll(filepath.Join(root, "realdir"), 0o755); err != nil {
		t.Fatalf("MkdirAll realdir: %v", err)
	}
	outsideFile := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(root, "realdir", "SKILL.md")); err != nil {
		t.Fatalf("symlink realdir/SKILL.md: %v", err)
	}

	got, err := DiscoverSkills(root, "")
	if err != nil {
		t.Fatalf("DiscoverSkills: %v", err)
	}
	want := []string{"skills/good"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v (symlink escapes must be excluded)", got, want)
	}
}

func TestCheckout_ClonesWithoutRequiringSkillMD(t *testing.T) {
	// Checkout clones the whole repo at a commit without asserting a single
	// subpath's SKILL.md, so discovery can run over the result.
	fixture := newFixtureRepo(t)
	pointFetchAt(t, fixture.URL)

	dst := t.TempDir()
	ref := SourceRef{Owner: "fixture", Repo: "fixture", Subpath: "", Commit: fixture.V200Commit}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := Checkout(ctx, ref, dst); err != nil {
		t.Fatalf("Checkout: %v", err)
	}

	skills, err := DiscoverSkills(dst, "")
	if err != nil {
		t.Fatalf("DiscoverSkills after Checkout: %v", err)
	}
	want := []string{"skills/example"}
	if !reflect.DeepEqual(skills, want) {
		t.Errorf("discovered %v, want %v", skills, want)
	}
}

func TestCheckout_VerifiesNonEmptySubpathExists(t *testing.T) {
	fixture := newFixtureRepo(t)
	pointFetchAt(t, fixture.URL)

	dst := t.TempDir()
	// A real directory at the commit: skills/example exists from v1.0.0 on.
	ref := SourceRef{Owner: "fixture", Repo: "fixture", Subpath: "skills/example", Commit: fixture.V200Commit}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := Checkout(ctx, ref, dst); err != nil {
		t.Fatalf("Checkout with existing subpath: %v", err)
	}
}

func TestCheckout_SubpathMissingErrors(t *testing.T) {
	fixture := newFixtureRepo(t)
	pointFetchAt(t, fixture.URL)

	dst := t.TempDir()
	ref := SourceRef{Owner: "fixture", Repo: "fixture", Subpath: "no/such/dir", Commit: fixture.V200Commit}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := Checkout(ctx, ref, dst)
	if err == nil {
		t.Fatal("Checkout accepted a missing subpath, want error")
	}
	if !strings.Contains(err.Error(), "no/such/dir") {
		t.Errorf("error %q should name the missing subpath", err.Error())
	}
}

func TestCheckout_RejectsBadCommit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	ref := SourceRef{Owner: "fixture", Repo: "fixture", Subpath: "", Commit: "not-a-sha"}
	if err := Checkout(ctx, ref, t.TempDir()); err == nil {
		t.Fatal("Checkout accepted a non-SHA commit, want error")
	}
}
