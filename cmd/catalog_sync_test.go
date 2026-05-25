package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/salaboy/skills-oci/pkg/catalog"
	"github.com/salaboy/skills-oci/pkg/config"
)

// updateGolden regenerates testdata/*.golden files when set. Run with
//
//	go test -run TestRunCatalogSync_PlainOutputGolden -update
//
// to seed the file from current stdout; subsequent runs assert byte-equality.
var updateGolden = flag.Bool("update", false, "regenerate golden files in testdata/")

// syncFakeFetcher records the (owner, repo, subpath, commit) tuples it was
// called with and can be configured to: (a) fail per-commit, (b) gate every
// call on a shared semaphore (so concurrency-limit tests can observe
// at-most-N-in-flight), and (c) sleep briefly inside the gate hold so the
// peak count is observable even on fast machines.
//
// It is named with a sync* prefix to avoid collision with the fakeFetcher
// in cmd/catalog_add_test.go.
type syncFakeFetcher struct {
	mu           sync.Mutex
	calls        []syncFetchCall
	failByCommit map[string]error

	// gate, when non-nil, is used to hold every fetch in flight. Workers
	// acquire one slot and release it on return. With a buffered channel
	// of size N, tests can observe peak in-flight ≤ N.
	gate chan struct{}

	inflight     atomic.Int32
	peakInflight atomic.Int32

	// holdFor, when non-zero, sleeps inside the held slot so the peak
	// in-flight count is observable across multiple goroutines.
	holdFor time.Duration
}

type syncFetchCall struct {
	Owner   string
	Repo    string
	Subpath string
	Commit  string
}

func (f *syncFakeFetcher) Fetch(_ context.Context, owner, repo, subpath, commit, dst string) error {
	f.mu.Lock()
	f.calls = append(f.calls, syncFetchCall{owner, repo, subpath, commit})
	f.mu.Unlock()

	cur := f.inflight.Add(1)
	defer f.inflight.Add(-1)
	for {
		peak := f.peakInflight.Load()
		if cur <= peak || f.peakInflight.CompareAndSwap(peak, cur) {
			break
		}
	}

	if f.gate != nil {
		f.gate <- struct{}{}
		if f.holdFor > 0 {
			time.Sleep(f.holdFor)
		}
		<-f.gate
	}

	if err, ok := f.failByCommit[commit]; ok {
		return err
	}

	// Drop a SKILL.md into the fetched subpath so license read can succeed.
	subpathDir := filepath.Join(dst, filepath.FromSlash(subpath))
	if err := os.MkdirAll(subpathDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(subpathDir, "SKILL.md"),
		[]byte("---\nname: fake\nversion: 1.0.0\nlicense: Apache-2.0\n---\nfake body\n"), 0o644)
}

func (f *syncFakeFetcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *syncFakeFetcher) commits() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		out = append(out, c.Commit)
	}
	return out
}

// syncFakeLicenseReader returns a canned license string. When defaultLicense
// is empty (and byCommit yields nothing), it returns ("", nil), which the
// orchestrator interprets as "no license declared".
type syncFakeLicenseReader struct {
	defaultLicense string
	byCommit       map[string]string
}

func (r syncFakeLicenseReader) ReadLicense(skillDir string) (string, error) {
	for sha, lic := range r.byCommit {
		if len(sha) >= 6 && strings.Contains(skillDir, sha[:6]) {
			return lic, nil
		}
	}
	return r.defaultLicense, nil
}

// syncFakePusher records every push, returns canned digests, and can fail
// per-tag. failTags maps a tag to the error to return for that push.
type syncFakePusher struct {
	mu          sync.Mutex
	calls       []catalog.PushInput
	digestByTag map[string]string
	failTags    map[string]error
}

func (p *syncFakePusher) Push(_ context.Context, in catalog.PushInput) (string, error) {
	p.mu.Lock()
	p.calls = append(p.calls, in)
	p.mu.Unlock()

	if err, ok := p.failTags[in.Tag]; ok {
		return "", err
	}
	if d, ok := p.digestByTag[in.Tag]; ok {
		return d, nil
	}
	return "sha256:abc" + in.Tag, nil
}

func (p *syncFakePusher) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

