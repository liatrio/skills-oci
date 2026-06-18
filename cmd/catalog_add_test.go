package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liatrio/skills-oci/pkg/catalog"
	"github.com/liatrio/skills-oci/pkg/scm"
	"github.com/spf13/cobra"
)

// fakeResolver returns a canned SHA for any ref. Used to avoid network.
// The zero value reports immutable=true (tag-shaped), matching the
// default expectation of every existing test. Tests exercising the
// branch path set mutable=true so ResolveRef reports immutable=false.
type fakeResolver struct {
	commit  string
	mutable bool
	err     error
}

func (f fakeResolver) ResolveRef(_ context.Context, _, _ string) (string, bool, error) {
	if f.err != nil {
		return "", false, f.err
	}
	return f.commit, !f.mutable, nil
}

func (f fakeResolver) ResolveHEAD(_ context.Context, _ string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.commit, nil
}

// fakeFetcher simulates scm.Checkout by writing SKILL.md files into a temp
// checkout, then runs the real scm.DiscoverSkills over them — so discovery
// is exercised for real while the network clone is faked. writeSkillMD
// controls whether the primary subpath gets a SKILL.md; extraSkills lists
// additional repo-relative skill directories to populate (for multi-skill
// discovery tests).
type fakeFetcher struct {
	writeSkillMD bool
	skillMDBody  string
	err          error
	extraSkills  []string
}

func (f fakeFetcher) Checkout(_ context.Context, ref scm.SourceRef, dst string) error {
	if f.err != nil {
		return f.err
	}
	write := func(sub string) error {
		dir := filepath.Join(dst, filepath.FromSlash(sub))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		body := f.skillMDBody
		if body == "" {
			body = "---\nname: fake-skill\nversion: 1.0.0\nlicense: Apache-2.0\n---\nfake body\n"
		}
		return os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644)
	}
	if f.writeSkillMD && ref.Subpath != "" {
		if err := write(ref.Subpath); err != nil {
			return err
		}
	} else if ref.Subpath != "" {
		// Subpath exists but no SKILL.md — discovery will find nothing there.
		if err := os.MkdirAll(filepath.Join(dst, filepath.FromSlash(ref.Subpath)), 0o755); err != nil {
			return err
		}
	}
	for _, s := range f.extraSkills {
		if err := write(s); err != nil {
			return err
		}
	}
	return nil
}

func (fakeFetcher) Discover(dst, relRoot string) ([]string, error) {
	return scm.DiscoverSkills(dst, relRoot)
}

func tempVendoredPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "vendored.json")
}

// loadVendoredFromDisk reads and parses a vendored.json written by the command.
func loadVendoredFromDisk(t *testing.T, path string) catalog.Vendored {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile vendored: %v", err)
	}
	v, err := catalog.LoadVendored(body)
	if err != nil {
		t.Fatalf("LoadVendored: %v", err)
	}
	return v
}

func TestCatalogAdd_WritesVendored(t *testing.T) {
	out := &bytes.Buffer{}
	dir := t.TempDir()
	vendoredPath := filepath.Join(dir, "vendored.json")
	const commit = "bc6708cbbc37adb919157f04d31e601e68f4b9c2"

	opts := addOpts{
		URL:          "https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill",
		Namespace:    "ghcr.io/liatrio/skills",
		VendoredPath: vendoredPath,
	}
	res := fakeResolver{commit: commit}
	fet := fakeFetcher{writeSkillMD: true}

	if err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader(""), opts, configAccessor{}, res, fet); err != nil {
		t.Fatalf("runCatalogAddWithDeps: %v", err)
	}

	v := loadVendoredFromDisk(t, vendoredPath)
	if v.SchemaVersion != 1 {
		t.Errorf("schemaVersion = %d, want 1", v.SchemaVersion)
	}
	if len(v.Skills) != 1 {
		t.Fatalf("len(skills) = %d, want 1", len(v.Skills))
	}
	got := v.Skills[0]
	want := catalog.VendoredEntry{
		Name:        "create-skill",
		Namespace:   "liatrio",
		Repo:        "anthropics/skills",
		Subpath:     "skills/create-skill",
		Commit:      commit,
		InternalRef: "ghcr.io/liatrio/skills/create-skill",
		License:     "Apache-2.0", // from the fakeFetcher's default SKILL.md
	}
	if got != want {
		t.Errorf("entry = %+v, want %+v", got, want)
	}
}

func TestCatalogAdd_WritesVendored_FlagForm(t *testing.T) {
	out := &bytes.Buffer{}
	vendoredPath := tempVendoredPath(t)

	opts := addOpts{
		Repo:         "anthropics/skills",
		Subpath:      "skills/create-skill",
		Version:      "v1.0.0",
		Namespace:    "ghcr.io/liatrio/skills",
		VendoredPath: vendoredPath,
	}
	res := fakeResolver{commit: "bc6708cbbc37adb919157f04d31e601e68f4b9c2"}
	if err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader(""), opts, configAccessor{}, res, fakeFetcher{writeSkillMD: true}); err != nil {
		t.Fatalf("flag form: %v", err)
	}
	v := loadVendoredFromDisk(t, vendoredPath)
	if len(v.Skills) != 1 || v.Skills[0].Name != "create-skill" {
		t.Errorf("flag form result wrong: %+v", v)
	}
}

func TestCatalogAdd_BranchInputPinsResolvedSHA(t *testing.T) {
	// A branch ref (immutable=false) must be pinned to the resolved commit SHA
	// so the vendored row never carries a mutable branch name.
	out := &bytes.Buffer{}
	vendoredPath := tempVendoredPath(t)
	const commit = "bc6708cbbc37adb919157f04d31e601e68f4b9c2"

	opts := addOpts{
		URL:          "https://github.com/anthropics/skills/tree/main/skills/create-skill",
		Namespace:    "ghcr.io/liatrio/skills",
		VendoredPath: vendoredPath,
	}
	res := fakeResolver{commit: commit, mutable: true}
	if err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader(""), opts, configAccessor{}, res, fakeFetcher{writeSkillMD: true}); err != nil {
		t.Fatalf("runCatalogAddWithDeps: %v", err)
	}
	got := loadVendoredFromDisk(t, vendoredPath).Skills[0]
	if got.Commit != commit {
		t.Errorf("Commit = %q, want %q", got.Commit, commit)
	}
}

