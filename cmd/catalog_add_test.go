package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/salaboy/skills-oci/pkg/catalog"
	"github.com/salaboy/skills-oci/pkg/scm"
	"github.com/salaboy/skills-oci/pkg/skill"
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
	if c.SchemaVersion != 2 {
		t.Errorf("SchemaVersion = %d, want 2", c.SchemaVersion)
	}
	if c.GeneratedAt.IsZero() || c.GeneratedAt.Location() != time.UTC {
		t.Errorf("GeneratedAt = %v, want non-zero UTC", c.GeneratedAt)
	}
	if len(c.Skills) != 1 {
		t.Fatalf("len(Skills) = %d, want 1", len(c.Skills))
	}
	got := c.Skills[0]
	// Surface fields the platform validator enforces.
	if got.Namespace != "liatrio" || got.Name != "create-skill" {
		t.Errorf("surface identifiers = %q/%q, want liatrio/create-skill", got.Namespace, got.Name)
	}
	if got.Status != catalog.StatusPublished || got.LatestVersion != "1.0.0" {
		t.Errorf("status/latest_version = %q/%q, want published/1.0.0", got.Status, got.LatestVersion)
	}
	if got.Visibility != catalog.VisibilityPublic {
		t.Errorf("visibility = %q, want public", got.Visibility)
	}
	if got.UpdatedAt.IsZero() || got.UpdatedAt.Location() != time.UTC {
		t.Errorf("UpdatedAt = %v, want non-zero UTC", got.UpdatedAt)
	}
	// Source-pin fields carried for catalog sync.
	if got.Repo != "anthropics/skills" || got.Subpath != "skills/create-skill" ||
		got.Version != "v1.0.0" || got.Commit != "bc6708cbbc37adb919157f04d31e601e68f4b9c2" ||
		got.InternalRef != "ghcr.io/liatrio/skills/create-skill" {
		t.Errorf("source-pin fields = %+v", got)
	}
}

func TestRunCatalogAddWithDeps_MigratesLegacyV1CatalogFile(t *testing.T) {
	// A v1-shaped catalog file (schemaVersion: 1, no generated_at, entries
	// lacking v2 surface fields) is silently migrated to v2 on load so
	// `catalog add` can append to legacy files written by older versions
	// of skills-oci. The rewritten file ends up v2-compliant.
	out := &bytes.Buffer{}
	catalogPath := tempCatalogPath(t)

	// Write a legacy v1 catalog to disk by hand — WriteCatalogAtomic would
	// reject it via Validate, which is the whole point of the migration.
	legacy := `{
  "schemaVersion": 1,
  "skills": [
    {
      "name": "existing-skill",
      "repo": "anthropics/skills",
      "subpath": "skills/existing-skill",
      "version": "v0.5.0",
      "commit": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "internal_ref": "ghcr.io/liatrio/skills/existing-skill"
    }
  ]
}`
	if err := os.WriteFile(catalogPath, []byte(legacy), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	opts := addOpts{
		URL:         "https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill",
		Namespace:   "ghcr.io/liatrio/skills",
		CatalogPath: catalogPath,
	}
	res := fakeResolver{commit: "bc6708cbbc37adb919157f04d31e601e68f4b9c2"}
	if err := runCatalogAddWithDeps(context.Background(), out, opts, configAccessor{}, res, fakeFetcher{writeSkillMD: true}); err != nil {
		t.Fatalf("runCatalogAddWithDeps: %v", err)
	}

	body, _ := os.ReadFile(catalogPath)
	c, err := catalog.Load(body)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.SchemaVersion != 2 {
		t.Errorf("SchemaVersion = %d, want 2", c.SchemaVersion)
	}
	if c.GeneratedAt.IsZero() {
		t.Error("GeneratedAt unset after migration")
	}
	if len(c.Skills) != 2 {
		t.Fatalf("len(Skills) = %d, want 2 (legacy + new)", len(c.Skills))
	}
	// Legacy row got v2 surface fields filled in.
	legacyRow := c.Skills[0]
	if legacyRow.Namespace != "liatrio" {
		t.Errorf("legacy namespace = %q, want liatrio", legacyRow.Namespace)
	}
	if legacyRow.Status != catalog.StatusPublished || legacyRow.LatestVersion != "0.5.0" {
		t.Errorf("legacy status/latest_version = %q/%q, want published/0.5.0", legacyRow.Status, legacyRow.LatestVersion)
	}
	if legacyRow.Visibility != catalog.VisibilityPublic {
		t.Errorf("legacy visibility = %q, want public", legacyRow.Visibility)
	}
	if legacyRow.UpdatedAt.IsZero() {
		t.Error("legacy UpdatedAt unset after migration")
	}
	// Legacy source-pin fields preserved.
	if legacyRow.Repo != "anthropics/skills" || legacyRow.Commit != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("legacy source-pin fields lost: %+v", legacyRow)
	}
}

