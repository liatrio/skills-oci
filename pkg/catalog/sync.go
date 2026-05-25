package catalog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// Outcome is the per-entry result classification.
type Outcome string

const (
	OutcomeSynced  Outcome = "synced"
	OutcomeFailed  Outcome = "failed"
	OutcomeSkipped Outcome = "skipped"
)

// Opts configures a Sync run. Concurrency, AllowMissingLicense, DryRun,
// and Only are caller-supplied; CatalogPath and LockPath default to
// catalog.json / catalog-lock.json in the working directory if empty.
type Opts struct {
	CatalogPath         string
	LockPath            string
	Concurrency         int
	AllowMissingLicense bool
	DryRun              bool
	Only                []string // limit sync to these entry names; empty = all

	// PlainHTTP, ExtraAnnotations are forwarded to the Pusher for each entry.
	PlainHTTP bool

	// EntryAnnotations is called per entry to compute extra OCI annotations
	// for that entry's push. Production code provides the
	// org.opencontainers.image.source URL; tests can swap in static maps.
	// May be nil; nil means no extra annotations.
	EntryAnnotations func(e Entry) map[string]string

	// Now returns the current time. Pinned for deterministic tests.
	Now func() time.Time

	// OnEntry, if non-nil, is invoked once per entry with the live status
	// of that entry as it transitions through Queued → Cloning → Pushing
	// → Done/Failed/Skipped. Used by the CLI to drive the minimum-viable
	// TUI and the --plain progress writer. Safe to invoke concurrently
	// from multiple goroutines.
	OnEntry func(status EntryStatus)

	// OnTelemetry, if non-nil, fires once per entry result so the CLI can
	// emit a catalog.synced event. Tests pass a no-op (or a recording
	// callback) so the orchestrator stays decoupled from pkg/telemetry.
	OnTelemetry func(result EntryResult)
}

// EntryStatus is the live state of one entry in flight, surfaced to the
// CLI for rendering. Stage transitions monotonically through the values
// declared below.
type EntryStatus struct {
	Index  int
	Total  int
	Name   string
	Stage  string // "queued" | "cloning" | "pushing" | "done" | "failed" | "skipped"
	Commit string // populated once known
	Digest string // populated once pushed
	Err    error  // populated for "failed"
	Detail string // freeform extra info (e.g. "already at <commit-short>")
}

// EntryResult records one entry's terminal outcome after Sync completes.
// Err is non-nil only when Outcome == OutcomeFailed.
type EntryResult struct {
	Name         string
	Outcome      Outcome
	Commit       string
	Digest       string
	Ref          string // <internal_ref>:<tag>@<digest>, populated only for synced
	Tag          string
	InternalRef  string // destination ref without tag, populated for every outcome
	UpstreamRepo string
	Err          error
}

// Result is the aggregate outcome of a Sync run.
type Result struct {
	Entries []EntryResult
}

// Counts returns the (synced, failed, skipped) tuple over all entries.
func (r Result) Counts() (synced, failed, skipped int) {
	for _, e := range r.Entries {
		switch e.Outcome {
		case OutcomeSynced:
			synced++
		case OutcomeFailed:
			failed++
		case OutcomeSkipped:
			skipped++
		}
	}
	return
}

// AnyFailed returns true if any entry resulted in OutcomeFailed.
func (r Result) AnyFailed() bool {
	for _, e := range r.Entries {
		if e.Outcome == OutcomeFailed {
			return true
		}
	}
	return false
}

// Fetcher is the seam between pkg/catalog/sync and pkg/scm. Production
// passes a thin wrapper; tests pass a fake that drops fixture files into
// dst (or returns an error to simulate upstream failures).
type Fetcher interface {
	Fetch(ctx context.Context, owner, repo, subpath, commit, dst string) error
}

// LicenseReader returns the upstream SKILL.md's declared license (or
// empty string when none is declared). Default production implementation
// uses pkg/skill.Parse; tests can supply pre-canned strings without a
// real SKILL.md on disk.
type LicenseReader interface {
	ReadLicense(skillDir string) (string, error)
}

