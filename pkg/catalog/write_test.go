package catalog

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteCatalogAtomic_WritesValidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")

	c := Catalog{SchemaVersion: 1, Skills: []Entry{validEntry()}}
	if err := WriteCatalogAtomic(path, c); err != nil {
		t.Fatalf("WriteCatalogAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	round, err := Load(got)
	if err != nil {
		t.Fatalf("written file did not round-trip through Load: %v", err)
	}
	if len(round.Skills) != 1 || round.Skills[0].Name != "create-skill" {
		t.Errorf("unexpected contents: %+v", round)
	}
}

func TestWriteCatalogAtomic_StableKeyOrderAcrossCalls(t *testing.T) {
	// Two calls with the same input must produce byte-identical files so
	// `catalog add` and Renovate updates produce minimal diffs.
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	c := Catalog{SchemaVersion: 1, Skills: []Entry{validEntry()}}

	if err := WriteCatalogAtomic(path, c); err != nil {
		t.Fatalf("first write: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile first: %v", err)
	}

	if err := WriteCatalogAtomic(path, c); err != nil {
		t.Fatalf("second write: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile second: %v", err)
	}

	if !bytes.Equal(first, second) {
		t.Errorf("WriteCatalogAtomic output not deterministic:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestWriteCatalogAtomic_RejectsInvalidCatalog(t *testing.T) {
	// Validate is the gate — writing a known-bad catalog shouldn't leave
	// junk on disk.
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")

	bad := Catalog{SchemaVersion: 1, Skills: []Entry{validEntry()}}
	bad.Skills[0].Commit = "not-a-sha"

	if err := WriteCatalogAtomic(path, bad); err == nil {
		t.Fatal("WriteCatalogAtomic accepted invalid catalog")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("target file should not exist after rejected write, got %v", err)
	}
}

func TestWriteCatalogAtomic_NoPartialFileOnRenameFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission semantics differ on windows; rename-failure simulated path skipped")
	}
	dir := t.TempDir()
	// Make the parent dir read-only so the rename into it fails. The
	// pre-rename temp file should then be cleaned up by the writer.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	path := filepath.Join(dir, "catalog.json")
	c := Catalog{SchemaVersion: 1, Skills: []Entry{validEntry()}}
	err := WriteCatalogAtomic(path, c)
	if err == nil {
		t.Fatal("expected error writing into read-only dir")
	}
	// Allow the writer's cleanup to run by re-enabling write, then check.
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod restore: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		// No temp files (e.g. catalog.json.tmp.<pid>) should remain.
		if strings.HasPrefix(e.Name(), "catalog.json") {
			t.Errorf("stray file left behind: %s", e.Name())
		}
	}
}

func TestWriteCatalogAtomic_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")

	first := Catalog{SchemaVersion: 1, Skills: []Entry{validEntry()}}
	if err := WriteCatalogAtomic(path, first); err != nil {
		t.Fatalf("first write: %v", err)
	}

	second := Catalog{SchemaVersion: 1}
	if err := WriteCatalogAtomic(path, second); err != nil {
		t.Fatalf("second write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	round, err := Load(got)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(round.Skills) != 0 {
		t.Errorf("len(Skills) after overwrite = %d, want 0", len(round.Skills))
	}
}