// fixedFixtureTime is the canonical timestamp used across sync-test
// catalogs so writes are deterministic and any v2 timestamps land on the
// same UTC value.
var fixedFixtureTime = time.Date(2026, 5, 22, 18, 30, 0, 0, time.UTC)

// writeTestCatalog writes a catalog.json with the given entries under dir
// and returns its path. Mirrors the helper in pkg/catalog/sync_test.go.
func writeTestCatalog(t *testing.T, dir string, entries ...catalog.Entry) string {
	t.Helper()
	path := filepath.Join(dir, "catalog.json")
	c := catalog.Catalog{
		SchemaVersion: 2,
		GeneratedAt:   fixedFixtureTime,
		Skills:        entries,
	}
	if err := catalog.WriteCatalogAtomic(path, c); err != nil {
		t.Fatalf("WriteCatalogAtomic: %v", err)
	}
	return path
}

// validSyncEntry returns a known-good catalog entry for tests.
func validSyncEntry() catalog.Entry {
	return catalog.Entry{
		Namespace:     "liatrio",
		Name:          "create-skill",
		LatestVersion: "1.0.0",
		UpdatedAt:     fixedFixtureTime,
		Status:        catalog.StatusPublished,
		Visibility:    catalog.VisibilityPublic,
		Repo:          "anthropics/skills",
		Subpath:       "skills/create-skill",
		Version:       "v1.0.0",
		Commit:        "bc6708cbbc37adb919157f04d31e601e68f4b9c2",
		InternalRef:   "ghcr.io/liatrio/skills/create-skill",
	}
}

// secondSyncEntry returns a distinct known-good entry for multi-entry tests.
func secondSyncEntry() catalog.Entry {
	return catalog.Entry{
		Namespace:     "liatrio",
		Name:          "other-skill",
		LatestVersion: "2.0.0",
		UpdatedAt:     fixedFixtureTime,
		Status:        catalog.StatusPublished,
		Visibility:    catalog.VisibilityPublic,
		Repo:          "anthropics/skills",
		Subpath:       "skills/other-skill",
		Version:       "v2.0.0",
		Commit:        "d4f8a2e97c5b21340eefaaaaaaaaaaaaaaaaaaaa",
		InternalRef:   "ghcr.io/liatrio/skills/other-skill",
	}
}

// fixedNow returns a deterministic time function suitable for syncOpts.
func fixedNow() func() time.Time {
	return func() time.Time { return time.Date(2026, 5, 22, 18, 30, 0, 0, time.UTC) }
}

// TestRunCatalogSync_HappyPathExit0 is the first RED test: a two-entry
// catalog where both fakes succeed should produce exit code 0, a written
// lockfile, and no error.
//
// It calls runCatalogSyncWithDeps, the dependency-injected core that does
// not yet exist. The build failure that results IS the intended RED state
// per CLAUDE.md's strict-TDD requirement.
func TestRunCatalogSync_HappyPathExit0(t *testing.T) {
	dir := t.TempDir()
	catPath := writeTestCatalog(t, dir, validSyncEntry(), secondSyncEntry())
	lockPath := filepath.Join(dir, "catalog-lock.json")

	var stdout strings.Builder
	opts := syncOpts{
		CatalogPath:         catPath,
		LockPath:            lockPath,
		Concurrency:         1, // deterministic ordering for the golden test pattern
		AllowMissingLicense: false,
		Now:                 fixedNow(),
	}
	fet := &syncFakeFetcher{}
	lic := syncFakeLicenseReader{defaultLicense: "Apache-2.0"}
	push := &syncFakePusher{}

	code, err := runCatalogSyncWithDeps(context.Background(), &stdout, opts, fet, lic, push, nil, "test-cli")
	if err != nil {
		t.Fatalf("runCatalogSyncWithDeps: %v", err)
	}
	if code != syncExitOK {
		t.Errorf("exit code = %d, want %d (syncExitOK)", code, syncExitOK)
	}

	// Lockfile written.
	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("lockfile not written: %v", err)
	}
	body, _ := os.ReadFile(lockPath)
	lock, err := catalog.LoadLock(body)
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	if len(lock.Skills) != 2 {
		t.Errorf("lock entries = %d, want 2", len(lock.Skills))
	}

	// Pusher saw both entries.
	if push.callCount() != 2 {
		t.Errorf("pusher calls = %d, want 2", push.callCount())
	}
}