// Pusher is the seam between pkg/catalog/sync and pkg/oci. Tests
// implement this directly; production wraps pkg/oci.Push.
type Pusher interface {
	Push(ctx context.Context, in PushInput) (digest string, err error)
}

// PushInput is the orchestrator's view of what a push needs. Decoupled
// from pkg/oci.PushOptions so test doubles do not have to model OCI
// internals.
type PushInput struct {
	InternalRef      string
	Tag              string
	SkillDir         string
	PlainHTTP        bool
	ExtraAnnotations map[string]string
}

// Sync reconciles every entry in the catalog at opts.CatalogPath against
// the destination registry. Behavior:
//
//   - Each entry runs in its own goroutine, bounded by opts.Concurrency
//     (default 4 when ≤ 0).
//   - An entry is skipped (OutcomeSkipped, no network) when its declared
//     commit matches the existing lock entry for the same Name.
//   - A failure in one entry never aborts the others. Sync returns nil
//     error for orchestration; per-entry errors land in Result.Entries.
//   - The lockfile is written atomically after all entries complete,
//     merging current-run successes with prior lock state for failed and
//     skipped entries. The previous lock entry is preserved verbatim for
//     a failed entry — a failed sync never regresses the lockfile.
//   - When opts.DryRun is set: the registry is never contacted (Pusher
//     is not called) and the lockfile is not written. The Result still
//     reports what would have happened.
//
// The returned error is non-nil only when a setup error prevents the run
// from starting (loading the catalog, validating it, etc.) or when the
// lockfile write itself fails. Per-entry failures do not propagate to
// the returned error.
func Sync(ctx context.Context, opts Opts, fet Fetcher, lic LicenseReader, push Pusher) (Result, error) {
	if opts.CatalogPath == "" {
		opts.CatalogPath = "catalog.json"
	}
	if opts.LockPath == "" {
		opts.LockPath = "catalog-lock.json"
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}

	catBytes, err := os.ReadFile(opts.CatalogPath)
	if err != nil {
		return Result{}, fmt.Errorf("reading %s: %w", opts.CatalogPath, err)
	}
	cat, err := Load(catBytes)
	if err != nil {
		return Result{}, err
	}
	if err := Validate(cat); err != nil {
		return Result{}, err
	}

	priorLock, err := loadLockOrEmpty(opts.LockPath)
	if err != nil {
		return Result{}, err
	}
	priorByName := indexLock(priorLock)

	// Drop indexer-managed rows (no source-pin fields) before further
	// processing — sync has nothing to fetch or push for them. Then apply
	// the optional --only name filter on the remaining vendor rows.
	entries := filterEntries(filterVendorEntries(cat.Skills), opts.Only)
	total := len(entries)

	// Pre-seed results so the order matches catalog order regardless of
	// goroutine completion order.
	results := make([]EntryResult, total)
	var mu sync.Mutex // guards lockfile-related state if needed

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(opts.Concurrency)

	for i, entry := range entries {
		i, entry := i, entry
		g.Go(func() error {
			res := runOne(gctx, i, total, entry, priorByName, opts, fet, lic, push)
			mu.Lock()
			results[i] = res
			mu.Unlock()
			if opts.OnTelemetry != nil {
				opts.OnTelemetry(res)
			}
			return nil // never bubble up; failures are recorded in results
		})
	}
	_ = g.Wait() // err is always nil because runOne swallows errors into results

	// Lockfile reconciliation: merge synced results in, keep prior lock
	// entries for failed/skipped names so the lock never regresses.
	if !opts.DryRun {
		nextLock := mergeLock(priorByName, results, entries, opts.Now())
		if err := WriteLockAtomic(opts.LockPath, nextLock); err != nil {
			return Result{Entries: results}, fmt.Errorf("writing %s: %w", opts.LockPath, err)
		}
	}

	return Result{Entries: results}, nil
}

