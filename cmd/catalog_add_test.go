package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salaboy/skills-oci/pkg/catalog"
	"github.com/salaboy/skills-oci/pkg/scm"
)

// fakeResolver returns a canned SHA for any tag. Used to avoid network.
type fakeResolver struct {
	commit string
	err    error
}

func (f fakeResolver) ResolveTag(_ context.Context, _, _ string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.commit, nil
}

// fakeFetcher writes a SKILL.md (or doesn't) into the expected subpath
// inside dst, simulating what scm.Fetch would do for a real fixture.
type fakeFetcher struct {
	writeSkillMD bool
	skillMDBody  string
	err          error
}

func (f fakeFetcher) Fetch(_ context.Context, ref scm.SourceRef, dst string) error {
	if f.err != nil {
		return f.err
	}
	subpathDir := filepath.Join(dst, filepath.FromSlash(ref.Subpath))
	if err := os.MkdirAll(subpathDir, 0o755); err != nil {
		return err
	}
	if !f.writeSkillMD {
		// Subpath exists but no SKILL.md — that's still an error per the
		// real fetcher's contract, but the fake leaves it to the real
		// scm.Fetch to enforce. For our tests we want the orchestrator
		// to handle a "fetched but no SKILL.md" state, so we wrote one
		// or did not based on the test's intent. When writeSkillMD=false
		// we still need to return an error so the orchestrator surfaces
		// it correctly.
		return fmt.Errorf("fake fetch: subpath %q does not contain SKILL.md", ref.Subpath)
	}
	body := f.skillMDBody
	if body == "" {
		body = "---\nname: fake-skill\nversion: 1.0.0\nlicense: Apache-2.0\n---\nfake body\n"
	}
	return os.WriteFile(filepath.Join(subpathDir, "SKILL.md"), []byte(body), 0o644)
}

func tempCatalogPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "catalog.json")
}

func TestRunCatalogAddWithDeps_HappyPathURL(t *testing.T) {
	out := &bytes.Buffer{}
	catalogPath := tempCatalogPath(t)

	opts := addOpts{
		URL:         "https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill",
		Namespace:   "ghcr.io/liatrio/skills",
		CatalogPath: catalogPath,
	}
	cfg := configAccessor{}
	res := fakeResolver{commit: "bc6708cbbc37adb919157f04d31e601e68f4b9c2"}
	fet := fakeFetcher{writeSkillMD: true}

	if err := runCatalogAddWithDeps(context.Background(), out, opts, cfg, res, fet); err != nil {
		t.Fatalf("runCatalogAddWithDeps: %v", err)
	}

	body, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatalf("ReadFile catalog: %v", err)
	}
	c, err := catalog.Load(body)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Skills) != 1 {
		t.Fatalf("len(Skills) = %d, want 1", len(c.Skills))
	}
	got := c.Skills[0]
	want := catalog.Entry{
		Name:        "create-skill",
		Repo:        "anthropics/skills",
		Subpath:     "skills/create-skill",
		Version:     "v1.0.0",
		Commit:      "bc6708cbbc37adb919157f04d31e601e68f4b9c2",
		InternalRef: "ghcr.io/liatrio/skills/create-skill",
	}
	if got != want {
		t.Errorf("entry = %+v, want %+v", got, want)
	}
}

