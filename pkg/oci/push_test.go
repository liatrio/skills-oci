package oci

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// pushRegistry is a minimal in-process OCI Distribution registry supporting
// the read-write subset that oras.Copy exercises on push:
//   - GET /v2/                                   (auth probe)
//   - HEAD /v2/<repo>/blobs/<digest>             (existence check)
//   - POST /v2/<repo>/blobs/uploads/             (start upload)
//   - PUT  /v2/<repo>/blobs/uploads/<id>?digest= (finish monolithic upload)
//   - GET  /v2/<repo>/blobs/<digest>             (readback)
//   - PUT  /v2/<repo>/manifests/<ref>            (push manifest)
//   - HEAD/GET /v2/<repo>/manifests/<ref>        (resolve / readback)
//
// Blobs and manifests are stored in memory keyed by digest; tags map to a
// manifest digest. Enough to push a skill artifact and read the manifest
// (and its annotations) back.
type pushRegistry struct {
	mu        sync.Mutex
	blobs     map[string][]byte // digest -> content
	manifests map[string][]byte // digest -> content
	tags      map[string]string // tag -> manifest digest
	nextID    int
}

func newPushRegistry() *pushRegistry {
	return &pushRegistry{
		blobs:     map[string][]byte{},
		manifests: map[string][]byte{},
		tags:      map[string]string{},
	}
}

// manifestForTag returns the parsed manifest most recently pushed under tag.
func (p *pushRegistry) manifestForTag(t *testing.T, tag string) ocispec.Manifest {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	dig, ok := p.tags[tag]
	if !ok {
		t.Fatalf("no manifest pushed for tag %q", tag)
	}
	body, ok := p.manifests[dig]
	if !ok {
		t.Fatalf("tag %q points at missing manifest %q", tag, dig)
	}
	var m ocispec.Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	return m
}