func TestCatalogAdd_OverwriteRequiresConfirm(t *testing.T) {
	const oldCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const newCommit = "bc6708cbbc37adb919157f04d31e601e68f4b9c2"

	// seed writes a vendored.json with one existing create-skill entry pinned
	// at oldCommit and returns its path.
	seed := func(t *testing.T) string {
		t.Helper()
		path := tempVendoredPath(t)
		v := catalog.Vendored{SchemaVersion: 1, Skills: []catalog.VendoredEntry{{
			Name: "create-skill", Namespace: "liatrio", Repo: "anthropics/skills",
			Subpath: "skills/create-skill", Commit: oldCommit,
			InternalRef: "ghcr.io/liatrio/skills/create-skill",
		}}}
		if err := catalog.WriteVendoredAtomic(path, v); err != nil {
			t.Fatalf("seed: %v", err)
		}
		return path
	}

	baseOpts := func(path string) addOpts {
		return addOpts{
			URL:          "https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill",
			Namespace:    "ghcr.io/liatrio/skills",
			VendoredPath: path,
		}
	}

	t.Run("piped n aborts without writing", func(t *testing.T) {
		path := seed(t)
		out := &bytes.Buffer{}
		err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader("n\n"), baseOpts(path), configAccessor{}, fakeResolver{commit: newCommit}, fakeFetcher{writeSkillMD: true})
		if err != nil {
			t.Fatalf("declined overwrite should not error: %v", err)
		}
		if got := loadVendoredFromDisk(t, path).Skills[0].Commit; got != oldCommit {
			t.Errorf("entry overwritten despite 'n': commit = %q, want %q", got, oldCommit)
		}
	})

	t.Run("empty answer (just enter) aborts", func(t *testing.T) {
		path := seed(t)
		out := &bytes.Buffer{}
		if err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader("\n"), baseOpts(path), configAccessor{}, fakeResolver{commit: newCommit}, fakeFetcher{writeSkillMD: true}); err != nil {
			t.Fatalf("empty answer should not error: %v", err)
		}
		if got := loadVendoredFromDisk(t, path).Skills[0].Commit; got != oldCommit {
			t.Errorf("entry overwritten on empty answer: commit = %q, want %q", got, oldCommit)
		}
	})

	t.Run("piped y overwrites", func(t *testing.T) {
		path := seed(t)
		out := &bytes.Buffer{}
		if err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader("y\n"), baseOpts(path), configAccessor{}, fakeResolver{commit: newCommit}, fakeFetcher{writeSkillMD: true}); err != nil {
			t.Fatalf("confirmed overwrite errored: %v", err)
		}
		if got := loadVendoredFromDisk(t, path).Skills[0].Commit; got != newCommit {
			t.Errorf("entry not overwritten despite 'y': commit = %q, want %q", got, newCommit)
		}
	})

	t.Run("--plain without -y exits non-zero and does not write", func(t *testing.T) {
		path := seed(t)
		out := &bytes.Buffer{}
		o := baseOpts(path)
		o.Plain = true
		err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader(""), o, configAccessor{}, fakeResolver{commit: newCommit}, fakeFetcher{writeSkillMD: true})
		if err == nil {
			t.Fatal("--plain overwrite without -y should error")
		}
		if !strings.Contains(err.Error(), "-y") {
			t.Errorf("error %q should instruct passing -y/--yes", err.Error())
		}
		if got := loadVendoredFromDisk(t, path).Skills[0].Commit; got != oldCommit {
			t.Errorf("entry overwritten under --plain without -y: commit = %q", got)
		}
	})

	t.Run("--plain with -y overwrites", func(t *testing.T) {
		path := seed(t)
		out := &bytes.Buffer{}
		o := baseOpts(path)
		o.Plain = true
		o.Yes = true
		if err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader(""), o, configAccessor{}, fakeResolver{commit: newCommit}, fakeFetcher{writeSkillMD: true}); err != nil {
			t.Fatalf("--plain -y errored: %v", err)
		}
		if got := loadVendoredFromDisk(t, path).Skills[0].Commit; got != newCommit {
			t.Errorf("entry not overwritten under --plain -y: commit = %q, want %q", got, newCommit)
		}
	})
}

func TestCatalogAdd_DryRun(t *testing.T) {
	out := &bytes.Buffer{}
	vendoredPath := tempVendoredPath(t)

	opts := addOpts{
		URL:          "https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill",
		Namespace:    "ghcr.io/liatrio/skills",
		VendoredPath: vendoredPath,
		DryRun:       true,
	}
	res := fakeResolver{commit: "bc6708cbbc37adb919157f04d31e601e68f4b9c2"}
	if err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader(""), opts, configAccessor{}, res, fakeFetcher{writeSkillMD: true}); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if _, err := os.Stat(vendoredPath); !os.IsNotExist(err) {
		t.Errorf("vendored.json should not exist after dry run, got %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "would add entry") {
		t.Errorf("dry-run output should announce would-be add; got:\n%s", got)
	}
	// The resolved entry's coordinates should appear in the printed JSON.
	if !strings.Contains(got, "create-skill") || !strings.Contains(got, "ghcr.io/liatrio/skills/create-skill") {
		t.Errorf("dry-run output missing resolved entry fields; got:\n%s", got)
	}
}

