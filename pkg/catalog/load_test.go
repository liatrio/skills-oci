package catalog

import (
	"strings"
	"testing"
)

func TestLoad_ValidV2RoundTrips(t *testing.T) {
	input := []byte(`{
  "schema_version": 2,
  "generated_at": "2026-05-23T12:00:00Z",
  "skills": [
    {
      "namespace": "liatrio",
      "name": "create-skill",
      "latest_version": "1.0.0",
      "updated_at": "2026-05-23T12:00:00Z",
      "status": "published",
      "visibility": "public",
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
	if c.SchemaVersion != 2 {
		t.Errorf("SchemaVersion = %d, want 2", c.SchemaVersion)
	}
	if c.GeneratedAt.IsZero() {
		t.Errorf("GeneratedAt unset; want non-zero")
	}
	if len(c.Skills) != 1 {
		t.Fatalf("len(Skills) = %d, want 1", len(c.Skills))
	}
	got := c.Skills[0]
	if got.Namespace != "liatrio" || got.Name != "create-skill" {
		t.Errorf("Skills[0] surface fields = %+v", got)
	}
	if got.Status != StatusPublished || got.Visibility != VisibilityPublic {
		t.Errorf("Skills[0] status/visibility = %q/%q", got.Status, got.Visibility)
	}
	if got.Repo != "anthropics/skills" || got.Commit != "bc6708cbbc37adb919157f04d31e601e68f4b9c2" {
		t.Errorf("Skills[0] source-pin fields = %+v", got)
	}
}

func TestLoad_EmptySkillsArray(t *testing.T) {
	input := []byte(`{"schema_version": 2, "generated_at": "2026-05-23T12:00:00Z", "skills": []}`)
	c, err := Load(input)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if c.SchemaVersion != 2 {
		t.Errorf("SchemaVersion = %d, want 2", c.SchemaVersion)
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
  "schema_version": 2,
  "generated_at": "2026-05-23T12:00:00Z",
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

func TestLoad_EmptyInputRejects(t *testing.T) {
	if _, err := Load(nil); err == nil {
		t.Error("Load(nil) accepted, want error")
	}
	if _, err := Load([]byte("")); err == nil {
		t.Error("Load(empty) accepted, want error")
	}
}

func TestLoad_ErrorMessageIncludesContext(t *testing.T) {
	// Error wrapping per CLAUDE.md: errors should include enough context
	// that a caller can produce a useful message.
	_, err := Load([]byte(`{"schema_version": "not-an-int"}`))
	if err == nil {
		t.Fatal("Load accepted bad schema_version type")
	}
	if !strings.Contains(err.Error(), "catalog") {
		t.Errorf("error %q lacks 'catalog' context", err.Error())
	}
}
