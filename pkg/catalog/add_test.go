package catalog

import (
	"strings"
	"testing"
)

func TestAddEntry_AppendsAtTail(t *testing.T) {
	a := validEntry()
	a.Name = "first"
	c := Catalog{SchemaVersion: 1, Skills: []Entry{a}}

	b := validEntry()
	b.Name = "second"

	out, err := AddEntry(c, b)
	if err != nil {
		t.Fatalf("AddEntry returned error: %v", err)
	}
	if len(out.Skills) != 2 {
		t.Fatalf("len(Skills) = %d, want 2", len(out.Skills))
	}
	if out.Skills[0].Name != "first" || out.Skills[1].Name != "second" {
		t.Errorf("ordering wrong: got %q,%q", out.Skills[0].Name, out.Skills[1].Name)
	}
}

func TestAddEntry_DoesNotMutateInput(t *testing.T) {
	a := validEntry()
	a.Name = "first"
	in := Catalog{SchemaVersion: 1, Skills: []Entry{a}}

	b := validEntry()
	b.Name = "second"

	_, err := AddEntry(in, b)
	if err != nil {
		t.Fatalf("AddEntry returned error: %v", err)
	}
	if len(in.Skills) != 1 {
		t.Errorf("input was mutated: len(Skills) = %d, want 1", len(in.Skills))
	}
}

func TestAddEntry_EmptyCatalogStillValid(t *testing.T) {
	in := Catalog{SchemaVersion: 1}
	e := validEntry()
	out, err := AddEntry(in, e)
	if err != nil {
		t.Fatalf("AddEntry returned error: %v", err)
	}
	if len(out.Skills) != 1 {
		t.Errorf("len(Skills) = %d, want 1", len(out.Skills))
	}
	if out.Skills[0].Name != e.Name {
		t.Errorf("Skills[0].Name = %q, want %q", out.Skills[0].Name, e.Name)
	}
}

func TestAddEntry_DuplicateNameReturnsValidateError(t *testing.T) {
	e := validEntry()
	in := Catalog{SchemaVersion: 1, Skills: []Entry{e}}

	_, err := AddEntry(in, e)
	if err == nil {
		t.Fatal("AddEntry accepted duplicate name")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error %q lacks 'duplicate' context", err.Error())
	}
}

func TestAddEntry_RejectsInvalidEntry(t *testing.T) {
	in := Catalog{SchemaVersion: 1}
	bad := validEntry()
	bad.Commit = "not-a-sha"

	_, err := AddEntry(in, bad)
	if err == nil {
		t.Fatal("AddEntry accepted invalid commit")
	}
	if !strings.Contains(err.Error(), "commit") {
		t.Errorf("error %q lacks 'commit' context", err.Error())
	}
}

func TestAddEntry_BootstrapsSchemaVersion(t *testing.T) {
	// AddEntry on a zero-value Catalog (SchemaVersion=0) should bootstrap
	// to v1 so callers building from scratch don't have to set it.
	in := Catalog{} // SchemaVersion intentionally 0
	out, err := AddEntry(in, validEntry())
	if err != nil {
		t.Fatalf("AddEntry returned error: %v", err)
	}
	if out.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1 (auto-bootstrap)", out.SchemaVersion)
	}
}

func TestAddEntry_ReturnsNewSlice(t *testing.T) {
	// The returned Catalog must not share its backing array with the input
	// — otherwise a subsequent append on the input would mutate the
	// returned value's data through the shared underlying array.
	a := validEntry()
	a.Name = "first"
	in := Catalog{SchemaVersion: 1, Skills: make([]Entry, 1, 4)}
	in.Skills[0] = a

	b := validEntry()
	b.Name = "second"
	out, err := AddEntry(in, b)
	if err != nil {
		t.Fatalf("AddEntry returned error: %v", err)
	}

	// Mutate the input after AddEntry; the output must not see the mutation.
	in.Skills = append(in.Skills, validEntry())
	if len(out.Skills) != 2 {
		t.Errorf("output mutated via shared backing array: len = %d, want 2", len(out.Skills))
	}
}