func TestCatalogAdd_MultiSkillDiscovery(t *testing.T) {
	// A repo-root URL (no subpath) must discover every skill and write one
	// entry per skill, sorted, each auto-named from its directory.
	out := &bytes.Buffer{}
	vendoredPath := tempVendoredPath(t)
	const commit = "bc6708cbbc37adb919157f04d31e601e68f4b9c2"

	opts := addOpts{
		URL:          "https://github.com/anthropics/skills/tree/v1.0.0",
		Namespace:    "ghcr.io/liatrio/skills",
		VendoredPath: vendoredPath,
	}
	res := fakeResolver{commit: commit}
	// Intentionally out of order to prove the output is sorted.
	fet := fakeFetcher{extraSkills: []string{"skills/beta", "skills/alpha"}}

	if err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader(""), opts, configAccessor{}, res, fet); err != nil {
		t.Fatalf("runCatalogAddWithDeps: %v", err)
	}

	v := loadVendoredFromDisk(t, vendoredPath)
	if len(v.Skills) != 2 {
		t.Fatalf("len(skills) = %d, want 2; entries=%+v", len(v.Skills), v.Skills)
	}
	if v.Skills[0].Name != "alpha" || v.Skills[1].Name != "beta" {
		t.Errorf("entries not sorted by name: got %q, %q", v.Skills[0].Name, v.Skills[1].Name)
	}
	for _, e := range v.Skills {
		if e.Repo != "anthropics/skills" || e.Commit != commit {
			t.Errorf("entry %q has wrong coordinates: %+v", e.Name, e)
		}
		if e.Namespace != "liatrio" {
			t.Errorf("entry %q namespace = %q, want liatrio", e.Name, e.Namespace)
		}
		if e.Subpath != "skills/"+e.Name {
			t.Errorf("entry %q subpath = %q, want skills/%s", e.Name, e.Subpath, e.Name)
		}
		if e.InternalRef != "ghcr.io/liatrio/skills/"+e.Name {
			t.Errorf("entry %q internal_ref = %q", e.Name, e.InternalRef)
		}
	}
	if got := out.String(); !strings.Contains(got, "discovered 2 skill(s)") || !strings.Contains(got, "wrote 2 entries") {
		t.Errorf("output missing multi-skill banners; got:\n%s", got)
	}
}

func TestCatalogAdd_ContainerSubpathDiscovery(t *testing.T) {
	// A subpath that is a container (not itself a skill) discovers the skills
	// beneath it — this is discovery mode even with an explicit subpath.
	out := &bytes.Buffer{}
	vendoredPath := tempVendoredPath(t)
	const commit = "bc6708cbbc37adb919157f04d31e601e68f4b9c2"

	opts := addOpts{
		URL:          "https://github.com/anthropics/skills/tree/v1.0.0/skills",
		Namespace:    "ghcr.io/liatrio/skills",
		VendoredPath: vendoredPath,
	}
	res := fakeResolver{commit: commit}
	fet := fakeFetcher{extraSkills: []string{"skills/one", "skills/two"}}

	if err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader(""), opts, configAccessor{}, res, fet); err != nil {
		t.Fatalf("runCatalogAddWithDeps: %v", err)
	}
	v := loadVendoredFromDisk(t, vendoredPath)
	if len(v.Skills) != 2 || v.Skills[0].Name != "one" || v.Skills[1].Name != "two" {
		t.Errorf("container discovery wrong: %+v", v.Skills)
	}
	if !strings.Contains(out.String(), "discovered 2 skill(s) under subpath skills") {
		t.Errorf("output should name the container subpath; got:\n%s", out.String())
	}
}

func TestCatalogAdd_BareRepoResolvesDefaultBranch(t *testing.T) {
	// A bare repo URL (no /tree/<ref>) resolves the default-branch HEAD and
	// records the resolved SHA as the version (the branch is mutable).
	out := &bytes.Buffer{}
	vendoredPath := tempVendoredPath(t)
	const commit = "bc6708cbbc37adb919157f04d31e601e68f4b9c2"

	opts := addOpts{
		URL:          "https://github.com/vercel-labs/agent-skills",
		Namespace:    "ghcr.io/liatrio/skills",
		VendoredPath: vendoredPath,
	}
	res := fakeResolver{commit: commit}
	fet := fakeFetcher{extraSkills: []string{"skills/web"}}

	if err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader(""), opts, configAccessor{}, res, fet); err != nil {
		t.Fatalf("runCatalogAddWithDeps: %v", err)
	}
	v := loadVendoredFromDisk(t, vendoredPath)
	if len(v.Skills) != 1 {
		t.Fatalf("len(skills) = %d, want 1", len(v.Skills))
	}
	got := v.Skills[0]
	if got.Repo != "vercel-labs/agent-skills" {
		t.Errorf("repo = %q, want vercel-labs/agent-skills", got.Repo)
	}
	if got.Commit != commit {
		t.Errorf("commit = %q, want resolved SHA %q (default branch is mutable)", got.Commit, commit)
	}
	if !strings.Contains(out.String(), "resolving vercel-labs/agent-skills@HEAD") {
		t.Errorf("output should announce default-branch resolution; got:\n%s", out.String())
	}
}

func TestCatalogAdd_NameAndInternalRefRejectedInMultiMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    addOpts
	}{
		{"name set", addOpts{Name: "custom"}},
		{"internal-ref set", addOpts{InternalRef: "ghcr.io/x/skills/custom"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := &bytes.Buffer{}
			vendoredPath := tempVendoredPath(t)
			o := tc.o
			o.URL = "https://github.com/anthropics/skills/tree/v1.0.0"
			o.Namespace = "ghcr.io/liatrio/skills"
			o.VendoredPath = vendoredPath
			res := fakeResolver{commit: "bc6708cbbc37adb919157f04d31e601e68f4b9c2"}
			fet := fakeFetcher{extraSkills: []string{"skills/alpha", "skills/beta"}}

			err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader(""), o, configAccessor{}, res, fet)
			if err == nil {
				t.Fatal("expected error when --name/--internal-ref used with multiple discovered skills")
			}
			if !strings.Contains(err.Error(), "single skill") {
				t.Errorf("error %q should explain the single-skill restriction", err.Error())
			}
			if _, statErr := os.Stat(vendoredPath); !os.IsNotExist(statErr) {
				t.Errorf("vendored.json should not be written on rejection")
			}
		})
	}
}