// TestRunCatalogSync_FailureExit1: when one entry's fetcher fails, the
// orchestrator records the failure but continues with the other entry,
// the lockfile is still written, and the prior good lock state for the
// failed entry is preserved verbatim. Exit code is 1.
func TestRunCatalogSync_FailureExit1(t *testing.T) {
	dir := t.TempDir()
	e1, e2 := validSyncEntry(), secondSyncEntry()
	catPath := writeTestCatalog(t, dir, e1, e2)
	lockPath := filepath.Join(dir, "catalog-lock.json")

	// Pre-seed a lockfile with a GOOD prior entry for e1 at an older commit.
	priorE1 := catalog.LockEntry{
		Name:        e1.Name,
		InternalRef: e1.InternalRef,
		Tag:         "v0.9.0",
		Commit:      "00000000aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Digest:      "sha256:priorgood",
		Ref:         e1.InternalRef + ":v0.9.0@sha256:priorgood",
		SyncedAt:    "2026-04-01T00:00:00Z",
	}
	if err := catalog.WriteLockAtomic(lockPath, catalog.Lock{
		LockfileVersion: 1,
		GeneratedAt:     "2026-04-01T00:00:00Z",
		Skills:          []catalog.LockEntry{priorE1},
	}); err != nil {
		t.Fatalf("seed lockfile: %v", err)
	}

	var stdout strings.Builder
	opts := syncOpts{
		CatalogPath: catPath,
		LockPath:    lockPath,
		Concurrency: 1,
		Now:         fixedNow(),
	}
	fet := &syncFakeFetcher{
		failByCommit: map[string]error{e1.Commit: errors.New("upstream timeout")},
	}
	lic := syncFakeLicenseReader{defaultLicense: "Apache-2.0"}
	push := &syncFakePusher{}

	code, err := runCatalogSyncWithDeps(context.Background(), &stdout, opts, fet, lic, push, nil, "test-cli")
	if err != nil {
		t.Fatalf("runCatalogSyncWithDeps returned unexpected error: %v", err)
	}
	if code != syncExitEntryFail {
		t.Errorf("exit code = %d, want %d (syncExitEntryFail)", code, syncExitEntryFail)
	}

	// Lockfile still written; both entries present.
	body, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("ReadFile lockfile: %v", err)
	}
	lock, err := catalog.LoadLock(body)
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	if len(lock.Skills) != 2 {
		t.Fatalf("lock entries = %d, want 2", len(lock.Skills))
	}

	// Failed entry's prior good state is preserved verbatim.
	var gotE1 *catalog.LockEntry
	for i := range lock.Skills {
		if lock.Skills[i].Name == e1.Name {
			gotE1 = &lock.Skills[i]
			break
		}
	}
	if gotE1 == nil {
		t.Fatalf("e1 missing from lockfile")
	}
	if gotE1.Digest != "sha256:priorgood" {
		t.Errorf("failed entry lock state was overwritten: digest=%q want %q", gotE1.Digest, "sha256:priorgood")
	}
	if gotE1.Commit != "00000000aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("failed entry commit was overwritten: %q", gotE1.Commit)
	}
}

