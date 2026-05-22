package catalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeFetcher writes a stub SKILL.md (or doesn't) into the expected
// subpath. Per-entry behavior keyed by entry name lets a single fake
// drive mixed-outcome tests.
type fakeFetcher struct {
	writeSkillMD  bool
	skillMDByName map[string]string // override per-entry; empty value = no SKILL.md
	failNames     map[string]error  // names that should fail at fetch time
	calls         atomic.Int64
}

func (f *fakeFetcher) Fetch(_ context.Context, owner, repo, subpath, commit, dst string) error {
	f.calls.Add(1)
	name := commit // we key off commit so per-call behavior can differ
	_ = owner
	_ = repo
	_ = name
	if err := f.failForCommit(commit); err != nil {
		return err
	}
	subpathDir := filepath.Join(dst, filepath.FromSlash(subpath))
	if err := os.MkdirAll(subpathDir, 0o755); err != nil {
		return err
	}
	body, ok := f.skillMDByName[commit]
	if !ok {
		if !f.writeSkillMD {
			return nil // subpath dir exists but no SKILL.md
		}
		body = "---\nname: fake\nversion: 1.0.0\nlicense: Apache-2.0\n---\nfake body\n"
	}
	return os.WriteFile(filepath.Join(subpathDir, "SKILL.md"), []byte(body), 0o644)
}

func (f *fakeFetcher) failForCommit(commit string) error {
	if err, ok := f.failNames[commit]; ok {
		return err
	}
	return nil
}

// fakeLicenseReader returns canned license values per skillDir.
type fakeLicenseReader struct {
	defaultLicense string
	byCommit       map[string]string // map sha-prefix → license override
}

func (f fakeLicenseReader) ReadLicense(skillDir string) (string, error) {
	// Map skillDir back to a commit by walking the parent's name. Tests
	// that exercise multiple entries use commits as identity keys.
	for sha, lic := range f.byCommit {
		if strings.Contains(skillDir, sha[:6]) {
			return lic, nil
		}
	}
	return f.defaultLicense, nil
}

// fakePusher records every push and returns canned digests. It can also
// gate progress on a channel so concurrency-limit tests can observe at-
// most-N-in-flight.
type fakePusher struct {
	mu           sync.Mutex
	calls        []PushInput
	inflight     atomic.Int32
	peakInflight atomic.Int32

	// Optional gating: when gate is non-nil, every push reads one value
	// from gate before returning so the test can hold workers in flight.
	gate chan struct{}

	digestByTag map[string]string // override per tag; default = sha256:abc<tag>
	failTags    map[string]error
}

func (p *fakePusher) Push(_ context.Context, in PushInput) (string, error) {
	p.mu.Lock()
	p.calls = append(p.calls, in)
	p.mu.Unlock()

	cur := p.inflight.Add(1)
	defer p.inflight.Add(-1)
	for {
		peak := p.peakInflight.Load()
		if cur <= peak || p.peakInflight.CompareAndSwap(peak, cur) {
			break
		}
	}

	if p.gate != nil {
		<-p.gate
	}

	if err, ok := p.failTags[in.Tag]; ok {
		return "", err
	}
	if d, ok := p.digestByTag[in.Tag]; ok {
		return d, nil
	}
	return "sha256:abc" + in.Tag, nil
}

// catalogFile writes a catalog.json file under dir and returns its path.
func writeCatalog(t *testing.T, dir string, entries ...Entry) string {
	t.Helper()
	path := filepath.Join(dir, "catalog.json")
	if err := WriteCatalogAtomic(path, Catalog{SchemaVersion: 1, Skills: entries}); err != nil {
		t.Fatalf("WriteCatalogAtomic: %v", err)
	}
	return path
}

func validSyncEntry() Entry {
	return Entry{
		Name:        "create-skill",
		Repo:        "anthropics/skills",
		Subpath:     "skills/create-skill",
		Version:     "v1.0.0",
		Commit:      "bc6708cbbc37adb919157f04d31e601e68f4b9c2",
		InternalRef: "ghcr.io/liatrio/skills/create-skill",
	}
}

func secondSyncEntry() Entry {
	return Entry{
		Name:        "other-skill",
		Repo:        "anthropics/skills",
		Subpath:     "skills/other-skill",
		Version:     "v2.0.0",
		Commit:      "d4f8a2e97c5b21340eefaaaaaaaaaaaaaaaaaaaa",
		InternalRef: "ghcr.io/liatrio/skills/other-skill",
	}
}

func TestSync_AllSucceed(t *testing.T) {
	dir := t.TempDir()
	catPath := writeCatalog(t, dir, validSyncEntry(), secondSyncEntry())
	lockPath := filepath.Join(dir, "catalog-lock.json")

	res, err := Sync(context.Background(), Opts{
		CatalogPath: catPath,
		LockPath:    lockPath,
		Now:         func() time.Time { return time.Date(2026, 5, 22, 18, 30, 0, 0, time.UTC) },
	}, &fakeFetcher{writeSkillMD: true}, fakeLicenseReader{defaultLicense: "Apache-2.0"}, &fakePusher{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	synced, failed, skipped := res.Counts()
	if synced != 2 || failed != 0 || skipped != 0 {
		t.Errorf("counts = (synced=%d failed=%d skipped=%d); want (2,0,0)", synced, failed, skipped)
	}

	// Lockfile contains both entries.
	body, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("ReadFile lock: %v", err)
	}
	lock, err := LoadLock(body)
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	if len(lock.Skills) != 2 {
		t.Errorf("lock entries = %d, want 2", len(lock.Skills))
	}
}

func TestSync_OneFailOthersSucceedAndPreservesPriorLock(t *testing.T) {
	dir := t.TempDir()
	e1, e2 := validSyncEntry(), secondSyncEntry()
	catPath := writeCatalog(t, dir, e1, e2)
	lockPath := filepath.Join(dir, "catalog-lock.json")

	// Pre-seed a lockfile with a GOOD entry for e2 at an older commit.
	priorE2 := LockEntry{
		Name:        e2.Name,
		InternalRef: e2.InternalRef,
		Tag:         "v1.9.0",
		Commit:      "00000000000000000000000000000000deadbeef",
		Digest:      "sha256:priorgood",
		Ref:         "ghcr.io/liatrio/skills/other-skill:v1.9.0@sha256:priorgood",
		SyncedAt:    "2026-04-01T00:00:00Z",
	}
	if err := WriteLockAtomic(lockPath, Lock{LockfileVersion: 1, GeneratedAt: "2026-04-01T00:00:00Z", Skills: []LockEntry{priorE2}}); err != nil {
		t.Fatalf("seed lock: %v", err)
	}

	pusher := &fakePusher{
		failTags: map[string]error{"v2.0.0": errors.New("registry timeout")},
	}

	res, err := Sync(context.Background(), Opts{
		CatalogPath: catPath,
		LockPath:    lockPath,
		Now:         func() time.Time { return time.Date(2026, 5, 22, 18, 30, 0, 0, time.UTC) },
	}, &fakeFetcher{writeSkillMD: true}, fakeLicenseReader{defaultLicense: "Apache-2.0"}, pusher)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	synced, failed, _ := res.Counts()
	if synced != 1 || failed != 1 {
		t.Errorf("counts = (synced=%d failed=%d); want (1,1)", synced, failed)
	}

	// Lockfile should have BOTH entries: e1 freshly synced, e2 still pointing
	// at its prior GOOD state — not overwritten with stale failure data.
	body, _ := os.ReadFile(lockPath)
	lock, _ := LoadLock(body)
	if len(lock.Skills) != 2 {
		t.Fatalf("lock entries = %d, want 2", len(lock.Skills))
	}
	for _, l := range lock.Skills {
		if l.Name == e2.Name && l.Digest != "sha256:priorgood" {
			t.Errorf("failed entry's prior lock state lost; got digest %q want %q", l.Digest, "sha256:priorgood")
		}
	}
}

func TestSync_SkipWhenLockMatchesCommit(t *testing.T) {
	dir := t.TempDir()
	entry := validSyncEntry()
	catPath := writeCatalog(t, dir, entry)
	lockPath := filepath.Join(dir, "catalog-lock.json")

	// Pre-seed lock with the SAME commit.
	if err := WriteLockAtomic(lockPath, Lock{LockfileVersion: 1, Skills: []LockEntry{{
		Name: entry.Name, InternalRef: entry.InternalRef, Tag: entry.Version,
		Commit: entry.Commit, Digest: "sha256:cached", Ref: "x", SyncedAt: "2026-01-01T00:00:00Z",
	}}}); err != nil {
		t.Fatalf("seed lock: %v", err)
	}

	pusher := &fakePusher{}
	fetcher := &fakeFetcher{writeSkillMD: true}
	res, err := Sync(context.Background(), Opts{
		CatalogPath: catPath, LockPath: lockPath,
	}, fetcher, fakeLicenseReader{}, pusher)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	_, _, skipped := res.Counts()
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}
	if len(pusher.calls) != 0 {
		t.Errorf("pusher called %d times for a skipped entry, want 0", len(pusher.calls))
	}
	if fetcher.calls.Load() != 0 {
		t.Errorf("fetcher called %d times for a skipped entry, want 0", fetcher.calls.Load())
	}
}

func TestSync_OnlyFilter(t *testing.T) {
	dir := t.TempDir()
	catPath := writeCatalog(t, dir, validSyncEntry(), secondSyncEntry())
	lockPath := filepath.Join(dir, "catalog-lock.json")

	res, err := Sync(context.Background(), Opts{
		CatalogPath: catPath, LockPath: lockPath,
		Only: []string{"other-skill"},
	}, &fakeFetcher{writeSkillMD: true}, fakeLicenseReader{defaultLicense: "Apache-2.0"}, &fakePusher{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(res.Entries) != 1 || res.Entries[0].Name != "other-skill" {
		t.Errorf("filtered result wrong: %+v", res.Entries)
	}
}

func TestSync_DryRunSkipsPushAndLockWrite(t *testing.T) {
	dir := t.TempDir()
	catPath := writeCatalog(t, dir, validSyncEntry())
	lockPath := filepath.Join(dir, "catalog-lock.json")

	pusher := &fakePusher{}
	_, err := Sync(context.Background(), Opts{
		CatalogPath: catPath, LockPath: lockPath, DryRun: true,
	}, &fakeFetcher{writeSkillMD: true}, fakeLicenseReader{defaultLicense: "Apache-2.0"}, pusher)
	if err != nil {
		t.Fatalf("Sync dry-run: %v", err)
	}
	if len(pusher.calls) != 0 {
		t.Errorf("pusher called during dry-run (%d times)", len(pusher.calls))
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Errorf("lockfile created during dry-run, got %v", err)
	}
}

func TestSync_ConcurrencyLimitHonored(t *testing.T) {
	dir := t.TempDir()
	entries := make([]Entry, 0, 8)
	for i := 0; i < 8; i++ {
		e := validSyncEntry()
		e.Name = fmt.Sprintf("entry-%d", i)
		e.Commit = fmt.Sprintf("%040d", i)
		e.Version = fmt.Sprintf("v%d.0.0", i)
		e.InternalRef = fmt.Sprintf("ghcr.io/x/skills/entry-%d", i)
		entries = append(entries, e)
	}
	catPath := writeCatalog(t, dir, entries...)
	lockPath := filepath.Join(dir, "catalog-lock.json")

	gate := make(chan struct{}, 8)
	pusher := &fakePusher{gate: gate}

	done := make(chan struct{})
	go func() {
		_, _ = Sync(context.Background(), Opts{
			CatalogPath: catPath, LockPath: lockPath,
			Concurrency: 2,
		}, &fakeFetcher{writeSkillMD: true}, fakeLicenseReader{defaultLicense: "Apache-2.0"}, pusher)
		close(done)
	}()

	// Let all 8 workers start by feeding the gate one token per push.
	for i := 0; i < 8; i++ {
		gate <- struct{}{}
	}
	<-done

	// With a concurrency limit of 2, peak inflight should never exceed 2.
	if peak := pusher.peakInflight.Load(); peak > 2 {
		t.Errorf("peak inflight = %d, want ≤ 2", peak)
	}
	if len(pusher.calls) != 8 {
		t.Errorf("pusher called %d times, want 8", len(pusher.calls))
	}
}

func TestSync_LicenseMissingFailsByDefault(t *testing.T) {
	dir := t.TempDir()
	entry := validSyncEntry()
	catPath := writeCatalog(t, dir, entry)
	lockPath := filepath.Join(dir, "catalog-lock.json")

	pusher := &fakePusher{}
	res, err := Sync(context.Background(), Opts{
		CatalogPath: catPath, LockPath: lockPath,
	}, &fakeFetcher{writeSkillMD: true}, fakeLicenseReader{defaultLicense: ""}, pusher)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if !res.AnyFailed() {
		t.Fatal("AnyFailed = false, want true (license missing should fail by default)")
	}
	if res.Entries[0].Err == nil || !strings.Contains(res.Entries[0].Err.Error(), "missing required 'license' field") {
		t.Errorf("expected 'missing required license field' error, got %v", res.Entries[0].Err)
	}
	if len(pusher.calls) != 0 {
		t.Errorf("pusher called for license-missing entry: %d times", len(pusher.calls))
	}
}

func TestSync_LicenseMissingWithFlagSucceedsAndOmitsAnnotation(t *testing.T) {
	dir := t.TempDir()
	entry := validSyncEntry()
	catPath := writeCatalog(t, dir, entry)
	lockPath := filepath.Join(dir, "catalog-lock.json")

	pusher := &fakePusher{}
	res, err := Sync(context.Background(), Opts{
		CatalogPath: catPath, LockPath: lockPath,
		AllowMissingLicense: true,
		EntryAnnotations: func(e Entry) map[string]string {
			return map[string]string{
				"org.opencontainers.image.source": "https://github.com/" + e.Repo + "/tree/" + e.Commit + "/" + e.Subpath,
			}
		},
	}, &fakeFetcher{writeSkillMD: true}, fakeLicenseReader{defaultLicense: ""}, pusher)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if synced, _, _ := res.Counts(); synced != 1 {
		t.Errorf("synced = %d, want 1", synced)
	}
	if len(pusher.calls) != 1 {
		t.Fatalf("pusher calls = %d, want 1", len(pusher.calls))
	}
	annotations := pusher.calls[0].ExtraAnnotations
	if _, has := annotations["org.opencontainers.image.licenses"]; has {
		t.Errorf("annotation map carries a license key for a license-missing entry: %v", annotations)
	}
	if _, has := annotations["org.opencontainers.image.source"]; !has {
		t.Errorf("annotation map missing org.opencontainers.image.source: %v", annotations)
	}
}

func TestSync_TelemetryCallbackFiresPerEntry(t *testing.T) {
	dir := t.TempDir()
	catPath := writeCatalog(t, dir, validSyncEntry(), secondSyncEntry())
	lockPath := filepath.Join(dir, "catalog-lock.json")

	var mu sync.Mutex
	got := make(map[string]Outcome)
	_, err := Sync(context.Background(), Opts{
		CatalogPath: catPath, LockPath: lockPath,
		OnTelemetry: func(r EntryResult) {
			mu.Lock()
			got[r.Name] = r.Outcome
			mu.Unlock()
		},
	}, &fakeFetcher{writeSkillMD: true}, fakeLicenseReader{defaultLicense: "Apache-2.0"}, &fakePusher{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got["create-skill"] != OutcomeSynced || got["other-skill"] != OutcomeSynced {
		t.Errorf("OnTelemetry didn't fire for both entries with outcome=synced: %v", got)
	}
}

func TestSync_FetcherFailureSurfaces(t *testing.T) {
	dir := t.TempDir()
	entry := validSyncEntry()
	catPath := writeCatalog(t, dir, entry)
	lockPath := filepath.Join(dir, "catalog-lock.json")

	fet := &fakeFetcher{
		failNames: map[string]error{entry.Commit: errors.New("simulated network failure")},
	}
	res, err := Sync(context.Background(), Opts{CatalogPath: catPath, LockPath: lockPath}, fet, fakeLicenseReader{defaultLicense: "Apache-2.0"}, &fakePusher{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !res.AnyFailed() {
		t.Error("fetcher failure should produce a failed entry")
	}
}

func TestSync_InvalidCatalogReturnsSetupError(t *testing.T) {
	dir := t.TempDir()
	// Write a catalog with a deliberately invalid commit so Validate rejects.
	bad := Entry{
		Name: "x", Repo: "x/y", Subpath: "s", Version: "v1.0.0",
		Commit: "not-a-sha", InternalRef: "ghcr.io/x/y",
	}
	path := filepath.Join(dir, "catalog.json")
	// Bypass WriteCatalogAtomic's Validate so we can land an invalid file.
	if err := os.WriteFile(path, []byte(`{"schemaVersion":1,"skills":[{"name":"x","repo":"x/y","subpath":"s","version":"v1.0.0","commit":"not-a-sha","internal_ref":"ghcr.io/x/y"}]}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_ = bad

	_, err := Sync(context.Background(), Opts{CatalogPath: path, LockPath: filepath.Join(dir, "lock.json")}, &fakeFetcher{}, fakeLicenseReader{}, &fakePusher{})
	if err == nil {
		t.Fatal("Sync accepted invalid catalog")
	}
	if !strings.Contains(err.Error(), "commit") {
		t.Errorf("error %q lacks 'commit' context", err.Error())
	}
}

func TestSync_MissingCatalogReturnsError(t *testing.T) {
	dir := t.TempDir()
	_, err := Sync(context.Background(), Opts{
		CatalogPath: filepath.Join(dir, "nonexistent.json"),
		LockPath:    filepath.Join(dir, "lock.json"),
	}, &fakeFetcher{}, fakeLicenseReader{}, &fakePusher{})
	if err == nil {
		t.Fatal("Sync accepted missing catalog.json")
	}
}
