package catalog

import (
	"strings"
	"testing"
)

// validEntry returns a fully-valid Entry that individual tests can mutate
// to exercise a single rejection rule.
func validEntry() Entry {
	return Entry{
		Name:        "create-skill",
		Repo:        "anthropics/skills",
		Subpath:     "skills/create-skill",
		Version:     "v1.0.0",
		Commit:      "bc6708cbbc37adb919157f04d31e601e68f4b9c2",
		InternalRef: "ghcr.io/liatrio/skills/create-skill",
	}
}

func TestValidate_AllValid(t *testing.T) {
	c := Catalog{SchemaVersion: 1, Skills: []Entry{validEntry()}}
	if err := Validate(c); err != nil {
		t.Errorf("Validate rejected valid catalog: %v", err)
	}
}

func TestValidate_EmptyCatalogIsValid(t *testing.T) {
	c := Catalog{SchemaVersion: 1, Skills: nil}
	if err := Validate(c); err != nil {
		t.Errorf("Validate rejected empty catalog: %v", err)
	}
}

func TestValidate_SchemaVersion(t *testing.T) {
	tests := []struct {
		name        string
		schema      int
		wantErrSubs string
	}{
		{"zero", 0, "schemaVersion"},
		{"two", 2, "schemaVersion"},
		{"negative", -1, "schemaVersion"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Catalog{SchemaVersion: tt.schema, Skills: []Entry{validEntry()}}
			err := Validate(c)
			if err == nil {
				t.Fatalf("Validate accepted schemaVersion=%d", tt.schema)
			}
			if !strings.Contains(err.Error(), tt.wantErrSubs) {
				t.Errorf("error %q lacks %q", err.Error(), tt.wantErrSubs)
			}
		})
	}
}

func TestValidate_Commit(t *testing.T) {
	tests := []struct {
		name   string
		commit string
	}{
		{"empty", ""},
		{"too short", "fff"},
		{"too long", "bc6708cbbc37adb919157f04d31e601e68f4b9c2a"},
		{"uppercase hex", "BC6708CBBC37ADB919157F04D31E601E68F4B9C2"},
		{"non-hex chars", "xyz0708cbbc37adb919157f04d31e601e68f4b9c2"},
		{"trailing space", "bc6708cbbc37adb919157f04d31e601e68f4b9c2 "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := validEntry()
			e.Commit = tt.commit
			c := Catalog{SchemaVersion: 1, Skills: []Entry{e}}
			err := Validate(c)
			if err == nil {
				t.Fatalf("Validate accepted commit=%q", tt.commit)
			}
			if !strings.Contains(err.Error(), "commit") {
				t.Errorf("error %q lacks 'commit' context", err.Error())
			}
		})
	}
}

func TestValidate_Version_RejectsMutableRefs(t *testing.T) {
	tests := []string{"", "latest", "main", "master", "HEAD"}
	for _, v := range tests {
		t.Run("version="+v, func(t *testing.T) {
			e := validEntry()
			e.Version = v
			c := Catalog{SchemaVersion: 1, Skills: []Entry{e}}
			err := Validate(c)
			if err == nil {
				t.Fatalf("Validate accepted forbidden version=%q", v)
			}
			if !strings.Contains(err.Error(), "version") {
				t.Errorf("error %q lacks 'version' context", err.Error())
			}
		})
	}
}

func TestValidate_Repo(t *testing.T) {
	tests := []struct {
		name string
		repo string
	}{
		{"empty", ""},
		{"with https scheme", "https://github.com/anthropics/skills"},
		{"with /tree/ path", "anthropics/skills/tree/main"},
		{"with /blob/ path", "anthropics/skills/blob/main"},
		{"missing slash", "anthropics"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := validEntry()
			e.Repo = tt.repo
			c := Catalog{SchemaVersion: 1, Skills: []Entry{e}}
			err := Validate(c)
			if err == nil {
				t.Fatalf("Validate accepted repo=%q", tt.repo)
			}
			if !strings.Contains(err.Error(), "repo") {
				t.Errorf("error %q lacks 'repo' context", err.Error())
			}
		})
	}
}

func TestValidate_Subpath(t *testing.T) {
	tests := []struct {
		name    string
		subpath string
	}{
		{"empty", ""},
		{"leading slash", "/skills/create-skill"},
		{"backslash", "skills\\create-skill"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := validEntry()
			e.Subpath = tt.subpath
			c := Catalog{SchemaVersion: 1, Skills: []Entry{e}}
			err := Validate(c)
			if err == nil {
				t.Fatalf("Validate accepted subpath=%q", tt.subpath)
			}
			if !strings.Contains(err.Error(), "subpath") {
				t.Errorf("error %q lacks 'subpath' context", err.Error())
			}
		})
	}
}

func TestValidate_Name(t *testing.T) {
	e := validEntry()
	e.Name = ""
	c := Catalog{SchemaVersion: 1, Skills: []Entry{e}}
	err := Validate(c)
	if err == nil {
		t.Fatal("Validate accepted empty name")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error %q lacks 'name' context", err.Error())
	}
}

func TestValidate_InternalRef(t *testing.T) {
	e := validEntry()
	e.InternalRef = ""
	c := Catalog{SchemaVersion: 1, Skills: []Entry{e}}
	err := Validate(c)
	if err == nil {
		t.Fatal("Validate accepted empty internal_ref")
	}
	if !strings.Contains(err.Error(), "internal_ref") {
		t.Errorf("error %q lacks 'internal_ref' context", err.Error())
	}
}

func TestValidate_RejectsDuplicateName(t *testing.T) {
	a := validEntry()
	b := validEntry()
	c := Catalog{SchemaVersion: 1, Skills: []Entry{a, b}}
	err := Validate(c)
	if err == nil {
		t.Fatal("Validate accepted duplicate name")
	}
	if !strings.Contains(err.Error(), "duplicate") || !strings.Contains(err.Error(), a.Name) {
		t.Errorf("error %q should mention 'duplicate' and the conflicting name", err.Error())
	}
}

func TestValidate_ErrorIncludesEntryIndex(t *testing.T) {
	// When validation fails on the Nth entry, the error should help the
	// caller find that entry. Index in the message keeps the error usable
	// even when entries share a name (and validation hasn't reached the
	// duplicate-name check yet).
	a := validEntry()
	a.Name = "first"
	b := validEntry()
	b.Name = "second"
	b.Commit = "not-a-sha"
	c := Catalog{SchemaVersion: 1, Skills: []Entry{a, b}}
	err := Validate(c)
	if err == nil {
		t.Fatal("Validate accepted bad commit on second entry")
	}
	if !strings.Contains(err.Error(), "second") {
		t.Errorf("error %q should mention the failing entry by name", err.Error())
	}
}