// TestRunCatalogSync_LockWriteFailureExit2: forcing the lockfile write to
// fail (chmod 0500 on the parent directory) produces exit code 2 — the
// "registry is ahead of the lockfile, manual reconciliation required"
// state from the data contract. The non-nil error is also propagated to
// the caller so the wrapper can surface it on stderr.
func TestRunCatalogSync_LockWriteFailureExit2(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod 0500 does not reliably block writes when running as root (some CI containers)")
	}

	dir := t.TempDir()
	catPath := writeTestCatalog(t, dir, validSyncEntry())

	// Create a read-only subdirectory; place the lockfile inside it.
	roDir := filepath.Join(dir, "ro")
	if err := os.Mkdir(roDir, 0o500); err != nil {
		t.Fatalf("Mkdir ro: %v", err)
	}
	// Restore mode so t.TempDir cleanup can recurse in.
	t.Cleanup(func() { _ = os.Chmod(roDir, 0o700) })
	lockPath := filepath.Join(roDir, "catalog-lock.json")

	var stdout strings.Builder
	opts := syncOpts{
		CatalogPath: catPath,
		LockPath:    lockPath,
		Concurrency: 1,
		Now:         fixedNow(),
	}
	fet := &syncFakeFetcher{}
	lic := syncFakeLicenseReader{defaultLicense: "Apache-2.0"}
	push := &syncFakePusher{}

	code, err := runCatalogSyncWithDeps(context.Background(), &stdout, opts, fet, lic, push, nil, "test-cli")
	if err == nil {
		t.Fatal("expected an error for lockfile-write failure, got nil")
	}
	if code != syncExitLockFail {
		t.Errorf("exit code = %d, want %d (syncExitLockFail)", code, syncExitLockFail)
	}
	if !strings.Contains(err.Error(), "writing") {
		t.Errorf("error %q lacks 'writing' context", err.Error())
	}
}

// TestRunCatalogSync_DryRunNoLockWritten: with DryRun set, the pusher is
// never invoked, the lockfile is not created, and exit code is 0.
func TestRunCatalogSync_DryRunNoLockWritten(t *testing.T) {
	dir := t.TempDir()
	catPath := writeTestCatalog(t, dir, validSyncEntry(), secondSyncEntry())
	lockPath := filepath.Join(dir, "catalog-lock.json")

	var stdout strings.Builder
	opts := syncOpts{
		CatalogPath: catPath,
		LockPath:    lockPath,
		Concurrency: 1,
		DryRun:      true,
		Now:         fixedNow(),
	}
	fet := &syncFakeFetcher{}
	lic := syncFakeLicenseReader{defaultLicense: "Apache-2.0"}
	push := &syncFakePusher{}

	code, err := runCatalogSyncWithDeps(context.Background(), &stdout, opts, fet, lic, push, nil, "test-cli")
	if err != nil {
		t.Fatalf("runCatalogSyncWithDeps: %v", err)
	}
	if code != syncExitOK {
		t.Errorf("exit code = %d, want %d (syncExitOK)", code, syncExitOK)
	}
	if push.callCount() != 0 {
		t.Errorf("pusher called %d times during dry-run, want 0", push.callCount())
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Errorf("lockfile exists after dry-run, stat err = %v", err)
	}
}

// TestRunCatalogSync_OnlyFilterRespected: with --only set to a subset of
// entries, the fetcher and pusher only see those entries; unnamed entries
// are skipped entirely.
func TestRunCatalogSync_OnlyFilterRespected(t *testing.T) {
	dir := t.TempDir()
	e1 := validSyncEntry()
	e2 := secondSyncEntry()
	e3 := catalog.Entry{
		Namespace:     "liatrio",
		Name:          "third-skill",
		LatestVersion: "3.0.0",
		UpdatedAt:     fixedFixtureTime,
		Status:        catalog.StatusPublished,
		Visibility:    catalog.VisibilityPublic,
		Repo:          "anthropics/skills",
		Subpath:       "skills/third-skill",
		Version:       "v3.0.0",
		Commit:        "f1e2d3c4b5a6cccccccccccccccccccccccccccc",
		InternalRef:   "ghcr.io/liatrio/skills/third-skill",
	}
	catPath := writeTestCatalog(t, dir, e1, e2, e3)
	lockPath := filepath.Join(dir, "catalog-lock.json")

	var stdout strings.Builder
	opts := syncOpts{
		CatalogPath: catPath,
		LockPath:    lockPath,
		Concurrency: 1,
		Only:        []string{e1.Name, e3.Name},
		Now:         fixedNow(),
	}
	fet := &syncFakeFetcher{}
	lic := syncFakeLicenseReader{defaultLicense: "Apache-2.0"}
	push := &syncFakePusher{}

	code, err := runCatalogSyncWithDeps(context.Background(), &stdout, opts, fet, lic, push, nil, "test-cli")
	if err != nil {
		t.Fatalf("runCatalogSyncWithDeps: %v", err)
	}
	if code != syncExitOK {
		t.Errorf("exit code = %d, want %d (syncExitOK)", code, syncExitOK)
	}

	// Fetcher saw only e1 and e3.
	commits := fet.commits()
	if len(commits) != 2 {
		t.Fatalf("fetcher saw %d commits, want 2: %v", len(commits), commits)
	}
	gotCommits := map[string]bool{commits[0]: true, commits[1]: true}
	if !gotCommits[e1.Commit] || !gotCommits[e3.Commit] {
		t.Errorf("fetcher commits = %v, want subset {%s, %s}", commits, e1.Commit, e3.Commit)
	}
	if gotCommits[e2.Commit] {
		t.Errorf("fetcher saw e2 (unnamed entry) commit %s", e2.Commit)
	}

	// Pusher likewise saw only those two.
	if push.callCount() != 2 {
		t.Errorf("pusher calls = %d, want 2", push.callCount())
	}
}