func TestRunCatalogAddWithDeps_HappyPathFlags(t *testing.T) {
	out := &bytes.Buffer{}
	catalogPath := tempCatalogPath(t)

	opts := addOpts{
		Repo:        "anthropics/skills",
		Subpath:     "skills/create-skill",
		Version:     "v1.0.0",
		Namespace:   "ghcr.io/liatrio/skills",
		CatalogPath: catalogPath,
	}
	cfg := configAccessor{}
	res := fakeResolver{commit: "bc6708cbbc37adb919157f04d31e601e68f4b9c2"}
	fet := fakeFetcher{writeSkillMD: true}

	if err := runCatalogAddWithDeps(context.Background(), out, opts, cfg, res, fet); err != nil {
		t.Fatalf("flag form: %v", err)
	}
	body, _ := os.ReadFile(catalogPath)
	c, _ := catalog.Load(body)
	if len(c.Skills) != 1 || c.Skills[0].Name != "create-skill" {
		t.Errorf("flag form result wrong: %+v", c)
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

func TestRunCatalogAddWithDeps_RejectsMissingNamespace(t *testing.T) {
	out := &bytes.Buffer{}
	t.Setenv("SKILLS_OCI_DEFAULT_NAMESPACE", "") // ensure env var not set
	opts := addOpts{
		URL:         "https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill",
		CatalogPath: tempCatalogPath(t),
	}
	cfg := configAccessor{} // no DefaultNamespace
	err := runCatalogAddWithDeps(context.Background(), out, opts, cfg, fakeResolver{commit: "bc6708cbbc37adb919157f04d31e601e68f4b9c2"}, fakeFetcher{writeSkillMD: true})
	if err == nil {
		t.Fatal("runCatalogAddWithDeps accepted missing namespace")
	}
	if !strings.Contains(err.Error(), "namespace") {
		t.Errorf("error %q lacks 'namespace' context", err.Error())
	}
}

func TestRunCatalogAddWithDeps_SubpathWithoutSKILLMD(t *testing.T) {
	out := &bytes.Buffer{}
	catalogPath := tempCatalogPath(t)

	opts := addOpts{
		URL:         "https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill",
		Namespace:   "ghcr.io/liatrio/skills",
		CatalogPath: catalogPath,
	}
	cfg := configAccessor{}
	res := fakeResolver{commit: "bc6708cbbc37adb919157f04d31e601e68f4b9c2"}
	fet := fakeFetcher{writeSkillMD: false} // signals "no SKILL.md"

	err := runCatalogAddWithDeps(context.Background(), out, opts, cfg, res, fet)
	if err == nil {
		t.Fatal("runCatalogAddWithDeps accepted upstream without SKILL.md")
	}
	if _, statErr := os.Stat(catalogPath); !os.IsNotExist(statErr) {
		t.Errorf("catalog.json should not exist after failed add, got %v", statErr)
	}
}

func TestRunCatalogAddWithDeps_TagNotFound(t *testing.T) {
	out := &bytes.Buffer{}
	catalogPath := tempCatalogPath(t)

	opts := addOpts{
		URL:         "https://github.com/anthropics/skills/tree/v9.9.9/skills/create-skill",
		Namespace:   "ghcr.io/liatrio/skills",
		CatalogPath: catalogPath,
	}
	res := fakeResolver{err: errors.New("tag \"v9.9.9\" not found on anthropics/skills")}
	err := runCatalogAddWithDeps(context.Background(), out, opts, configAccessor{}, res, fakeFetcher{writeSkillMD: true})
	if err == nil {
		t.Fatal("runCatalogAddWithDeps accepted tag-not-found")
	}
	if _, statErr := os.Stat(catalogPath); !os.IsNotExist(statErr) {
		t.Errorf("catalog.json should not exist after failed add")
	}
}

func TestRunCatalogAddWithDeps_DuplicateNameRejected(t *testing.T) {
	out := &bytes.Buffer{}
	catalogPath := tempCatalogPath(t)

	// Pre-populate catalog with an existing entry of the same name.
	seed := catalog.Catalog{
		SchemaVersion: 1,
		Skills: []catalog.Entry{{
			Name:        "create-skill",
			Repo:        "anthropics/skills",
			Subpath:     "skills/create-skill",
			Version:     "v0.9.0",
			Commit:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			InternalRef: "ghcr.io/liatrio/skills/create-skill",
		}},
	}
	if err := catalog.WriteCatalogAtomic(catalogPath, seed); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}

	opts := addOpts{
		URL:         "https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill",
		Namespace:   "ghcr.io/liatrio/skills",
		CatalogPath: catalogPath,
	}
	res := fakeResolver{commit: "bc6708cbbc37adb919157f04d31e601e68f4b9c2"}
	err := runCatalogAddWithDeps(context.Background(), out, opts, configAccessor{}, res, fakeFetcher{writeSkillMD: true})
	if err == nil {
		t.Fatal("runCatalogAddWithDeps accepted duplicate name")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error %q lacks 'duplicate' context", err.Error())
	}

	// Original entry still there, untouched.
	body, _ := os.ReadFile(catalogPath)
	c, _ := catalog.Load(body)
	if c.Skills[0].Commit != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("original entry mutated: %+v", c.Skills[0])
	}
}

