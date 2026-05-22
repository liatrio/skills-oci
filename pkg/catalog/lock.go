package catalog

import (
	"encoding/json"
	"fmt"
	"sort"
)

// ChangeKind enumerates how an entry differs between two Lock values.
type ChangeKind int

const (
	// ChangeAdded means the entry exists in the after lock but not the before.
	ChangeAdded ChangeKind = iota + 1
	// ChangeRemoved means the entry exists in the before lock but not the after.
	ChangeRemoved
	// ChangeBumped means the entry exists in both but its Commit changed.
	ChangeBumped
)

// String returns a human-readable label for a ChangeKind.
func (k ChangeKind) String() string {
	switch k {
	case ChangeAdded:
		return "added"
	case ChangeRemoved:
		return "removed"
	case ChangeBumped:
		return "bumped"
	default:
		return "unknown"
	}
}

// Change describes one entry-level difference between two Lock values.
// Unchanged entries are not emitted; consumers infer unchanged from absence.
type Change struct {
	Name string
	Kind ChangeKind
	From LockEntry // zero value when ChangeAdded
	To   LockEntry // zero value when ChangeRemoved
}

// WriteLockAtomic marshals l with stable key order and writes it to path
// via a temp file + rename. Like WriteCatalogAtomic, a failed write
// leaves no partial file behind.
func WriteLockAtomic(path string, l Lock) error {
	body, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling lock: %w", err)
	}
	body = append(body, '\n')
	return writeAtomic(path, body)
}

// Diff returns the entry-level changes between before and after. Output
// is sorted by entry name so callers can render deterministic summaries.
// Unchanged entries are deliberately omitted.
func Diff(before, after Lock) []Change {
	beforeByName := indexLock(before)
	afterByName := indexLock(after)

	names := make(map[string]struct{}, len(beforeByName)+len(afterByName))
	for n := range beforeByName {
		names[n] = struct{}{}
	}
	for n := range afterByName {
		names[n] = struct{}{}
	}

	out := make([]Change, 0, len(names))
	for name := range names {
		from, inBefore := beforeByName[name]
		to, inAfter := afterByName[name]
		switch {
		case !inBefore && inAfter:
			out = append(out, Change{Name: name, Kind: ChangeAdded, To: to})
		case inBefore && !inAfter:
			out = append(out, Change{Name: name, Kind: ChangeRemoved, From: from})
		case inBefore && inAfter && from.Commit != to.Commit:
			out = append(out, Change{Name: name, Kind: ChangeBumped, From: from, To: to})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func indexLock(l Lock) map[string]LockEntry {
	m := make(map[string]LockEntry, len(l.Skills))
	for _, e := range l.Skills {
		m[e.Name] = e
	}
	return m
}