// TestRunCatalogSync_ConcurrencyFromConfig has two parts:
//
//  1. parseSyncOpts resolves project-config concurrency when no
//     --concurrency flag is set.
//  2. The orchestrator actually honors the resolved concurrency: with an
//     8-entry catalog and concurrency=2, the gated fetcher's peak
//     in-flight count is exactly 2 (proves both bounds: cap respected,
//     parallelism actually achieved).
func TestRunCatalogSync_ConcurrencyFromConfig(t *testing.T) {
	t.Run("parseSyncOpts_picks_config", func(t *testing.T) {
		cmd := newCatalogSyncCmd()
		cfg := config.Config{Catalog: config.CatalogConfig{Concurrency: 2}}
		opts := parseSyncOpts(cmd, cfg)
		if opts.Concurrency != 2 {
			t.Errorf("opts.Concurrency = %d, want 2", opts.Concurrency)
		}
	})

	t.Run("orchestrator_honors_cap", func(t *testing.T) {
		dir := t.TempDir()
		entries := make([]catalog.Entry, 0, 8)
		for i := 0; i < 8; i++ {
			e := validSyncEntry()
			e.Name = fmt.Sprintf("entry-%d", i)
			e.Commit = fmt.Sprintf("%040d", i)
			e.Version = fmt.Sprintf("v%d.0.0", i)
			e.InternalRef = fmt.Sprintf("ghcr.io/liatrio/skills/entry-%d", i)
			e.Subpath = fmt.Sprintf("skills/entry-%d", i)
			entries = append(entries, e)
		}
		catPath := writeTestCatalog(t, dir, entries...)
		lockPath := filepath.Join(dir, "catalog-lock.json")

		// gate has capacity matching the concurrency bound. holdFor keeps
		// each fetch in flight long enough for parallel ones to overlap.
		gate := make(chan struct{}, 2)
		fet := &syncFakeFetcher{
			gate:    gate,
			holdFor: 10 * time.Millisecond,
		}
		lic := syncFakeLicenseReader{defaultLicense: "Apache-2.0"}
		push := &syncFakePusher{}

		var stdout strings.Builder
		opts := syncOpts{
			CatalogPath: catPath,
			LockPath:    lockPath,
			Concurrency: 2,
			Now:         fixedNow(),
		}
		code, err := runCatalogSyncWithDeps(context.Background(), &stdout, opts, fet, lic, push, nil, "test-cli")
		if err != nil {
			t.Fatalf("runCatalogSyncWithDeps: %v", err)
		}
		if code != syncExitOK {
			t.Errorf("exit code = %d, want syncExitOK", code)
		}

		peak := fet.peakInflight.Load()
		if peak > 2 {
			t.Errorf("peak inflight = %d, want ≤ 2", peak)
		}
		if peak < 2 {
			t.Errorf("peak inflight = %d, want exactly 2 (parallelism not achieved)", peak)
		}
		if fet.callCount() != 8 {
			t.Errorf("fetcher calls = %d, want 8", fet.callCount())
		}
	})
}