// runOne processes a single catalog entry to a terminal EntryResult.
// All errors are captured into result.Err; nothing propagates to the
// caller goroutine.
func runOne(ctx context.Context, index, total int, entry Entry, priorByName map[string]LockEntry, opts Opts, fet Fetcher, lic LicenseReader, push Pusher) EntryResult {
	emit := func(stage, detail string, commit, digest string, err error) {
		if opts.OnEntry == nil {
			return
		}
		opts.OnEntry(EntryStatus{
			Index: index + 1, Total: total,
			Name:   entry.Name,
			Stage:  stage,
			Commit: commit,
			Digest: digest,
			Err:    err,
			Detail: detail,
		})
	}

	emit("queued", "", entry.Commit, "", nil)

	// Skip when the lockfile already reflects this exact commit.
	if prior, ok := priorByName[entry.Name]; ok && prior.Commit == entry.Commit {
		emit("skipped", fmt.Sprintf("already at %s", shortSHA(entry.Commit)), entry.Commit, prior.Digest, nil)
		return EntryResult{
			Name: entry.Name, Outcome: OutcomeSkipped,
			Commit: entry.Commit, Digest: prior.Digest, Ref: prior.Ref, Tag: prior.Tag,
			UpstreamRepo: entry.Repo, InternalRef: entry.InternalRef,
		}
	}

	owner, repo := splitRepo(entry.Repo)

	// Clone into a temp dir; clean up regardless of outcome.
	tmp, err := os.MkdirTemp("", "skills-oci-catalog-sync-*")
	if err != nil {
		err = fmt.Errorf("creating temp dir: %w", err)
		emit("failed", "", entry.Commit, "", err)
		return EntryResult{Name: entry.Name, Outcome: OutcomeFailed, Commit: entry.Commit, Tag: entry.Version, UpstreamRepo: entry.Repo, InternalRef: entry.InternalRef, Err: err}
	}
	defer os.RemoveAll(tmp)

	emit("cloning", "", entry.Commit, "", nil)
	if err := fet.Fetch(ctx, owner, repo, entry.Subpath, entry.Commit, tmp); err != nil {
		err = fmt.Errorf("fetching %s@%s: %w", entry.Repo, entry.Commit, err)
		emit("failed", "", entry.Commit, "", err)
		return EntryResult{Name: entry.Name, Outcome: OutcomeFailed, Commit: entry.Commit, Tag: entry.Version, UpstreamRepo: entry.Repo, InternalRef: entry.InternalRef, Err: err}
	}

	skillDir := filepath.Join(tmp, filepath.FromSlash(entry.Subpath))
	license, err := lic.ReadLicense(skillDir)
	if err != nil {
		err = fmt.Errorf("reading upstream SKILL.md for %s: %w", entry.Name, err)
		emit("failed", "", entry.Commit, "", err)
		return EntryResult{Name: entry.Name, Outcome: OutcomeFailed, Commit: entry.Commit, Tag: entry.Version, UpstreamRepo: entry.Repo, InternalRef: entry.InternalRef, Err: err}
	}
	if license == "" && !opts.AllowMissingLicense {
		err := fmt.Errorf("entry %q: upstream SKILL.md missing required 'license' field", entry.Name)
		emit("failed", "", entry.Commit, "", err)
		return EntryResult{Name: entry.Name, Outcome: OutcomeFailed, Commit: entry.Commit, Tag: entry.Version, UpstreamRepo: entry.Repo, InternalRef: entry.InternalRef, Err: err}
	}

	// DryRun bails before push so the registry is not contacted.
	if opts.DryRun {
		emit("done", "dry-run", entry.Commit, "", nil)
		return EntryResult{
			Name: entry.Name, Outcome: OutcomeSynced,
			Commit: entry.Commit, Tag: entry.Version,
			UpstreamRepo: entry.Repo, InternalRef: entry.InternalRef,
		}
	}

	annotations := map[string]string{}
	if opts.EntryAnnotations != nil {
		for k, v := range opts.EntryAnnotations(entry) {
			annotations[k] = v
		}
	}
	emit("pushing", "", entry.Commit, "", nil)
	digest, err := push.Push(ctx, PushInput{
		InternalRef:      entry.InternalRef,
		Tag:              entry.Version,
		SkillDir:         skillDir,
		PlainHTTP:        opts.PlainHTTP,
		ExtraAnnotations: annotations,
	})
	if err != nil {
		err = fmt.Errorf("pushing %s:%s: %w", entry.InternalRef, entry.Version, err)
		emit("failed", "", entry.Commit, "", err)
		return EntryResult{Name: entry.Name, Outcome: OutcomeFailed, Commit: entry.Commit, Tag: entry.Version, UpstreamRepo: entry.Repo, InternalRef: entry.InternalRef, Err: err}
	}

	ref := fmt.Sprintf("%s:%s@%s", entry.InternalRef, entry.Version, digest)
	emit("done", shortSHA(digest), entry.Commit, digest, nil)
	return EntryResult{
		Name: entry.Name, Outcome: OutcomeSynced,
		Commit: entry.Commit, Digest: digest, Ref: ref, Tag: entry.Version,
		UpstreamRepo: entry.Repo, InternalRef: entry.InternalRef,
	}
}

