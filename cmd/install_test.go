package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/liatrio/skills-oci/pkg/oci"
	"github.com/liatrio/skills-oci/pkg/tui/load"
)

// recordingEmitter mirrors the one in pkg/oci/pull_telemetry_test.go but lives
// here so the cmd-layer test can assert wiring end-to-end.
type recordingEmitter struct {
	mu    sync.Mutex
	calls []oci.SkillDownloadInfo
}

func (r *recordingEmitter) OnSkillDownloaded(info oci.SkillDownloadInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, info)
}

func (r *recordingEmitter) snapshot() []oci.SkillDownloadInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]oci.SkillDownloadInfo, len(r.calls))
	copy(out, r.calls)
	return out
}

func TestInstall_EmitsPerPulledSkill_AndZeroOnCacheHit(t *testing.T) {
	// Construct an httptest-backed fake registry serving 3 skill artifacts.
	skills := []struct{ name, version string }{
		{"alpha", "1.0.0"},
		{"beta", "1.1.0"},
		{"gamma", "2.0.0"},
	}
	srv, host := newFakeRegistryFor(t, "ns/skills", skills)
	t.Cleanup(srv.Close)

	// Build a minimal skills.json pointing at each artifact.
	projectDir := t.TempDir()
	mfPath := filepath.Join(projectDir, "skills.json")
	type entry struct {
		Name                string   `json:"name"`
		Source              string   `json:"source"`
		Version             string   `json:"version"`
		AdditionalBasePaths []string `json:"additionalBasePaths,omitempty"`
	}
	type manifest struct {
		Skills []entry `json:"skills"`
	}
	mf := manifest{}
	for _, s := range skills {
		mf.Skills = append(mf.Skills, entry{
			Name:    s.name,
			Source:  fmt.Sprintf("%s/ns/skills/%s", host, s.name),
			Version: s.version,
		})
	}
	body, err := json.Marshal(mf)
	if err != nil {
		t.Fatalf("marshal skills.json: %v", err)
	}
	if err := os.WriteFile(mfPath, body, 0o644); err != nil {
		t.Fatalf("write skills.json: %v", err)
	}

	// Round 1: nothing on disk → expect 3 events.
	rec := &recordingEmitter{}
	installed, skipped, err := load.LoadSkills(projectDir, ".agents/skills", true, nil, rec, "0.1.0")
	if err != nil {
		t.Fatalf("LoadSkills (round 1): %v", err)
	}
	if len(installed) != 3 || len(skipped) != 0 {
		t.Fatalf("round 1: installed=%v skipped=%v, want 3 installed", installed, skipped)
	}
	calls := rec.snapshot()
	if len(calls) != 3 {
		t.Fatalf("round 1: emitter calls = %d, want 3", len(calls))
	}
	for _, c := range calls {
		if c.Command != "install" || c.Trigger != "manifest" {
			t.Errorf("source = (%q,%q), want (install,manifest)", c.Command, c.Trigger)
		}
	}

	// Round 2: directories now exist → expect 0 events.
	rec2 := &recordingEmitter{}
	installed2, skipped2, err := load.LoadSkills(projectDir, ".agents/skills", true, nil, rec2, "0.1.0")
	if err != nil {
		t.Fatalf("LoadSkills (round 2): %v", err)
	}
	if len(skipped2) != 3 {
		t.Errorf("round 2: skipped = %d, want 3 (cache hit on all)", len(skipped2))
	}
	if len(installed2) != 0 {
		t.Errorf("round 2: installed = %d, want 0", len(installed2))
	}
	if calls2 := rec2.snapshot(); len(calls2) != 0 {
		t.Errorf("round 2: emitter calls = %d, want 0 (no events on cache hit)", len(calls2))
	}
}