// TestRunCatalogSync_AllowMissingLicenseFromConfig has three parts:
//
//  1. parseSyncOpts resolves project-config allow_missing_license when no
//     --allow-missing-license flag is set.
//  2. With allow=true, an entry whose upstream SKILL.md has no license
//     field syncs successfully (exit 0).
//  3. With allow=false (the default), the same fixture fails (exit 1).
func TestRunCatalogSync_AllowMissingLicenseFromConfig(t *testing.T) {
	t.Run("parseSyncOpts_picks_config", func(t *testing.T) {
		cmd := newCatalogSyncCmd()
		cfg := config.Config{Catalog: config.CatalogConfig{AllowMissingLicense: true}}
		opts := parseSyncOpts(cmd, cfg)
		if !opts.AllowMissingLicense {
			t.Error("opts.AllowMissingLicense = false, want true")
		}
	})

	setup := func(t *testing.T, allow bool) (syncExitCode, error) {
		t.Helper()
		dir := t.TempDir()
		catPath := writeTestCatalog(t, dir, validSyncEntry())
		lockPath := filepath.Join(dir, "catalog-lock.json")

		var stdout strings.Builder
		opts := syncOpts{
			CatalogPath:         catPath,
			LockPath:            lockPath,
			Concurrency:         1,
			AllowMissingLicense: allow,
			Now:                 fixedNow(),
		}
		fet := &syncFakeFetcher{}
		lic := syncFakeLicenseReader{defaultLicense: ""} // empty license
		push := &syncFakePusher{}
		return runCatalogSyncWithDeps(context.Background(), &stdout, opts, fet, lic, push, nil, "test-cli")
	}

	t.Run("allow_true_succeeds", func(t *testing.T) {
		code, err := setup(t, true)
		if err != nil {
			t.Fatalf("runCatalogSyncWithDeps: %v", err)
		}
		if code != syncExitOK {
			t.Errorf("exit code = %d, want syncExitOK (allow=true should let empty-license entry pass)", code)
		}
	})

	t.Run("allow_false_fails", func(t *testing.T) {
		code, err := setup(t, false)
		if err != nil {
			t.Fatalf("runCatalogSyncWithDeps: %v", err)
		}
		if code != syncExitEntryFail {
			t.Errorf("exit code = %d, want syncExitEntryFail (allow=false should reject empty-license entry)", code)
		}
	})
}

// TestRunCatalogSync_PlainOutputGolden captures stdout from a deterministic
// 2-entry happy-path run and compares it byte-for-byte against the
// committed golden file at cmd/testdata/catalog-sync-plain.golden.
//
// Run with `-update` (positioned AFTER the package path so go test
// passes it through to the test binary) to regenerate the golden after
// intentional format changes:
//
//	go test ./cmd/ -run TestRunCatalogSync_PlainOutputGolden -update
func TestRunCatalogSync_PlainOutputGolden(t *testing.T) {
	dir := t.TempDir()
	catPath := writeTestCatalog(t, dir, validSyncEntry(), secondSyncEntry())
	lockPath := filepath.Join(dir, "catalog-lock.json")

	var stdout strings.Builder
	opts := syncOpts{
		CatalogPath: catPath,
		LockPath:    lockPath,
		Concurrency: 1, // serial workers give deterministic line ordering
		Now:         fixedNow(),
	}
	fet := &syncFakeFetcher{}
	lic := syncFakeLicenseReader{defaultLicense: "Apache-2.0"}
	push := &syncFakePusher{
		digestByTag: map[string]string{
			"v1.0.0": "sha256:1111111111111111111111111111111111111111111111111111111111111111",
			"v2.0.0": "sha256:2222222222222222222222222222222222222222222222222222222222222222",
		},
	}

	code, err := runCatalogSyncWithDeps(context.Background(), &stdout, opts, fet, lic, push, nil, "test-cli")
	if err != nil {
		t.Fatalf("runCatalogSyncWithDeps: %v", err)
	}
	if code != syncExitOK {
		t.Errorf("exit code = %d, want syncExitOK", code)
	}

	got := stdout.String()
	goldenPath := filepath.Join("testdata", "catalog-sync-plain.golden")

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("MkdirAll testdata: %v", err)
		}
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", goldenPath, err)
		}
		t.Logf("updated golden: %s (%d bytes)", goldenPath, len(got))
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("ReadFile %s: %v (run with -update to seed)", goldenPath, err)
	}
	if got != string(want) {
		t.Errorf("plain output does not match golden.\n--- got ---\n%s\n--- want ---\n%s", got, string(want))
	}
}
