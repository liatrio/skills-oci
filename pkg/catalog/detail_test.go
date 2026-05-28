package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validSkillDetail() SkillDetail {
	return SkillDetail{
		SchemaVersion: 2,
		Namespace:     "liatrio",
		Name:          "algorithmic-art",
		LatestVersion: "0.0.0+sha.690f15c",
		Visibility:    VisibilityPublic,
		Status:        StatusPublished,
		Description:   "Creating algorithmic art using p5.js.",
		RepoURL:       "https://github.com/anthropics/skills/tree/690f15cac7f7b4c055c5ab109c79ed9259934081/skills/algorithmic-art",
		OCIRef:        "ghcr.io/liatrio/skills/algorithmic-art",
		Versions: []SkillVersion{{
			Version:     "0.0.0+sha.690f15c",
			PublishedAt: time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC),
			Body:        "---\nname: algorithmic-art\n---\nbody\n",
		}},
	}
}

func TestWriteSkillDetailAtomic_RoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skills", "liatrio", "algorithmic-art.json")

	if err := WriteSkillDetailAtomic(path, validSkillDetail()); err != nil {
		t.Fatalf("WriteSkillDetailAtomic: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var got SkillDetail
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.SchemaVersion != 2 {
		t.Errorf("schema_version = %d, want 2", got.SchemaVersion)
	}
	if got.Namespace != "liatrio" || got.Name != "algorithmic-art" {
		t.Errorf("namespace/name = %q/%q", got.Namespace, got.Name)
	}
	if len(got.Versions) != 1 || got.Versions[0].Body == "" {
		t.Errorf("versions[] = %+v, want one with body", got.Versions)
	}
}

func TestWriteSkillDetailAtomic_BootstrapsSchemaVersion(t *testing.T) {
	// A caller building a detail from scratch may leave schema_version
	// unset (0); the writer bootstraps it to 2 before validating and
	// writing. The on-disk file must record schema_version 2.
	dir := t.TempDir()
	path := filepath.Join(dir, "skill.json")
	d := validSkillDetail()
	d.SchemaVersion = 0

	if err := WriteSkillDetailAtomic(path, d); err != nil {
		t.Fatalf("WriteSkillDetailAtomic: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var got SkillDetail
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.SchemaVersion != 2 {
		t.Errorf("schema_version = %d, want 2 (bootstrapped from 0)", got.SchemaVersion)
	}
}

func TestWriteSkillDetailAtomic_RejectsNonTwoSchemaVersion(t *testing.T) {
	// schema_version is bootstrapped from 0 to 2, but any other non-2
	// value is a contract violation the writer must reject rather than
	// silently persist.
	dir := t.TempDir()
	path := filepath.Join(dir, "skill.json")
	d := validSkillDetail()
	d.SchemaVersion = 99

	err := WriteSkillDetailAtomic(path, d)
	if err == nil {
		t.Fatal("WriteSkillDetailAtomic accepted schema_version=99")
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Errorf("error %q lacks 'schema_version' context", err.Error())
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("file should not exist after rejected write")
	}
}

func TestWriteSkillDetailAtomic_CreatesParentDirs(t *testing.T) {
	// The platform layout is data/skills/<namespace>/<name>.json — the
	// namespace subdirectory likely won't exist yet on first add. Writer
	// must MkdirAll the parents, not error.
	dir := t.TempDir()
	path := filepath.Join(dir, "deeply", "nested", "skills", "liatrio", "algorithmic-art.json")
	if err := WriteSkillDetailAtomic(path, validSkillDetail()); err != nil {
		t.Fatalf("WriteSkillDetailAtomic should create parents: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

func TestWriteSkillDetailAtomic_RejectsInvalidDetail(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*SkillDetail)
		want   string
	}{
		{"bad namespace", func(d *SkillDetail) { d.Namespace = "Liatrio" }, "namespace"},
		{"bad name", func(d *SkillDetail) { d.Name = "Foo_Bar" }, "name"},
		{"non-semver latest_version", func(d *SkillDetail) { d.LatestVersion = "not-semver"; d.Versions[0].Version = "not-semver" }, "latest_version"},
		{"empty versions", func(d *SkillDetail) { d.Versions = nil }, "versions"},
		{"latest_not_in_versions", func(d *SkillDetail) { d.Versions[0].Version = "9.9.9" }, "latest_version"},
		{"empty oci_ref", func(d *SkillDetail) { d.OCIRef = "" }, "oci_ref"},
		{"bad visibility", func(d *SkillDetail) { d.Visibility = "private" }, "visibility"},
		// Per-version branches inside the versions[] loop. latest_version
		// stays valid so top-level validation passes and the loop is
		// actually entered — earlier cases that mutate both fields short-
		// circuit at the latest_version check and never reach here.
		{"non-semver versions[].version", func(d *SkillDetail) { d.Versions[0].Version = "not-semver" }, "versions[0].version"},
		{"non-UTC versions[].published_at", func(d *SkillDetail) {
			d.Versions[0].PublishedAt = time.Date(2026, 5, 23, 12, 0, 0, 0, time.FixedZone("UTC-5", -5*3600))
		}, "versions[0].published_at"},
		{"sub-second versions[].published_at", func(d *SkillDetail) {
			d.Versions[0].PublishedAt = time.Date(2026, 5, 23, 12, 0, 0, 500_000_000, time.UTC)
		}, "versions[0].published_at"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "skill.json")
			d := validSkillDetail()
			tc.mutate(&d)
			err := WriteSkillDetailAtomic(path, d)
			if err == nil {
				t.Fatalf("WriteSkillDetailAtomic accepted invalid detail: %+v", d)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q lacks %q context", err.Error(), tc.want)
			}
			if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
				t.Errorf("file should not exist after rejected write")
			}
		})
	}
}

func TestWriteSkillDetailAtomic_StableKeyOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skill.json")
	d := validSkillDetail()

	if err := WriteSkillDetailAtomic(path, d); err != nil {
		t.Fatalf("first write: %v", err)
	}
	first, _ := os.ReadFile(path)
	if err := WriteSkillDetailAtomic(path, d); err != nil {
		t.Fatalf("second write: %v", err)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Errorf("output not deterministic:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}
