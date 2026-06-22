package catalog

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// validVendoredEntry returns a fully-populated, valid VendoredEntry for reuse.
func validVendoredEntry() VendoredEntry {
	return VendoredEntry{
		Name:      "manage-pull-requests",
		Namespace: "liatrio-labs",
		Repo:      "liatrio-labs/skills",
		Subpath:   "skills/manage-pull-requests",
		Commit:    "0123456789abcdef0123456789abcdef01234567",
	}
}

func TestLoadVendored_RoundTrip(t *testing.T) {
	// Arrange: the on-disk JSON shape — camelCase schemaVersion, the upstream
	// coordinate fields (no destination ref; the consumer derives it).
	in := []byte(`{
  "schemaVersion": 1,
  "skills": [
    {
      "name": "manage-pull-requests",
      "namespace": "liatrio-labs",
      "repo": "liatrio-labs/skills",
      "subpath": "skills/manage-pull-requests",
      "commit": "0123456789abcdef0123456789abcdef01234567"
    }
  ]
}`)

	// Act
	v, err := LoadVendored(in)
	if err != nil {
		t.Fatalf("LoadVendored: %v", err)
	}

	// Assert: parsed values match the JSON.
	if v.SchemaVersion != 1 {
		t.Errorf("schemaVersion = %d, want 1", v.SchemaVersion)
	}
	if len(v.Skills) != 1 {
		t.Fatalf("len(skills) = %d, want 1", len(v.Skills))
	}
	want := validVendoredEntry()
	if !reflect.DeepEqual(v.Skills[0], want) {
		t.Errorf("entry = %+v, want %+v", v.Skills[0], want)
	}

	// Round-trip: write to disk, reload, compare structs.
	dir := t.TempDir()
	path := filepath.Join(dir, "vendored.json")
	if err := WriteVendoredAtomic(path, v); err != nil {
		t.Fatalf("WriteVendoredAtomic: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	v2, err := LoadVendored(body)
	if err != nil {
		t.Fatalf("LoadVendored (round 2): %v", err)
	}
	if !reflect.DeepEqual(v, v2) {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", v2, v)
	}
}

func TestVendored_LicenseRoundTrip(t *testing.T) {
	// Arrange: an entry carrying an optional license string.
	in := []byte(`{
  "schemaVersion": 1,
  "skills": [
    {
      "name": "manage-pull-requests",
      "namespace": "liatrio-labs",
      "repo": "liatrio-labs/skills",
      "subpath": "skills/manage-pull-requests",
      "commit": "0123456789abcdef0123456789abcdef01234567",
      "license": "Apache-2.0"
    }
  ]
}`)

	// Act
	v, err := LoadVendored(in)
	if err != nil {
		t.Fatalf("LoadVendored: %v", err)
	}

	// Assert: license parsed and survives a write/reload round-trip.
	if got := v.Skills[0].License; got != "Apache-2.0" {
		t.Errorf("license = %q, want %q", got, "Apache-2.0")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "vendored.json")
	if err := WriteVendoredAtomic(path, v); err != nil {
		t.Fatalf("WriteVendoredAtomic: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(body), `"license": "Apache-2.0"`) {
		t.Errorf("written vendored.json missing license field:\n%s", body)
	}
	v2, err := LoadVendored(body)
	if err != nil {
		t.Fatalf("LoadVendored (round 2): %v", err)
	}
	if !reflect.DeepEqual(v, v2) {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", v2, v)
	}
}

func TestVendored_LicenseOmittedWhenEmpty(t *testing.T) {
	// An entry with no license must not emit a "license" key (omitempty), so
	// existing entries and SKILL.md files without a license stay clean.
	dir := t.TempDir()
	path := filepath.Join(dir, "vendored.json")
	v := Vendored{SchemaVersion: 1, Skills: []VendoredEntry{validVendoredEntry()}}
	if err := WriteVendoredAtomic(path, v); err != nil {
		t.Fatalf("WriteVendoredAtomic: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if strings.Contains(string(body), "license") {
		t.Errorf("empty license should be omitted, got:\n%s", body)
	}
}

func TestLoadVendored_Errors(t *testing.T) {
	if _, err := LoadVendored([]byte("   \n")); err == nil {
		t.Errorf("LoadVendored(empty) = nil, want error")
	}
	if _, err := LoadVendored([]byte("{not json")); err == nil {
		t.Errorf("LoadVendored(malformed) = nil, want error")
	}
}

func TestValidateVendored(t *testing.T) {
	missing := func(mut func(*VendoredEntry)) Vendored {
		e := validVendoredEntry()
		mut(&e)
		return Vendored{SchemaVersion: 1, Skills: []VendoredEntry{e}}
	}

	tests := []struct {
		name    string
		in      Vendored
		wantErr bool
	}{
		{"valid", Vendored{SchemaVersion: 1, Skills: []VendoredEntry{validVendoredEntry()}}, false},
		{"valid empty skills", Vendored{SchemaVersion: 1}, false},
		{"bad schemaVersion zero", Vendored{SchemaVersion: 0, Skills: []VendoredEntry{validVendoredEntry()}}, true},
		{"bad schemaVersion two", Vendored{SchemaVersion: 2, Skills: []VendoredEntry{validVendoredEntry()}}, true},
		{"missing name", missing(func(e *VendoredEntry) { e.Name = "" }), true},
		{"missing namespace", missing(func(e *VendoredEntry) { e.Namespace = "" }), true},
		{"missing repo", missing(func(e *VendoredEntry) { e.Repo = "" }), true},
		{"missing subpath", missing(func(e *VendoredEntry) { e.Subpath = "" }), true},
		{"missing commit", missing(func(e *VendoredEntry) { e.Commit = "" }), true},
		{"commit too short", missing(func(e *VendoredEntry) { e.Commit = "0123abc" }), true},
		{"commit uppercase", missing(func(e *VendoredEntry) { e.Commit = strings.ToUpper(validVendoredEntry().Commit) }), true},
		{"commit non-hex", missing(func(e *VendoredEntry) { e.Commit = "g123456789abcdef0123456789abcdef01234567" }), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateVendored(tt.in)
			if tt.wantErr && err == nil {
				t.Errorf("ValidateVendored(%s) = nil, want error", tt.name)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidateVendored(%s) = %v, want nil", tt.name, err)
			}
		})
	}
}

func TestUpsertVendored(t *testing.T) {
	t.Run("append new keeps deterministic order", func(t *testing.T) {
		base := Vendored{SchemaVersion: 1, Skills: []VendoredEntry{
			{Name: "zebra", Namespace: "ns-b", Repo: "o/r", Subpath: "s", Commit: validVendoredEntry().Commit},
		}}
		add := VendoredEntry{Name: "alpha", Namespace: "ns-a", Repo: "o/r", Subpath: "s", Commit: validVendoredEntry().Commit}
		got, replaced := UpsertVendored(base, add)
		if replaced {
			t.Errorf("replaced = true, want false for new entry")
		}
		if len(got.Skills) != 2 {
			t.Fatalf("len = %d, want 2", len(got.Skills))
		}
		// Sorted by namespace then name: ns-a/alpha before ns-b/zebra.
		if got.Skills[0].Namespace != "ns-a" || got.Skills[1].Namespace != "ns-b" {
			t.Errorf("order = [%s, %s], want [ns-a, ns-b]", got.Skills[0].Namespace, got.Skills[1].Namespace)
		}
	})

	t.Run("orders by name within the same namespace", func(t *testing.T) {
		base := Vendored{SchemaVersion: 1, Skills: []VendoredEntry{
			{Name: "yak", Namespace: "ns", Repo: "o/r", Subpath: "s", Commit: validVendoredEntry().Commit},
		}}
		add := VendoredEntry{Name: "ant", Namespace: "ns", Repo: "o/r", Subpath: "s", Commit: validVendoredEntry().Commit}
		got, _ := UpsertVendored(base, add)
		if got.Skills[0].Name != "ant" || got.Skills[1].Name != "yak" {
			t.Errorf("name order = [%s, %s], want [ant, yak]", got.Skills[0].Name, got.Skills[1].Name)
		}
	})

	t.Run("replace existing by (namespace,name)", func(t *testing.T) {
		base := Vendored{SchemaVersion: 1, Skills: []VendoredEntry{validVendoredEntry()}}
		bump := validVendoredEntry()
		bump.Commit = "fedcba9876543210fedcba9876543210fedcba98"
		got, replaced := UpsertVendored(base, bump)
		if !replaced {
			t.Errorf("replaced = false, want true for existing (namespace,name)")
		}
		if len(got.Skills) != 1 {
			t.Fatalf("len = %d, want 1 (in-place replace)", len(got.Skills))
		}
		if got.Skills[0].Commit != bump.Commit {
			t.Errorf("entry not replaced: %+v", got.Skills[0])
		}
	})

	t.Run("does not mutate the input", func(t *testing.T) {
		base := Vendored{SchemaVersion: 1, Skills: []VendoredEntry{validVendoredEntry()}}
		origCommit := base.Skills[0].Commit
		bump := validVendoredEntry()
		bump.Commit = "fedcba9876543210fedcba9876543210fedcba98"
		_, _ = UpsertVendored(base, bump)
		if base.Skills[0].Commit != origCommit {
			t.Errorf("input mutated: base commit = %q, want %q", base.Skills[0].Commit, origCommit)
		}
	})

	t.Run("defaults schemaVersion when zero", func(t *testing.T) {
		got, _ := UpsertVendored(Vendored{}, validVendoredEntry())
		if got.SchemaVersion != 1 {
			t.Errorf("schemaVersion = %d, want 1 (bootstrapped from zero value)", got.SchemaVersion)
		}
	})
}

func TestWriteVendoredAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vendored.json")

	// Unsorted input; the writer should persist in deterministic order.
	v := Vendored{SchemaVersion: 1, Skills: []VendoredEntry{
		{Name: "zebra", Namespace: "ns-b", Repo: "o/r", Subpath: "s", Commit: validVendoredEntry().Commit},
		{Name: "alpha", Namespace: "ns-a", Repo: "o/r", Subpath: "s", Commit: validVendoredEntry().Commit},
	}}
	if err := WriteVendoredAtomic(path, v); err != nil {
		t.Fatalf("WriteVendoredAtomic: %v", err)
	}

	// Reload and confirm content survived.
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.HasSuffix(string(body), "\n") {
		t.Errorf("file should end with a trailing newline")
	}
	reloaded, err := LoadVendored(body)
	if err != nil {
		t.Fatalf("LoadVendored: %v", err)
	}
	if len(reloaded.Skills) != 2 {
		t.Fatalf("reloaded len = %d, want 2", len(reloaded.Skills))
	}

	// A zero-value SchemaVersion is bootstrapped to 1 rather than rejected.
	zeroPath := filepath.Join(dir, "zero.json")
	if err := WriteVendoredAtomic(zeroPath, Vendored{Skills: []VendoredEntry{validVendoredEntry()}}); err != nil {
		t.Fatalf("WriteVendoredAtomic(zero schema): %v", err)
	}
	zb, err := os.ReadFile(zeroPath)
	if err != nil {
		t.Fatalf("read zero: %v", err)
	}
	if zv, _ := LoadVendored(zb); zv.SchemaVersion != 1 {
		t.Errorf("bootstrapped schemaVersion = %d, want 1", zv.SchemaVersion)
	}

	// Rejects invalid content before writing (no partial file at a fresh path).
	bad := Vendored{SchemaVersion: 1, Skills: []VendoredEntry{{Name: "x"}}}
	badPath := filepath.Join(dir, "bad.json")
	if err := WriteVendoredAtomic(badPath, bad); err == nil {
		t.Errorf("WriteVendoredAtomic accepted invalid content")
	}
	if _, err := os.Stat(badPath); !os.IsNotExist(err) {
		t.Errorf("invalid write left a file at %s", badPath)
	}
}
