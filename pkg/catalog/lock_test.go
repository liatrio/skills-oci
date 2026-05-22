package catalog

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func validLockEntry() LockEntry {
	return LockEntry{
		Name:        "create-skill",
		InternalRef: "ghcr.io/liatrio/skills/create-skill",
		Tag:         "v1.0.0",
		Commit:      "bc6708cbbc37adb919157f04d31e601e68f4b9c2",
		Digest:      "sha256:1234567890abcdef",
		Ref:         "ghcr.io/liatrio/skills/create-skill:v1.0.0@sha256:1234567890abcdef",
		SyncedAt:    "2026-05-22T18:30:14Z",
	}
}

func TestWriteLockAtomic_WritesValidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog-lock.json")
	l := Lock{
		LockfileVersion: 1,
		GeneratedAt:     "2026-05-22T18:30:00Z",
		Skills:          []LockEntry{validLockEntry()},
	}

	if err := WriteLockAtomic(path, l); err != nil {
		t.Fatalf("WriteLockAtomic: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	round, err := LoadLock(got)
	if err != nil {
		t.Fatalf("written lock did not round-trip: %v", err)
	}
	if len(round.Skills) != 1 || round.Skills[0].Digest != l.Skills[0].Digest {
		t.Errorf("round-trip mismatch: %+v", round)
	}
}

func TestWriteLockAtomic_StableKeyOrderAcrossCalls(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog-lock.json")
	l := Lock{LockfileVersion: 1, GeneratedAt: "2026-05-22T18:30:00Z", Skills: []LockEntry{validLockEntry()}}

	if err := WriteLockAtomic(path, l); err != nil {
		t.Fatalf("first write: %v", err)
	}
	first, _ := os.ReadFile(path)
	if err := WriteLockAtomic(path, l); err != nil {
		t.Fatalf("second write: %v", err)
	}
	second, _ := os.ReadFile(path)
	if !bytes.Equal(first, second) {
		t.Errorf("lockfile output not deterministic")
	}
}

func TestDiff_Combinations(t *testing.T) {
	unchanged := validLockEntry()
	unchanged.Name = "unchanged-entry"

	bumpedBefore := validLockEntry()
	bumpedBefore.Name = "bumped-entry"
	bumpedBefore.Commit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	bumpedAfter := validLockEntry()
	bumpedAfter.Name = "bumped-entry"
	bumpedAfter.Commit = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	removed := validLockEntry()
	removed.Name = "removed-entry"

	added := validLockEntry()
	added.Name = "added-entry"

	before := Lock{LockfileVersion: 1, Skills: []LockEntry{unchanged, bumpedBefore, removed}}
	after := Lock{LockfileVersion: 1, Skills: []LockEntry{unchanged, bumpedAfter, added}}

	changes := Diff(before, after)
	got := map[string]ChangeKind{}
	for _, ch := range changes {
		got[ch.Name] = ch.Kind
	}
	want := map[string]ChangeKind{
		"bumped-entry":  ChangeBumped,
		"removed-entry": ChangeRemoved,
		"added-entry":   ChangeAdded,
	}
	if len(got) != len(want) {
		t.Errorf("got %d changes (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for name, kind := range want {
		if got[name] != kind {
			t.Errorf("Diff[%q] = %v, want %v", name, got[name], kind)
		}
	}
	// Unchanged entries must NOT appear in Diff output.
	if _, ok := got["unchanged-entry"]; ok {
		t.Error("Diff should not surface unchanged entries")
	}
}

func TestDiff_EmptyBeforeProducesOnlyAdds(t *testing.T) {
	before := Lock{LockfileVersion: 1}
	after := Lock{LockfileVersion: 1, Skills: []LockEntry{validLockEntry()}}
	changes := Diff(before, after)
	if len(changes) != 1 {
		t.Fatalf("len(changes) = %d, want 1", len(changes))
	}
	if changes[0].Kind != ChangeAdded {
		t.Errorf("Kind = %v, want %v", changes[0].Kind, ChangeAdded)
	}
}

func TestDiff_EmptyAfterProducesOnlyRemoves(t *testing.T) {
	before := Lock{LockfileVersion: 1, Skills: []LockEntry{validLockEntry()}}
	after := Lock{LockfileVersion: 1}
	changes := Diff(before, after)
	if len(changes) != 1 {
		t.Fatalf("len(changes) = %d, want 1", len(changes))
	}
	if changes[0].Kind != ChangeRemoved {
		t.Errorf("Kind = %v, want %v", changes[0].Kind, ChangeRemoved)
	}
}

func TestDiff_IdenticalLocksHaveNoChanges(t *testing.T) {
	l := Lock{LockfileVersion: 1, Skills: []LockEntry{validLockEntry()}}
	changes := Diff(l, l)
	if len(changes) != 0 {
		t.Errorf("identical locks produced %d changes: %+v", len(changes), changes)
	}
}

func TestChangeKind_String(t *testing.T) {
	tests := map[ChangeKind]string{
		ChangeAdded:   "added",
		ChangeRemoved: "removed",
		ChangeBumped:  "bumped",
		ChangeKind(0): "unknown",
	}
	for kind, want := range tests {
		if got := kind.String(); got != want {
			t.Errorf("ChangeKind(%d).String() = %q, want %q", kind, got, want)
		}
	}
}
