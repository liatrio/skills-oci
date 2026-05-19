package oci

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/salaboy/skills-oci/pkg/skill"
)

// recordingEmitter captures every OnSkillDownloaded call.
type recordingEmitter struct {
	mu    sync.Mutex
	calls []SkillDownloadInfo
}

func (r *recordingEmitter) OnSkillDownloaded(info SkillDownloadInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, info)
}

func (r *recordingEmitter) snapshot() []SkillDownloadInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]SkillDownloadInfo, len(r.calls))
	copy(out, r.calls)
	return out
}

// fakeRegistry serves a single skill artifact via the minimum subset of the
// OCI Distribution Spec that oras-go's pull path exercises:
//   - GET /v2/ (auth probe)
//   - HEAD / GET /v2/<repo>/manifests/<tag-or-digest>
//   - HEAD / GET /v2/<repo>/blobs/<digest>
type fakeRegistry struct {
	t        *testing.T
	repo     string
	manifest []byte
	manDesc  ocispec.Descriptor
	tag      string
	blobs    map[digest.Digest][]byte
}

func (f *fakeRegistry) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case path == "/v2/" || path == "/v2":
			w.WriteHeader(http.StatusOK)
			return
		case strings.HasSuffix(path, fmt.Sprintf("/manifests/%s", f.tag)),
			strings.HasSuffix(path, fmt.Sprintf("/manifests/%s", f.manDesc.Digest)):
			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", f.manDesc.Digest.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(f.manifest)))
			if r.Method == http.MethodGet {
				_, _ = w.Write(f.manifest)
			}
			return
		}
		// /blobs/<digest>
		if idx := strings.Index(path, "/blobs/"); idx >= 0 {
			d := digest.Digest(path[idx+len("/blobs/"):])
			body, ok := f.blobs[d]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Docker-Content-Digest", d.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			if r.Method == http.MethodGet {
				_, _ = w.Write(body)
			}
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
}

// buildArtifact constructs an in-memory skill artifact (config + layer +
// manifest) using the same media types as pkg/oci/push.go. Returns the
// fakeRegistry ready to be wired into httptest.
func buildArtifact(t *testing.T, repoPath, tag, skillName, version string) *fakeRegistry {
	t.Helper()

	// 1. Build a minimal tar.gz layer containing a SKILL.md file.
	var layerBuf bytes.Buffer
	gw := gzip.NewWriter(&layerBuf)
	tw := tar.NewWriter(gw)
	content := []byte("# " + skillName + "\n")
	hdr := &tar.Header{
		Name: filepath.Join(skillName, "SKILL.md"),
		Mode: 0o644,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	layer := layerBuf.Bytes()
	layerDigest := digest.FromBytes(layer)

	// 2. Config blob is the skill config JSON.
	cfg := skill.SkillConfig{Name: skillName, Version: version}
	cfgBytes, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal cfg: %v", err)
	}
	cfgDigest := digest.FromBytes(cfgBytes)

	manifest := ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: ArtifactType,
		Config: ocispec.Descriptor{
			MediaType: ConfigMediaType,
			Digest:    cfgDigest,
			Size:      int64(len(cfgBytes)),
		},
		Layers: []ocispec.Descriptor{{
			MediaType: ContentMediaType,
			Digest:    layerDigest,
			Size:      int64(len(layer)),
		}},
	}
	manBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	manDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manBytes),
		Size:      int64(len(manBytes)),
	}

	return &fakeRegistry{
		t:        t,
		repo:     repoPath,
		manifest: manBytes,
		manDesc:  manDesc,
		tag:      tag,
		blobs: map[digest.Digest][]byte{
			cfgDigest:   cfgBytes,
			layerDigest: layer,
		},
	}
}

// hostNoScheme strips the leading "http://" from an httptest server URL so it
// can be used as the registry portion of an OCI reference.
func hostNoScheme(serverURL string) string {
	return strings.TrimPrefix(serverURL, "http://")
}

func TestPull_EmitsOneEventOnSuccess(t *testing.T) {
	const (
		repoPath  = "liatrio-labs/skills/example-skill"
		tag       = "1.0.0"
		skillName = "example-skill"
		version   = "1.0.0"
	)
	fake := buildArtifact(t, repoPath, tag, skillName, version)
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	host := hostNoScheme(srv.URL)
	ref := fmt.Sprintf("%s/%s:%s", host, repoPath, tag)

	rec := &recordingEmitter{}
	_, err := Pull(context.Background(), PullOptions{
		Reference:  ref,
		OutputDir:  t.TempDir(),
		PlainHTTP:  true,
		CLIVersion: "0.1.0",
		Command:    "add",
		Trigger:    "user",
		Emitter:    rec,
	})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	calls := rec.snapshot()
	if len(calls) != 1 {
		t.Fatalf("emitter calls = %d, want 1", len(calls))
	}
	got := calls[0]
	if got.Command != "add" || got.Trigger != "user" {
		t.Errorf("source = (%q,%q), want (add,user)", got.Command, got.Trigger)
	}
	if got.Name != skillName {
		t.Errorf("skill.name = %q, want %q", got.Name, skillName)
	}
	if got.Version != version {
		t.Errorf("skill.version = %q, want %q", got.Version, version)
	}
	if got.Registry != host {
		t.Errorf("skill.registry = %q, want %q", got.Registry, host)
	}
	wantRef := fmt.Sprintf("%s/%s:%s", host, repoPath, tag)
	if got.OCIRef != wantRef {
		t.Errorf("oci_ref = %q, want %q", got.OCIRef, wantRef)
	}
	if !strings.HasPrefix(got.Digest, "sha256:") {
		t.Errorf("digest = %q, want sha256: prefix", got.Digest)
	}
	if got.Namespace != "liatrio-labs" {
		t.Errorf("namespace = %q, want liatrio-labs", got.Namespace)
	}
}

func TestPull_EmitsZeroEventsOnFailure(t *testing.T) {
	// Registry that returns 404 for everything except the auth probe.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	host := hostNoScheme(srv.URL)
	ref := fmt.Sprintf("%s/missing/skill:1.0.0", host)

	rec := &recordingEmitter{}
	_, err := Pull(context.Background(), PullOptions{
		Reference:  ref,
		OutputDir:  t.TempDir(),
		PlainHTTP:  true,
		CLIVersion: "0.1.0",
		Command:    "add",
		Trigger:    "user",
		Emitter:    rec,
	})
	if err == nil {
		t.Fatalf("Pull: expected error, got nil")
	}
	if calls := rec.snapshot(); len(calls) != 0 {
		t.Errorf("emitter called %d times on failure, want 0", len(calls))
	}
}

func TestPull_NilEmitterIsHarmless(t *testing.T) {
	const (
		repoPath = "ns/skills/quiet"
		tag      = "2.0.0"
		name     = "quiet"
	)
	fake := buildArtifact(t, repoPath, tag, name, tag)
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	host := hostNoScheme(srv.URL)
	_, err := Pull(context.Background(), PullOptions{
		Reference: fmt.Sprintf("%s/%s:%s", host, repoPath, tag),
		OutputDir: t.TempDir(),
		PlainHTTP: true,
		// No Emitter, no CLIVersion, no Command/Trigger — should still work.
	})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	_ = io.Discard // keep io import alive across edits
}