func TestCatalogAdd_MultiPerSkillOverwritePrompt(t *testing.T) {
	const oldCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const newCommit = "bc6708cbbc37adb919157f04d31e601e68f4b9c2"

	// seed writes a vendored.json that already pins liatrio/alpha at oldCommit
	// (beta is new). Discovery returns alpha + beta, so only alpha prompts.
	seed := func(t *testing.T) string {
		t.Helper()
		path := tempVendoredPath(t)
		v := catalog.Vendored{SchemaVersion: 1, Skills: []catalog.VendoredEntry{{
			Name: "alpha", Namespace: "liatrio", Repo: "anthropics/skills",
			Subpath: "skills/alpha", Commit: oldCommit,
			InternalRef: "ghcr.io/liatrio/skills/alpha",
		}}}
		if err := catalog.WriteVendoredAtomic(path, v); err != nil {
			t.Fatalf("seed: %v", err)
		}
		return path
	}

	mkOpts := func(path string) addOpts {
		return addOpts{
			URL:          "https://github.com/anthropics/skills/tree/v1.0.0",
			Namespace:    "ghcr.io/liatrio/skills",
			VendoredPath: path,
		}
	}
	res := fakeResolver{commit: newCommit}
	fet := fakeFetcher{extraSkills: []string{"skills/alpha", "skills/beta"}}

	t.Run("declining alpha keeps it but still adds beta", func(t *testing.T) {
		path := seed(t)
		out := &bytes.Buffer{}
		if err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader("n\n"), mkOpts(path), configAccessor{}, res, fet); err != nil {
			t.Fatalf("runCatalogAddWithDeps: %v", err)
		}
		v := loadVendoredFromDisk(t, path)
		byName := map[string]catalog.VendoredEntry{}
		for _, e := range v.Skills {
			byName[e.Name] = e
		}
		if byName["alpha"].Commit != oldCommit {
			t.Errorf("alpha overwritten despite 'n': %q", byName["alpha"].Commit)
		}
		if byName["beta"].Commit != newCommit {
			t.Errorf("beta (new, no prompt) not added: %+v", byName["beta"])
		}
	})

	t.Run("confirming alpha overwrites it", func(t *testing.T) {
		path := seed(t)
		out := &bytes.Buffer{}
		if err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader("y\n"), mkOpts(path), configAccessor{}, res, fet); err != nil {
			t.Fatalf("runCatalogAddWithDeps: %v", err)
		}
		v := loadVendoredFromDisk(t, path)
		for _, e := range v.Skills {
			if e.Commit != newCommit {
				t.Errorf("entry %q = %q, want %q", e.Name, e.Commit, newCommit)
			}
		}
	})
}

func TestCatalogAdd_MultiPlainOverwrite(t *testing.T) {
	const oldCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const newCommit = "bc6708cbbc37adb919157f04d31e601e68f4b9c2"

	seed := func(t *testing.T) string {
		t.Helper()
		path := tempVendoredPath(t)
		v := catalog.Vendored{SchemaVersion: 1, Skills: []catalog.VendoredEntry{{
			Name: "alpha", Namespace: "liatrio", Repo: "anthropics/skills",
			Subpath: "skills/alpha", Commit: oldCommit,
			InternalRef: "ghcr.io/liatrio/skills/alpha",
		}}}
		if err := catalog.WriteVendoredAtomic(path, v); err != nil {
			t.Fatalf("seed: %v", err)
		}
		return path
	}
	res := fakeResolver{commit: newCommit}
	fet := fakeFetcher{extraSkills: []string{"skills/alpha", "skills/beta"}}

	t.Run("--plain without -y errors and lists the conflict", func(t *testing.T) {
		path := seed(t)
		out := &bytes.Buffer{}
		o := addOpts{URL: "https://github.com/anthropics/skills/tree/v1.0.0", Namespace: "ghcr.io/liatrio/skills", VendoredPath: path, Plain: true}
		err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader(""), o, configAccessor{}, res, fet)
		if err == nil {
			t.Fatal("--plain overwrite without -y should error")
		}
		if !strings.Contains(err.Error(), "-y") || !strings.Contains(err.Error(), "liatrio/alpha") {
			t.Errorf("error %q should name the conflict and -y", err.Error())
		}
		if got := loadVendoredFromDisk(t, path).Skills[0].Commit; got != oldCommit {
			t.Errorf("alpha overwritten under --plain without -y: %q", got)
		}
	})

	t.Run("--plain with -y overwrites all", func(t *testing.T) {
		path := seed(t)
		out := &bytes.Buffer{}
		o := addOpts{URL: "https://github.com/anthropics/skills/tree/v1.0.0", Namespace: "ghcr.io/liatrio/skills", VendoredPath: path, Plain: true, Yes: true}
		if err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader(""), o, configAccessor{}, res, fet); err != nil {
			t.Fatalf("--plain -y errored: %v", err)
		}
		for _, e := range loadVendoredFromDisk(t, path).Skills {
			if e.Commit != newCommit {
				t.Errorf("entry %q not overwritten under --plain -y: %q", e.Name, e.Commit)
			}
		}
	})
}

func TestCatalogAdd_MultiDryRun(t *testing.T) {
	out := &bytes.Buffer{}
	vendoredPath := tempVendoredPath(t)
	opts := addOpts{
		URL:          "https://github.com/anthropics/skills/tree/v1.0.0",
		Namespace:    "ghcr.io/liatrio/skills",
		VendoredPath: vendoredPath,
		DryRun:       true,
	}
	res := fakeResolver{commit: "bc6708cbbc37adb919157f04d31e601e68f4b9c2"}
	fet := fakeFetcher{extraSkills: []string{"skills/alpha", "skills/beta"}}

	if err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader(""), opts, configAccessor{}, res, fet); err != nil {
		t.Fatalf("multi dry run: %v", err)
	}
	if _, err := os.Stat(vendoredPath); !os.IsNotExist(err) {
		t.Errorf("vendored.json should not exist after dry run")
	}
	got := out.String()
	if !strings.Contains(got, "would write 2 entries") || !strings.Contains(got, "alpha") || !strings.Contains(got, "beta") {
		t.Errorf("dry-run output missing entries; got:\n%s", got)
	}
}