func (p *pushRegistry) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/v2/" || path == "/v2" {
			w.WriteHeader(http.StatusOK)
			return
		}
		switch {
		case strings.Contains(path, "/blobs/uploads/"):
			p.handleUpload(w, r)
		case strings.Contains(path, "/blobs/"):
			p.handleBlob(w, r, path)
		case strings.Contains(path, "/manifests/"):
			p.handleManifest(w, r, path)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func (p *pushRegistry) handleUpload(w http.ResponseWriter, r *http.Request) {
	repo := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/v2/"), "/blobs/uploads/", 2)[0]
	switch r.Method {
	case http.MethodPost:
		p.mu.Lock()
		p.nextID++
		id := strconv.Itoa(p.nextID)
		p.mu.Unlock()
		w.Header().Set("Location", fmt.Sprintf("/v2/%s/blobs/uploads/%s", repo, id))
		w.Header().Set("Docker-Upload-UUID", id)
		w.Header().Set("Range", "0-0")
		w.WriteHeader(http.StatusAccepted)
	case http.MethodPut:
		dig := r.URL.Query().Get("digest")
		body := readAll(w, r)
		if body == nil {
			return
		}
		if got := digest.FromBytes(body).String(); dig != "" && got != dig {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		p.mu.Lock()
		p.blobs[dig] = body
		p.mu.Unlock()
		w.Header().Set("Docker-Content-Digest", dig)
		w.Header().Set("Location", fmt.Sprintf("/v2/%s/blobs/%s", repo, dig))
		w.WriteHeader(http.StatusCreated)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (p *pushRegistry) handleBlob(w http.ResponseWriter, r *http.Request, path string) {
	dig := path[strings.Index(path, "/blobs/")+len("/blobs/"):]
	p.mu.Lock()
	body, ok := p.blobs[dig]
	p.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Docker-Content-Digest", dig)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodGet {
		_, _ = w.Write(body)
	}
}

func (p *pushRegistry) handleManifest(w http.ResponseWriter, r *http.Request, path string) {
	ref := path[strings.Index(path, "/manifests/")+len("/manifests/"):]
	switch r.Method {
	case http.MethodPut:
		body := readAll(w, r)
		if body == nil {
			return
		}
		dig := digest.FromBytes(body).String()
		p.mu.Lock()
		p.manifests[dig] = body
		if !strings.HasPrefix(ref, "sha256:") {
			p.tags[ref] = dig
		}
		p.mu.Unlock()
		w.Header().Set("Docker-Content-Digest", dig)
		w.WriteHeader(http.StatusCreated)
	case http.MethodGet, http.MethodHead:
		p.mu.Lock()
		dig := ref
		if t, ok := p.tags[ref]; ok {
			dig = t
		}
		body, ok := p.manifests[dig]
		p.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
		w.Header().Set("Docker-Content-Digest", dig)
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		if r.Method == http.MethodGet {
			_, _ = w.Write(body)
		}
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func readAll(w http.ResponseWriter, r *http.Request) []byte {
	buf := make([]byte, 0, r.ContentLength)
	tmp := make([]byte, 32*1024)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return buf
}

// pushSampleSkill pushes testdata/sample-skill to reg with the given caller
// annotations and returns the resolved tag.
func pushSampleSkill(t *testing.T, host string, annotations map[string]string) string {
	t.Helper()
	const tag = "1.0.0"
	_, err := Push(context.Background(), PushOptions{
		Reference:   fmt.Sprintf("%s/liatrio-labs/skills/manage-pull-requests", host),
		Tag:         tag,
		SkillDir:    "../../testdata/sample-skill",
		PlainHTTP:   true,
		Annotations: annotations,
	})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	return tag
}

func TestPush_MergesCallerAnnotations(t *testing.T) {
	const revision = "0123456789abcdef0123456789abcdef01234567"
	reg := newPushRegistry()
	srv := httptest.NewServer(reg.handler())
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	tag := pushSampleSkill(t, host, map[string]string{
		ocispec.AnnotationRevision: revision,
	})

	m := reg.manifestForTag(t, tag)
	if got := m.Annotations[ocispec.AnnotationRevision]; got != revision {
		t.Errorf("manifest annotation %q = %q, want %q", ocispec.AnnotationRevision, got, revision)
	}
	// The built-in skill-name annotation must still be present (additive merge).
	if m.Annotations[AnnotationSkillName] == "" {
		t.Errorf("built-in annotation %q missing after caller merge", AnnotationSkillName)
	}
}

func TestPush_CallerAnnotationOverridesBuiltin(t *testing.T) {
	const customTitle = "Vendored: manage-pull-requests"
	reg := newPushRegistry()
	srv := httptest.NewServer(reg.handler())
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	tag := pushSampleSkill(t, host, map[string]string{
		ocispec.AnnotationTitle: customTitle,
	})

	m := reg.manifestForTag(t, tag)
	if got := m.Annotations[ocispec.AnnotationTitle]; got != customTitle {
		t.Errorf("caller annotation should win: %q = %q, want %q", ocispec.AnnotationTitle, got, customTitle)
	}
}

func TestPush_NilAnnotations_UnchangedManifest(t *testing.T) {
	reg := newPushRegistry()
	srv := httptest.NewServer(reg.handler())
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	tag := pushSampleSkill(t, host, nil)

	m := reg.manifestForTag(t, tag)
	// Built-in set is intact and no caller key leaked in.
	if m.Annotations[ocispec.AnnotationTitle] != "manage-pull-requests" {
		t.Errorf("built-in title = %q, want %q", m.Annotations[ocispec.AnnotationTitle], "manage-pull-requests")
	}
	if m.Annotations[AnnotationSkillName] != "manage-pull-requests" {
		t.Errorf("built-in skill name = %q, want %q", m.Annotations[AnnotationSkillName], "manage-pull-requests")
	}
	if _, ok := m.Annotations[ocispec.AnnotationRevision]; ok {
		t.Errorf("unexpected revision annotation present for nil caller map")
	}
}