func loadLockOrEmpty(path string) (Lock, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Lock{LockfileVersion: 1}, nil
		}
		return Lock{}, fmt.Errorf("reading %s: %w", path, err)
	}
	return LoadLock(data)
}

// filterVendorEntries returns only the entries that carry the full
// source-pin set. Indexer-managed rows (no source-pin) are silently
// dropped because there's nothing for sync to fetch or push for them —
// the artifact they describe already exists in the registry.
func filterVendorEntries(entries []Entry) []Entry {
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if HasSourcePin(e) {
			out = append(out, e)
		}
	}
	return out
}

func filterEntries(entries []Entry, only []string) []Entry {
	if len(only) == 0 {
		return entries
	}
	want := make(map[string]struct{}, len(only))
	for _, n := range only {
		want[n] = struct{}{}
	}
	out := make([]Entry, 0, len(only))
	for _, e := range entries {
		if _, ok := want[e.Name]; ok {
			out = append(out, e)
		}
	}
	return out
}

func mergeLock(priorByName map[string]LockEntry, results []EntryResult, entries []Entry, generatedAt time.Time) Lock {
	ts := generatedAt.UTC().Format(time.RFC3339)
	merged := make([]LockEntry, 0, len(results))
	covered := make(map[string]struct{}, len(results))

	for i, r := range results {
		entry := entries[i]
		covered[r.Name] = struct{}{}
		switch r.Outcome {
		case OutcomeSynced:
			merged = append(merged, LockEntry{
				Name:        r.Name,
				InternalRef: entry.InternalRef,
				Tag:         entry.Version,
				Commit:      r.Commit,
				Digest:      r.Digest,
				Ref:         r.Ref,
				SyncedAt:    ts,
			})
		case OutcomeSkipped:
			// Skipped entries keep their prior lock state byte-for-byte.
			if prior, ok := priorByName[r.Name]; ok {
				merged = append(merged, prior)
			}
		case OutcomeFailed:
			// Failed entries do NOT overwrite prior good state.
			if prior, ok := priorByName[r.Name]; ok {
				merged = append(merged, prior)
			}
		}
	}
	// Carry over prior lock entries for names that are still in the
	// catalog but were not part of this run (e.g. filtered out by --only).
	for _, e := range entries {
		if _, done := covered[e.Name]; done {
			continue
		}
		if prior, ok := priorByName[e.Name]; ok {
			merged = append(merged, prior)
		}
	}

	return Lock{
		LockfileVersion: 1,
		GeneratedAt:     ts,
		Skills:          merged,
	}
}

func splitRepo(slug string) (owner, repo string) {
	for i := 0; i < len(slug); i++ {
		if slug[i] == '/' {
			return slug[:i], slug[i+1:]
		}
	}
	return slug, ""
}

func shortSHA(s string) string {
	if len(s) < 8 {
		return s
	}
	return s[:8]
}