// partialBadFetcher writes one valid skill and one with invalid frontmatter,
// so a parse failure on the second must abort the whole add.
type partialBadFetcher struct{}

func (partialBadFetcher) Checkout(_ context.Context, _ scm.SourceRef, dst string) error {
	good := filepath.Join(dst, "skills", "good")
	if err := os.MkdirAll(good, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(good, "SKILL.md"), []byte("---\nname: good\nversion: 1.0.0\nlicense: Apache-2.0\n---\nok\n"), 0o644); err != nil {
		return err
	}
	bad := filepath.Join(dst, "skills", "bad")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(bad, "SKILL.md"), []byte("no frontmatter\n"), 0o644)
}

func (partialBadFetcher) Discover(dst, relRoot string) ([]string, error) {
	return scm.DiscoverSkills(dst, relRoot)
}

func TestCatalogAdd_ParseFailureAbortsWholeAdd(t *testing.T) {
	out := &bytes.Buffer{}
	vendoredPath := tempVendoredPath(t)
	opts := addOpts{
		URL:          "https://github.com/anthropics/skills/tree/v1.0.0",
		Namespace:    "ghcr.io/liatrio/skills",
		VendoredPath: vendoredPath,
	}
	res := fakeResolver{commit: "bc6708cbbc37adb919157f04d31e601e68f4b9c2"}
	err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader(""), opts, configAccessor{}, res, partialBadFetcher{})
	if err == nil {
		t.Fatal("expected parse failure to abort the add")
	}
	if !strings.Contains(err.Error(), "reading upstream SKILL.md") {
		t.Errorf("error %q lacks parse context", err.Error())
	}
	if _, statErr := os.Stat(vendoredPath); !os.IsNotExist(statErr) {
		t.Errorf("vendored.json must not be written when any skill fails to parse")
	}
}

func TestParseAddOpts_URLPlusFlagsRejects(t *testing.T) {
	cmd := newCatalogAddCmd()
	if err := cmd.Flags().Set("repo", "anthropics/skills"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	_, err := parseAddOpts(cmd, []string{"https://github.com/anthropics/skills/tree/v1.0.0/x"})
	if err == nil {
		t.Fatal("parseAddOpts accepted URL + flags both set")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error %q lacks 'ambiguous' context", err.Error())
	}
}

func TestParseAddOpts_MissingInputsRejects(t *testing.T) {
	cmd := newCatalogAddCmd()
	_, err := parseAddOpts(cmd, nil)
	if err == nil {
		t.Fatal("parseAddOpts accepted empty input")
	}
	if !strings.Contains(err.Error(), "missing input") {
		t.Errorf("error %q lacks 'missing input' context", err.Error())
	}
}

func TestParseAddOpts_DefaultsAndYesFlag(t *testing.T) {
	cmd := newCatalogAddCmd()
	if err := cmd.Flags().Set("repo", "anthropics/skills"); err != nil {
		t.Fatalf("Set repo: %v", err)
	}
	if err := cmd.Flags().Set("subpath", "skills/create-skill"); err != nil {
		t.Fatalf("Set subpath: %v", err)
	}
	if err := cmd.Flags().Set("version", "v1.0.0"); err != nil {
		t.Fatalf("Set version: %v", err)
	}
	if err := cmd.Flags().Set("yes", "true"); err != nil {
		t.Fatalf("Set yes: %v", err)
	}
	o, err := parseAddOpts(cmd, nil)
	if err != nil {
		t.Fatalf("parseAddOpts: %v", err)
	}
	if o.VendoredPath != "vendored.json" {
		t.Errorf("default VendoredPath = %q, want vendored.json", o.VendoredPath)
	}
	if !o.Yes {
		t.Errorf("Yes = false, want true (from --yes)")
	}
}

func TestRunCatalogAddWithDeps_RejectsMissingNamespace(t *testing.T) {
	out := &bytes.Buffer{}
	t.Setenv("SKILLS_OCI_DEFAULT_NAMESPACE", "") // ensure env var not set
	opts := addOpts{
		URL:          "https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill",
		VendoredPath: tempVendoredPath(t),
	}
	cfg := configAccessor{} // no DefaultNamespace
	err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader(""), opts, cfg, fakeResolver{commit: "bc6708cbbc37adb919157f04d31e601e68f4b9c2"}, fakeFetcher{writeSkillMD: true})
	if err == nil {
		t.Fatal("runCatalogAddWithDeps accepted missing namespace")
	}
	if !strings.Contains(err.Error(), "namespace") {
		t.Errorf("error %q lacks 'namespace' context", err.Error())
	}
}

func TestRunCatalogAddWithDeps_FetchErrorsOnMissingSKILLMD(t *testing.T) {
	out := &bytes.Buffer{}
	vendoredPath := tempVendoredPath(t)

	opts := addOpts{
		URL:          "https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill",
		Namespace:    "ghcr.io/liatrio/skills",
		VendoredPath: vendoredPath,
	}
	res := fakeResolver{commit: "bc6708cbbc37adb919157f04d31e601e68f4b9c2"}
	fet := fakeFetcher{writeSkillMD: false} // fetcher returns an error

	err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader(""), opts, configAccessor{}, res, fet)
	if err == nil {
		t.Fatal("runCatalogAddWithDeps accepted upstream without SKILL.md")
	}
	if _, statErr := os.Stat(vendoredPath); !os.IsNotExist(statErr) {
		t.Errorf("vendored.json should not exist after failed add, got %v", statErr)
	}
}

func TestRunCatalogAddWithDeps_TagNotFound(t *testing.T) {
	out := &bytes.Buffer{}
	vendoredPath := tempVendoredPath(t)

	opts := addOpts{
		URL:          "https://github.com/anthropics/skills/tree/v9.9.9/skills/create-skill",
		Namespace:    "ghcr.io/liatrio/skills",
		VendoredPath: vendoredPath,
	}
	res := fakeResolver{err: errors.New("tag \"v9.9.9\" not found on anthropics/skills")}
	err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader(""), opts, configAccessor{}, res, fakeFetcher{writeSkillMD: true})
	if err == nil {
		t.Fatal("runCatalogAddWithDeps accepted tag-not-found")
	}
	if _, statErr := os.Stat(vendoredPath); !os.IsNotExist(statErr) {
		t.Errorf("vendored.json should not exist after failed add")
	}
}