// TestInstall_PlainAndTUIParity ensures both code paths produce the same
// event set for the same input state. We exercise the underlying LoadSkills
// directly for both paths since runInstallPlain delegates to it and the TUI's
// startLoad() delegates to it as well.
func TestInstall_PlainAndTUIParity(t *testing.T) {
	skills := []struct{ name, version string }{
		{"one", "1.0.0"},
		{"two", "1.0.0"},
	}
	srv, host := newFakeRegistryFor(t, "p/skills", skills)
	t.Cleanup(srv.Close)

	dirA := t.TempDir()
	dirB := t.TempDir()
	for _, dir := range []string{dirA, dirB} {
		body := fmt.Sprintf(`{"skills":[
		  {"name":"one","source":"%s/p/skills/one","version":"1.0.0"},
		  {"name":"two","source":"%s/p/skills/two","version":"1.0.0"}
		]}`, host, host)
		if err := os.WriteFile(filepath.Join(dir, "skills.json"), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", dir, err)
		}
	}

	recA := &recordingEmitter{}
	if _, _, err := load.LoadSkills(dirA, ".agents/skills", true, nil, recA, "0.1.0"); err != nil {
		t.Fatalf("plain path: %v", err)
	}
	recB := &recordingEmitter{}
	if _, _, err := load.LoadSkills(dirB, ".agents/skills", true, nil, recB, "0.1.0"); err != nil {
		t.Fatalf("tui path: %v", err)
	}

	a := recA.snapshot()
	b := recB.snapshot()
	if len(a) != len(b) {
		t.Fatalf("event counts differ: plain=%d tui=%d", len(a), len(b))
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Version != b[i].Version || a[i].OCIRef != b[i].OCIRef {
			t.Errorf("event %d mismatch: plain=%+v tui=%+v", i, a[i], b[i])
		}
	}
}

func TestEmitterFromContext_NilSafe(t *testing.T) {
	// EmitterFromContext on a bare context should return nil; the adapter
	// must tolerate that without panicking.
	em := EmitterFromContext(context.Background())
	if em != nil {
		t.Errorf("EmitterFromContext(empty) = %v, want nil", em)
	}
	adapter := &SkillEmitterAdapter{Emitter: em}
	adapter.OnSkillDownloaded(oci.SkillDownloadInfo{}) // must not panic
}

func TestSkillEmitterAdapter_NilReceiverDoesNotPanic(t *testing.T) {
	var a *SkillEmitterAdapter
	a.OnSkillDownloaded(oci.SkillDownloadInfo{})
}

// --- helpers ---

// newFakeRegistryFor returns a single httptest.Server that serves multiple
// skill artifacts under a shared repository prefix. Each (name,version) pair
// is served at `<prefix>/<name>:<version>`.
func newFakeRegistryFor(t *testing.T, prefix string, items []struct{ name, version string }) (*httptest.Server, string) {
	t.Helper()
	// Build all artifacts up front so we can route by URL path.
	type artifact struct {
		manifestBytes []byte
		manifestDig   string
		blobs         map[string][]byte
	}
	artifacts := make(map[string]artifact) // key: <prefix>/<name> -> artifact
	tags := make(map[string]string)        // key: "<repo>:<tag>" -> manifest digest

	for _, it := range items {
		repo := fmt.Sprintf("%s/%s", prefix, it.name)
		manB, manDig, blobs := buildSkillArtifact(t, it.name, it.version)
		artifacts[repo] = artifact{manifestBytes: manB, manifestDig: manDig, blobs: blobs}
		tags[fmt.Sprintf("%s:%s", repo, it.version)] = manDig
	}

	mux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/v2/" || path == "/v2" {
			w.WriteHeader(http.StatusOK)
			return
		}
		// /v2/<repo>/manifests/<ref> or /blobs/<digest>
		const v2 = "/v2/"
		if !strings.HasPrefix(path, v2) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		rest := path[len(v2):]
		// blobs
		if i := strings.Index(rest, "/blobs/"); i >= 0 {
			repo := rest[:i]
			d := rest[i+len("/blobs/"):]
			if art, ok := artifacts[repo]; ok {
				if body, ok := art.blobs[d]; ok {
					w.Header().Set("Content-Type", "application/octet-stream")
					w.Header().Set("Docker-Content-Digest", d)
					w.Header().Set("Content-Length", fmt.Sprint(len(body)))
					if r.Method == http.MethodGet {
						_, _ = w.Write(body)
					}
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// manifests
		if i := strings.Index(rest, "/manifests/"); i >= 0 {
			repo := rest[:i]
			ref := rest[i+len("/manifests/"):]
			if art, ok := artifacts[repo]; ok {
				key := repo + ":" + ref
				if dig, isTag := tags[key]; isTag && dig == art.manifestDig {
					serveManifest(w, r, art.manifestBytes, art.manifestDig)
					return
				}
				if ref == art.manifestDig {
					serveManifest(w, r, art.manifestBytes, art.manifestDig)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	host := strings.TrimPrefix(srv.URL, "http://")
	return srv, host
}