func TestRunCatalogAddWithDeps_DryRunDoesNotWrite(t *testing.T) {
	out := &bytes.Buffer{}
	catalogPath := tempCatalogPath(t)

	opts := addOpts{
		URL:         "https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill",
		Namespace:   "ghcr.io/liatrio/skills",
		CatalogPath: catalogPath,
		DryRun:      true,
	}
	res := fakeResolver{commit: "bc6708cbbc37adb919157f04d31e601e68f4b9c2"}
	if err := runCatalogAddWithDeps(context.Background(), out, opts, configAccessor{}, res, fakeFetcher{writeSkillMD: true}); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if _, err := os.Stat(catalogPath); !os.IsNotExist(err) {
		t.Errorf("catalog.json should not exist after dry run, got %v", err)
	}
	if !strings.Contains(out.String(), "would add entry") {
		t.Errorf("dry-run output should announce would-be entry; got:\n%s", out.String())
	}
}

func TestRunCatalogAddWithDeps_OutputMatchesSpecFormat(t *testing.T) {
	// Validates the spec's committed --plain format line-by-line.
	out := &bytes.Buffer{}
	catalogPath := tempCatalogPath(t)

	opts := addOpts{
		URL:         "https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill",
		Namespace:   "ghcr.io/liatrio/skills",
		CatalogPath: catalogPath,
	}
	res := fakeResolver{commit: "bc6708cbbc37adb919157f04d31e601e68f4b9c2"}
	fet := fakeFetcher{writeSkillMD: true} // default body has name+version+license

	if err := runCatalogAddWithDeps(context.Background(), out, opts, configAccessor{}, res, fet); err != nil {
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
		"catalog add: appended entry \"create-skill\"",
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
		{
			name: "missing subpath",
			o:    addOpts{Repo: "anthropics/skills", Version: "v1.0.0"},
			want: "subpath",
		},
		{
			name: "missing version",
			o:    addOpts{Repo: "anthropics/skills", Subpath: "skills/create-skill"},
			want: "version",
		},
		{
			name: "malformed repo (no slash)",
			o:    addOpts{Repo: "anthropics", Subpath: "skills/create-skill", Version: "v1.0.0"},
			want: "repo",
		},
		{
			name: "empty owner in repo",
			o:    addOpts{Repo: "/skills", Subpath: "skills/create-skill", Version: "v1.0.0"},
			want: "repo",
		},
		{
			name: "empty repo segment",
			o:    addOpts{Repo: "anthropics/", Subpath: "skills/create-skill", Version: "v1.0.0"},
			want: "repo",
		},
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
		URL:         "https://gitlab.com/foo/bar/tree/v1.0.0/x", // non-github host
		Namespace:   "ghcr.io/liatrio/skills",
		CatalogPath: tempCatalogPath(t),
	}
	err := runCatalogAddWithDeps(context.Background(), out, opts, configAccessor{}, fakeResolver{commit: "bc6708cbbc37adb919157f04d31e601e68f4b9c2"}, fakeFetcher{writeSkillMD: true})
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
		URL:         "https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill",
		Namespace:   "ghcr.io/liatrio/skills",
		CatalogPath: tempCatalogPath(t),
	}
	fet := fakeFetcher{err: errors.New("simulated network failure")}
	err := runCatalogAddWithDeps(context.Background(), out, opts, configAccessor{}, fakeResolver{commit: "bc6708cbbc37adb919157f04d31e601e68f4b9c2"}, fet)
	if err == nil {
		t.Fatal("runCatalogAddWithDeps swallowed fetch failure")
	}
}