func TestRunCatalogAddWithDeps_OutputMatchesSpecFormat(t *testing.T) {
	// Validates the spec's committed --plain format line-by-line.
	out := &bytes.Buffer{}
	vendoredPath := tempVendoredPath(t)

	opts := addOpts{
		URL:          "https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill",
		Namespace:    "ghcr.io/liatrio/skills",
		VendoredPath: vendoredPath,
	}
	res := fakeResolver{commit: "bc6708cbbc37adb919157f04d31e601e68f4b9c2"}
	fet := fakeFetcher{writeSkillMD: true} // default body has name+version+license

	if err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader(""), opts, configAccessor{}, res, fet); err != nil {
		t.Fatalf("happy path: %v", err)
	}

	want := []string{
		"resolving anthropics/skills@v1.0.0",
		"→ commit bc6708cbbc37adb919157f04d31e601e68f4b9c2",
		"fetching subpath skills/create-skill",
		"verifying SKILL.md",
		"upstream name: fake-skill",
		"upstream version: 1.0.0",
		"upstream license: Apache-2.0",
		"catalog add: wrote entry \"create-skill\"",
	}
	got := out.String()
	for _, line := range want {
		if !strings.Contains(got, line) {
			t.Errorf("output missing %q\n--- got ---\n%s", line, got)
		}
	}
}

func TestResolveInternalRef_PrecedenceChain(t *testing.T) {
	// --internal-ref wins over everything.
	got, err := resolveInternalRef(addOpts{InternalRef: "explicit:tag-stripped"}, configAccessor{defaultNamespace: "from-config"}, "name")
	if err != nil || got != "explicit:tag-stripped" {
		t.Errorf("--internal-ref didn't win: got=%q err=%v", got, err)
	}

	// --namespace beats config.
	got, err = resolveInternalRef(addOpts{Namespace: "from-flag"}, configAccessor{defaultNamespace: "from-config"}, "name")
	if err != nil || got != "from-flag/name" {
		t.Errorf("--namespace didn't win: got=%q err=%v", got, err)
	}

	// Config beats env.
	t.Setenv("SKILLS_OCI_DEFAULT_NAMESPACE", "from-env")
	got, err = resolveInternalRef(addOpts{}, configAccessor{defaultNamespace: "from-config"}, "name")
	if err != nil || got != "from-config/name" {
		t.Errorf("config didn't beat env: got=%q err=%v", got, err)
	}

	// Env when no config.
	got, err = resolveInternalRef(addOpts{}, configAccessor{}, "name")
	if err != nil || got != "from-env/name" {
		t.Errorf("env didn't fall through: got=%q err=%v", got, err)
	}

	// Nothing → error.
	t.Setenv("SKILLS_OCI_DEFAULT_NAMESPACE", "")
	if _, err := resolveInternalRef(addOpts{}, configAccessor{}, "name"); err == nil {
		t.Error("no source produced no error")
	}
}

func TestResolveInternalRef_StripsTrailingSlashOnNamespace(t *testing.T) {
	got, _ := resolveInternalRef(addOpts{Namespace: "ghcr.io/liatrio/skills/"}, configAccessor{}, "create-skill")
	want := "ghcr.io/liatrio/skills/create-skill"
	if got != want {
		t.Errorf("got %q, want %q (trailing slash should be stripped)", got, want)
	}
}

func TestResolveUpstreamInputs_FlagFormValidation(t *testing.T) {
	tests := []struct {
		name string
		o    addOpts
		want string // expected substring of error
	}{
		{"missing subpath", addOpts{Repo: "anthropics/skills", Version: "v1.0.0"}, "subpath"},
		{"missing version", addOpts{Repo: "anthropics/skills", Subpath: "skills/create-skill"}, "version"},
		{"malformed repo (no slash)", addOpts{Repo: "anthropics", Subpath: "skills/create-skill", Version: "v1.0.0"}, "repo"},
		{"empty owner in repo", addOpts{Repo: "/skills", Subpath: "skills/create-skill", Version: "v1.0.0"}, "repo"},
		{"empty repo segment", addOpts{Repo: "anthropics/", Subpath: "skills/create-skill", Version: "v1.0.0"}, "repo"},
		{"ssrf: url smuggled as repo", addOpts{Repo: "http://169.254.169.254/latest/meta-data", Subpath: "skills/create-skill", Version: "v1.0.0"}, "repo"},
		{"ssrf: scheme-only owner segment", addOpts{Repo: "http:/169.254.169.254", Subpath: "skills/create-skill", Version: "v1.0.0"}, "repo"},
		{"owner with @ host smuggling", addOpts{Repo: "user@evil.com/repo", Subpath: "skills/create-skill", Version: "v1.0.0"}, "repo"},
		{"repo segment with embedded slash", addOpts{Repo: "owner/repo/extra", Subpath: "skills/create-skill", Version: "v1.0.0"}, "repo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, _, err := resolveUpstreamInputs(tt.o)
			if err == nil {
				t.Fatalf("resolveUpstreamInputs accepted %+v", tt.o)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q lacks %q context", err.Error(), tt.want)
			}
		})
	}
}

func TestResolveUpstreamInputs_TrimsSubpathSlashes(t *testing.T) {
	o := addOpts{Repo: "anthropics/skills", Subpath: "/skills/create-skill/", Version: "v1.0.0"}
	_, _, subpath, _, err := resolveUpstreamInputs(o)
	if err != nil {
		t.Fatalf("resolveUpstreamInputs: %v", err)
	}
	if subpath != "skills/create-skill" {
		t.Errorf("subpath = %q, want %q (leading/trailing slashes trimmed)", subpath, "skills/create-skill")
	}
}