func TestRunCatalogAddWithDeps_WritesDetailFileWhenDetailDirSet(t *testing.T) {
	// With --detail-dir set, the detail file lands at
	// <detail-dir>/<namespace>/<name>.json. Without it, no detail file
	// is written (covered by a separate test).
	out := &bytes.Buffer{}
	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "catalog.json")
	detailDir := filepath.Join(dir, "skills")
	const commit = "bc6708cbbc37adb919157f04d31e601e68f4b9c2"

	opts := addOpts{
		URL:         "https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill",
		Namespace:   "ghcr.io/liatrio/skills",
		CatalogPath: catalogPath,
		DetailDir:   detailDir,
	}
	res := fakeResolver{commit: commit}
	if err := runCatalogAddWithDeps(context.Background(), out, opts, configAccessor{}, res, fakeFetcher{writeSkillMD: true}); err != nil {
		t.Fatalf("runCatalogAddWithDeps: %v", err)
	}

	detailPath := filepath.Join(detailDir, "liatrio", "create-skill.json")
	body, err := os.ReadFile(detailPath)
	if err != nil {
		t.Fatalf("detail file not written at %s: %v", detailPath, err)
	}
	var detail catalog.SkillDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("Unmarshal detail: %v", err)
	}
	if err := catalog.ValidateSkillDetail(detail); err != nil {
		t.Errorf("written detail fails own validator: %v", err)
	}
	if detail.Namespace != "liatrio" || detail.Name != "create-skill" {
		t.Errorf("namespace/name = %q/%q", detail.Namespace, detail.Name)
	}
	if detail.LatestVersion != "1.0.0" {
		t.Errorf("latest_version = %q, want 1.0.0", detail.LatestVersion)
	}
	if detail.OCIRef != "ghcr.io/liatrio/skills/create-skill" {
		t.Errorf("oci_ref = %q", detail.OCIRef)
	}
	// repo_url should point at the original upstream ref (the tag the
	// user vendored at) — not the resolved commit. Keeps the link
	// human-readable for tagged inputs.
	wantRepoURL := "https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill"
	if detail.RepoURL != wantRepoURL {
		t.Errorf("repo_url = %q, want %q", detail.RepoURL, wantRepoURL)
	}
	_ = commit // still referenced by other assertions above
	if len(detail.Versions) != 1 {
		t.Fatalf("versions = %d, want 1", len(detail.Versions))
	}
	if !strings.Contains(detail.Versions[0].Body, "fake body") {
		t.Errorf("versions[0].body lacks SKILL.md content: %q", detail.Versions[0].Body)
	}
}

// jsonForDetailTest is a thin helper that unmarshals a JSON file into a
// SkillDetail, panicking on failure — only used in the test above to
// keep the call site brief. Pulled out here so encoding/json doesn't
// leak into the production catalog_add.go imports.
var _ = json.Unmarshal

func TestRunCatalogAddWithDeps_NoDetailWriteWhenDetailDirUnset(t *testing.T) {
	// The default invocation must not touch any path besides --catalog.
	// In particular, no `skills/` subdirectory should appear next to the
	// catalog file, since that would be surprising file pollution for
	// users vendoring into their own workflows.
	out := &bytes.Buffer{}
	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "catalog.json")

	opts := addOpts{
		URL:         "https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill",
		Namespace:   "ghcr.io/liatrio/skills",
		CatalogPath: catalogPath,
		// DetailDir intentionally left empty.
	}
	res := fakeResolver{commit: "bc6708cbbc37adb919157f04d31e601e68f4b9c2"}
	if err := runCatalogAddWithDeps(context.Background(), out, opts, configAccessor{}, res, fakeFetcher{writeSkillMD: true}); err != nil {
		t.Fatalf("runCatalogAddWithDeps: %v", err)
	}

	// catalog.json should exist.
	if _, err := os.Stat(catalogPath); err != nil {
		t.Errorf("catalog.json missing: %v", err)
	}
	// No skills/ directory should have been created anywhere under dir.
	skillsDir := filepath.Join(dir, "skills")
	if _, err := os.Stat(skillsDir); !os.IsNotExist(err) {
		t.Errorf("skills/ directory was created without --detail-dir; want it to not exist (stat err=%v)", err)
	}
	// Stdout should not announce a detail write.
	if strings.Contains(out.String(), "wrote detail") {
		t.Errorf("output announced a detail write without --detail-dir:\n%s", out.String())
	}
}

