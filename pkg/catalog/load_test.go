package catalog

import (
	"strings"
	"testing"
)

func TestLoad_ValidV1RoundTrips(t *testing.T) {
	input := []byte(`{
  "schemaVersion": 1,
  "skills": [
    {
      "name": "create-skill",
      "repo": "anthropics/skills",
      "subpath": "skills/create-skill",
      "version": "v1.0.0",
      "commit": "bc6708cbbc37adb919157f04d31e601e68f4b9c2",
      "internal_ref": "ghcr.io/liatrio/skills/create-skill"
    }
  ]
}`)

	c, err := Load(input)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if c.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", c.SchemaVersion)
	}
	if len(c.Skills) != 1 {
		t.Fatalf("len(Skills) = %d, want 1", len(c.Skills))
	}
	got := c.Skills[0]
	want := Entry{
		Name:        "create-skill",
		Repo:        "anthropics/skills",
		Subpath:     "skills/create-skill",
		Version:     "v1.0.0",
		Commit:      "bc6708cbbc37adb919157f04d31e601e68f4b9c2",
		InternalRef: "ghcr.io/liatrio/skills/create-skill",
	}
	if got != want {
		t.Errorf("Skills[0] = %+v, want %+v", got, want)
	}
}

func TestLoad_EmptySkillsArray(t *testing.T) {
	input := []byte(`{"schemaVersion": 1, "skills": []}`)
	c, err := Load(input)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if c.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", c.SchemaVersion)
	}
	if len(c.Skills) != 0 {
		t.Errorf("len(Skills) = %d, want 0", len(c.Skills))
	}
}

func TestLoad_TolerateUnknownExtraField(t *testing.T) {
	// Forward-compat: unknown top-level keys are tolerated so adding a new
	// optional field in a future minor version of the contract does not
	// break older readers.
	input := []byte(`{
  "schemaVersion": 1,
  "skills": [],
  "futureField": "should-be-ignored"
}`)
	if _, err := Load(input); err != nil {
		t.Errorf("Load rejected unknown field: %v", err)
	}
}

func TestLoad_RejectsInvalidJSON(t *testing.T) {
	input := []byte(`{not json`)
	if _, err := Load(input); err == nil {
		t.Error("Load accepted invalid JSON, want error")
	}
}

func TestLoadLock_ValidV1RoundTrips(t *testing.T) {
	input := []byte(`{
  "lockfileVersion": 1,
  "generatedAt": "2026-05-22T18:30:00Z",
  "skills": [
    {
      "name": "create-skill",
      "internal_ref": "ghcr.io/liatrio/skills/create-skill",
      "tag": "v1.0.0",
      "commit": "bc6708cbbc37adb919157f04d31e601e68f4b9c2",
      "digest": "sha256:1234",
      "ref": "ghcr.io/liatrio/skills/create-skill:v1.0.0@sha256:1234",
      "syncedAt": "2026-05-22T18:30:14Z"
    }
  ]
}`)

	l, err := LoadLock(input)
	if err != nil {
		t.Fatalf("LoadLock returned error: %v", err)
	}
	if l.LockfileVersion != 1 {
		t.Errorf("LockfileVersion = %d, want 1", l.LockfileVersion)
	}
	if l.GeneratedAt != "2026-05-22T18:30:00Z" {
		t.Errorf("GeneratedAt = %q, want %q", l.GeneratedAt, "2026-05-22T18:30:00Z")
	}
	if len(l.Skills) != 1 {
		t.Fatalf("len(Skills) = %d, want 1", len(l.Skills))
	}
	if l.Skills[0].Digest != "sha256:1234" {
		t.Errorf("Skills[0].Digest = %q, want %q", l.Skills[0].Digest, "sha256:1234")
	}
}

func TestLoadLock_RejectsInvalidJSON(t *testing.T) {
	if _, err := LoadLock([]byte(`{[`)); err == nil {
		t.Error("LoadLock accepted invalid JSON, want error")
	}
}

func TestLoad_EmptyInputRejects(t *testing.T) {
	if _, err := Load(nil); err == nil {
		t.Error("Load(nil) accepted, want error")
	}
	if _, err := Load([]byte("")); err == nil {
		t.Error("Load(empty) accepted, want error")
	}
}

func TestLoadLock_EmptyInputRejects(t *testing.T) {
	if _, err := LoadLock(nil); err == nil {
		t.Error("LoadLock(nil) accepted, want error")
	}
	if _, err := LoadLock([]byte("  ")); err == nil {
		t.Error("LoadLock(whitespace) accepted, want error")
	}
}

func TestLoad_ErrorMessageIncludesContext(t *testing.T) {
	// Error wrapping per CLAUDE.md: errors should include enough context
	// that a caller can produce a useful message.
	_, err := Load([]byte(`{"schemaVersion": "not-an-int"}`))
	if err == nil {
		t.Fatal("Load accepted bad schemaVersion type")
	}
	if !strings.Contains(err.Error(), "catalog") {
		t.Errorf("error %q lacks 'catalog' context", err.Error())
	}
}