func TestRunCatalogAddWithDeps_MalformedURLRejected(t *testing.T) {
	out := &bytes.Buffer{}
	opts := addOpts{
		URL:          "https://gitlab.com/foo/bar/tree/v1.0.0/x", // non-github host
		Namespace:    "ghcr.io/liatrio/skills",
		VendoredPath: tempVendoredPath(t),
	}
	err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader(""), opts, configAccessor{}, fakeResolver{commit: "bc6708cbbc37adb919157f04d31e601e68f4b9c2"}, fakeFetcher{writeSkillMD: true})
	if err == nil {
		t.Fatal("runCatalogAddWithDeps accepted non-github URL")
	}
	if !strings.Contains(err.Error(), "github.com") {
		t.Errorf("error %q lacks 'github.com' context", err.Error())
	}
}

func TestRunCatalogAddWithDeps_FetchFailure(t *testing.T) {
	out := &bytes.Buffer{}
	opts := addOpts{
		URL:          "https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill",
		Namespace:    "ghcr.io/liatrio/skills",
		VendoredPath: tempVendoredPath(t),
	}
	fet := fakeFetcher{err: errors.New("simulated network failure")}
	err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader(""), opts, configAccessor{}, fakeResolver{commit: "bc6708cbbc37adb919157f04d31e601e68f4b9c2"}, fet)
	if err == nil {
		t.Fatal("runCatalogAddWithDeps swallowed fetch failure")
	}
	if !strings.Contains(err.Error(), "fetching subpath") {
		t.Errorf("fetch error %q lacks 'fetching subpath' context", err.Error())
	}
}

// countingResolver records whether ResolveRef was ever invoked, so tests
// can assert that input validation rejects bad --repo values *before* any
// network call (the SSRF guard).
type countingResolver struct {
	commit string
	calls  int
}

func (r *countingResolver) ResolveRef(_ context.Context, _, _ string) (string, bool, error) {
	r.calls++
	return r.commit, true, nil
}

func (r *countingResolver) ResolveHEAD(_ context.Context, _ string) (string, error) {
	r.calls++
	return r.commit, nil
}

func TestRunCatalogAddWithDeps_RejectsSSRFRepoBeforeResolve(t *testing.T) {
	bad := []string{
		"http://169.254.169.254/latest/meta-data",
		"http:/169.254.169.254",
		"user@evil.com/repo",
		"owner/repo/extra",
	}
	for _, repo := range bad {
		t.Run(repo, func(t *testing.T) {
			out := &bytes.Buffer{}
			res := &countingResolver{commit: "bc6708cbbc37adb919157f04d31e601e68f4b9c2"}
			opts := addOpts{
				Repo:         repo,
				Subpath:      "skills/create-skill",
				Version:      "v1.0.0",
				Namespace:    "ghcr.io/liatrio/skills",
				VendoredPath: tempVendoredPath(t),
			}
			err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader(""), opts, configAccessor{}, res, fakeFetcher{writeSkillMD: true})
			if err == nil {
				t.Fatalf("runCatalogAddWithDeps accepted SSRF-prone --repo %q", repo)
			}
			if res.calls != 0 {
				t.Errorf("resolver was called %d times for %q; want 0 (rejected before network)", res.calls, repo)
			}
		})
	}
}

func TestParseAddOpts_ParsesTimeout(t *testing.T) {
	cmd := newCatalogAddCmd()
	mustSet(t, cmd, "repo", "anthropics/skills")
	mustSet(t, cmd, "subpath", "skills/create-skill")
	mustSet(t, cmd, "version", "v1.0.0")
	mustSet(t, cmd, "timeout", "90s")
	o, err := parseAddOpts(cmd, nil)
	if err != nil {
		t.Fatalf("parseAddOpts: %v", err)
	}
	if o.Timeout != 90*time.Second {
		t.Errorf("Timeout = %v, want 90s", o.Timeout)
	}
}

func TestParseAddOpts_TimeoutDefault(t *testing.T) {
	cmd := newCatalogAddCmd()
	mustSet(t, cmd, "repo", "anthropics/skills")
	mustSet(t, cmd, "subpath", "skills/create-skill")
	mustSet(t, cmd, "version", "v1.0.0")
	o, err := parseAddOpts(cmd, nil)
	if err != nil {
		t.Fatalf("parseAddOpts: %v", err)
	}
	if o.Timeout != 60*time.Second {
		t.Errorf("default Timeout = %v, want 60s", o.Timeout)
	}
}

func mustSet(t *testing.T, cmd *cobra.Command, name, val string) {
	t.Helper()
	if err := cmd.Flags().Set(name, val); err != nil {
		t.Fatalf("Set %s: %v", name, err)
	}
}

// deadlineResolver reports whether the context it received carried a deadline.
type deadlineResolver struct {
	commit      string
	hadDeadline bool
}

func (r *deadlineResolver) ResolveRef(ctx context.Context, _, _ string) (string, bool, error) {
	_, r.hadDeadline = ctx.Deadline()
	return r.commit, true, nil
}

func (r *deadlineResolver) ResolveHEAD(ctx context.Context, _ string) (string, error) {
	_, r.hadDeadline = ctx.Deadline()
	return r.commit, nil
}

func TestRunCatalogAddWithDeps_TimeoutAppliesDeadline(t *testing.T) {
	out := &bytes.Buffer{}
	res := &deadlineResolver{commit: "bc6708cbbc37adb919157f04d31e601e68f4b9c2"}
	opts := addOpts{
		URL:          "https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill",
		Namespace:    "ghcr.io/liatrio/skills",
		VendoredPath: tempVendoredPath(t),
		Timeout:      30 * time.Second,
	}
	if err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader(""), opts, configAccessor{}, res, fakeFetcher{writeSkillMD: true}); err != nil {
		t.Fatalf("runCatalogAddWithDeps: %v", err)
	}
	if !res.hadDeadline {
		t.Error("resolver context carried no deadline despite Timeout=30s")
	}
}