func TestRunCatalogAddWithDeps_SHAInputProducesSyntheticPublishedRow(t *testing.T) {
	// A SHA-pinned add still produces a `published` row because the
	// version-derivation chain falls back to a synthetic SemVer with the
	// commit SHA as build metadata. That keeps the detail file's
	// "latest_version must be SemVer" contract intact.
	out := &bytes.Buffer{}
	catalogPath := tempCatalogPath(t)
	const commit = "690f15cac7f7b4c055c5ab109c79ed9259934081"

	opts := addOpts{
		URL:         "https://github.com/anthropics/skills/tree/" + commit + "/skills/algorithmic-art",
		Namespace:   "ghcr.io/liatrio/skills",
		CatalogPath: catalogPath,
	}
	res := fakeResolver{commit: commit}
	// The default fake SKILL.md frontmatter sets version: 1.0.0 (top-level).
	// To exercise the synthetic fallback we need a fixture with no version.
	fet := fakeFetcher{writeSkillMD: true, skillMDBody: "---\nname: algorithmic-art\nlicense: MIT\n---\nbody\n"}
	if err := runCatalogAddWithDeps(context.Background(), out, opts, configAccessor{}, res, fet); err != nil {
		t.Fatalf("runCatalogAddWithDeps: %v", err)
	}
	body, _ := os.ReadFile(catalogPath)
	c, err := catalog.Load(body)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := c.Skills[0]
	if got.Status != catalog.StatusPublished {
		t.Errorf("status = %q, want published", got.Status)
	}
	wantVersion := "0.0.0+sha." + commit[:8]
	if got.LatestVersion != wantVersion {
		t.Errorf("latest_version = %q, want %q (synthetic SHA fallback)", got.LatestVersion, wantVersion)
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
	seedTime := time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC)
	seed := catalog.Catalog{
		SchemaVersion: 2,
		GeneratedAt:   seedTime,
		Skills: []catalog.Entry{{
			Namespace:     "liatrio",
			Name:          "create-skill",
			LatestVersion: "0.9.0",
			UpdatedAt:     seedTime,
			Status:        catalog.StatusPublished,
			Visibility:    catalog.VisibilityPublic,
			Repo:          "anthropics/skills",
			Subpath:       "skills/create-skill",
			Version:       "v0.9.0",
			Commit:        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			InternalRef:   "ghcr.io/liatrio/skills/create-skill",
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

func TestDeriveLatestVersion_PrecedenceChain(t *testing.T) {
	const commit = "690f15cac7f7b4c055c5ab109c79ed9259934081"

	tests := []struct {
		name       string
		versionRef string
		cfg        skill.SkillConfig
		want       string
	}{
		{
			name:       "step 1: inbound SemVer with leading v",
			versionRef: "v1.2.3",
			cfg:        skill.SkillConfig{Metadata: map[string]any{"version": "9.9.9"}, Version: "8.8.8"},
			want:       "1.2.3",
		},
		{
			name:       "step 1: inbound SemVer without leading v",
			versionRef: "1.2.3",
			cfg:        skill.SkillConfig{Metadata: map[string]any{"version": "9.9.9"}},
			want:       "1.2.3",
		},
		{
			name:       "step 2: SKILL.md metadata.version",
			versionRef: commit, // not a SemVer
			cfg:        skill.SkillConfig{Metadata: map[string]any{"version": "1.1.0"}, Version: "8.8.8"},
			want:       "1.1.0",
		},
		{
			name:       "step 3: SKILL.md top-level version (no metadata)",
			versionRef: commit,
			cfg:        skill.SkillConfig{Version: "v0.5.0"},
			want:       "0.5.0",
		},
		{
			name:       "step 4: synthetic SHA fallback",
			versionRef: commit,
			cfg:        skill.SkillConfig{},
			want:       "0.0.0+sha." + commit[:8],
		},
		{
			name:       "metadata.version is non-semver, falls through to top-level",
			versionRef: commit,
			cfg:        skill.SkillConfig{Metadata: map[string]any{"version": "not-semver"}, Version: "1.0.0"},
			want:       "1.0.0",
		},
		{
			name:       "metadata.version non-string ignored",
			versionRef: commit,
			cfg:        skill.SkillConfig{Metadata: map[string]any{"version": 1.0}, Version: "1.0.0"},
			want:       "1.0.0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveLatestVersion(tt.versionRef, tt.cfg, commit)
			if got != tt.want {
				t.Errorf("deriveLatestVersion = %q, want %q", got, tt.want)
			}
		})
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