func TestRunCatalogAddWithDeps_ZeroTimeoutNoDeadline(t *testing.T) {
	out := &bytes.Buffer{}
	res := &deadlineResolver{commit: "bc6708cbbc37adb919157f04d31e601e68f4b9c2"}
	opts := addOpts{
		URL:          "https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill",
		Namespace:    "ghcr.io/liatrio/skills",
		VendoredPath: tempVendoredPath(t),
		Timeout:      0,
	}
	if err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader(""), opts, configAccessor{}, res, fakeFetcher{writeSkillMD: true}); err != nil {
		t.Fatalf("runCatalogAddWithDeps: %v", err)
	}
	if res.hadDeadline {
		t.Error("resolver context carried a deadline despite Timeout=0")
	}
}

// noSkillMDFetcher writes a SKILL.md with invalid frontmatter so discovery
// finds the directory but skill.Parse fails — exercising the parse-failure
// path, distinct from a checkout-level error or an empty directory.
type noSkillMDFetcher struct{}

func (noSkillMDFetcher) Checkout(_ context.Context, ref scm.SourceRef, dst string) error {
	dir := filepath.Join(dst, filepath.FromSlash(ref.Subpath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Present but unparseable: no YAML frontmatter delimiters.
	return os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("no frontmatter here\n"), 0o644)
}

func (noSkillMDFetcher) Discover(dst, relRoot string) ([]string, error) {
	return scm.DiscoverSkills(dst, relRoot)
}

func TestRunCatalogAddWithDeps_FetchSucceedsButNoSKILLMD(t *testing.T) {
	out := &bytes.Buffer{}
	vendoredPath := tempVendoredPath(t)
	opts := addOpts{
		URL:          "https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill",
		Namespace:    "ghcr.io/liatrio/skills",
		VendoredPath: vendoredPath,
	}
	res := fakeResolver{commit: "bc6708cbbc37adb919157f04d31e601e68f4b9c2"}
	err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader(""), opts, configAccessor{}, res, noSkillMDFetcher{})
	if err == nil {
		t.Fatal("runCatalogAddWithDeps accepted subpath without SKILL.md")
	}
	if !strings.Contains(err.Error(), "reading upstream SKILL.md") {
		t.Errorf("error %q lacks 'reading upstream SKILL.md' (skill.Parse step-5 path)", err.Error())
	}
	if _, statErr := os.Stat(vendoredPath); !os.IsNotExist(statErr) {
		t.Errorf("vendored.json should not exist after failed parse, got %v", statErr)
	}
}

func TestLoadVendoredFile_MissingBootstraps(t *testing.T) {
	v, err := loadVendoredFile(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("loadVendoredFile: %v", err)
	}
	if v.SchemaVersion != 1 || len(v.Skills) != 0 {
		t.Errorf("bootstrapped value = %+v, want empty SchemaVersion:1", v)
	}
}

func TestLoadVendoredFile_ReadErrorWrapped(t *testing.T) {
	// A directory path makes os.ReadFile fail with a non-IsNotExist error,
	// which must be wrapped with the path rather than treated as absent.
	dir := t.TempDir()
	_, err := loadVendoredFile(dir)
	if err == nil {
		t.Fatal("loadVendoredFile accepted a directory path")
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("read error %q lacks the path %q", err.Error(), dir)
	}
}

func TestLoadVendoredFile_ParseErrorSurfaced(t *testing.T) {
	path := tempVendoredPath(t)
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := loadVendoredFile(path); err == nil {
		t.Fatal("loadVendoredFile accepted malformed JSON")
	}
}

func TestExtractV2Namespace(t *testing.T) {
	tests := []struct {
		name        string
		internalRef string
		want        string
		wantErr     bool
	}{
		{"valid four-segment ref", "ghcr.io/liatrio/skills/create-skill", "liatrio", false},
		{"valid three-segment ref", "ghcr.io/liatrio/create-skill", "liatrio", false},
		{"two-segment ref errors (no name)", "registry/namespace", "", true},
		{"missing registry host errors", "liatrio/ask-matt", "", true},
		{"single segment errors", "singleword", "", true},
		{"empty middle segment errors", "ghcr.io//create-skill", "", true},
		{"empty second segment errors", "registry/", "", true},
		{"empty string errors", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractV2Namespace(tt.internalRef)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("extractV2Namespace(%q) = %q, want error", tt.internalRef, got)
				}
				if !strings.Contains(err.Error(), "<registry>/<namespace>/skills/<name>") {
					t.Errorf("error %q lacks the format hint", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("extractV2Namespace(%q) unexpected error: %v", tt.internalRef, err)
			}
			if got != tt.want {
				t.Errorf("extractV2Namespace(%q) = %q, want %q", tt.internalRef, got, tt.want)
			}
		})
	}
}

// TestCatalogAdd_RejectsUnderQualifiedNamespace is the regression guard for the
// incident where `--namespace liatrio` (the bare org, missing the registry
// host) produced a two-segment internal_ref `liatrio/<name>`. The consumer's
// registry parser requires <registry>/<namespace>/<name>, so such a ref breaks
// the downstream catalog-sync build. The add must fail fast and write nothing.
func TestCatalogAdd_RejectsUnderQualifiedNamespace(t *testing.T) {
	out := &bytes.Buffer{}
	vendoredPath := tempVendoredPath(t)

	opts := addOpts{
		URL:          "https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill",
		Namespace:    "liatrio", // bare org, no registry host — would yield "liatrio/create-skill"
		VendoredPath: vendoredPath,
	}
	res := fakeResolver{commit: "bc6708cbbc37adb919157f04d31e601e68f4b9c2"}
	err := runCatalogAddWithDeps(context.Background(), out, strings.NewReader(""), opts, configAccessor{}, res, fakeFetcher{writeSkillMD: true})
	if err == nil {
		t.Fatal("runCatalogAddWithDeps accepted an under-qualified namespace")
	}
	if !strings.Contains(err.Error(), "internal_ref") && !strings.Contains(err.Error(), "<registry>/<namespace>") {
		t.Errorf("error %q lacks internal_ref/format context", err.Error())
	}
	if _, statErr := os.Stat(vendoredPath); !os.IsNotExist(statErr) {
		t.Errorf("a rejected add must not write vendored.json (found file at %s)", vendoredPath)
	}
}
