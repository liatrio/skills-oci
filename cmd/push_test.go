package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestParseAnnotations(t *testing.T) {
	tests := []struct {
		name    string
		input   []string
		want    map[string]string
		wantErr bool
	}{
		{
			name:  "single key=value",
			input: []string{"k=v"},
			want:  map[string]string{"k": "v"},
		},
		{
			name:  "value containing equals splits on first only",
			input: []string{"a=b=c"},
			want:  map[string]string{"a": "b=c"},
		},
		{
			name:  "multiple distinct keys merged",
			input: []string{"org.opencontainers.image.revision=abc123", "org.opencontainers.image.source=https://example.com/repo"},
			want: map[string]string{
				"org.opencontainers.image.revision": "abc123",
				"org.opencontainers.image.source":   "https://example.com/repo",
			},
		},
		{
			name:  "duplicate key last wins",
			input: []string{"k=first", "k=second"},
			want:  map[string]string{"k": "second"},
		},
		{
			name:  "empty value is allowed",
			input: []string{"k="},
			want:  map[string]string{"k": ""},
		},
		{
			name:  "nil input yields nil map",
			input: nil,
			want:  nil,
		},
		{
			name:  "empty slice yields nil map",
			input: []string{},
			want:  nil,
		},
		{
			name:    "missing equals is rejected",
			input:   []string{"foo"},
			wantErr: true,
		},
		{
			name:    "empty key is rejected",
			input:   []string{"=v"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAnnotations(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseAnnotations(%q) = %v, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAnnotations(%q) unexpected error: %v", tt.input, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseAnnotations(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestPushCommand_StampsAnnotation drives the real push command end to end
// (cobra flag parsing -> parseAnnotations -> runPushPlain -> oci.Push -> HTTP
// push) against an in-process registry, then reads the manifest back and
// asserts caller annotations land alongside the surviving built-ins. This is
// the writer side of the catalog-sync reconcile contract: the .revision
// annotation FetchSourceRevision reads back must be stamped by push.
func TestPushCommand_StampsAnnotation(t *testing.T) {
	const revision = "0123456789abcdef0123456789abcdef01234567"
	const source = "https://github.com/liatrio-labs/example"
	const tag = "1.0.0"

	reg := newPushTestRegistry()
	srv := httptest.NewServer(reg.handler())
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	cmd := newPushCmd()
	cmd.SetArgs([]string{
		fmt.Sprintf("%s/liatrio-labs/skills/manage-pull-requests:%s", host, tag),
		"../testdata/sample-skill",
		"--plain",
		"--plain-http",
		"--annotation", "org.opencontainers.image.revision=" + revision,
		"--annotation", "org.opencontainers.image.source=" + source,
	})
	// --plain and --plain-http are persistent flags on root; register locally so
	// the command can be executed in isolation.
	cmd.Flags().Bool("plain", false, "")
	cmd.Flags().Bool("plain-http", false, "")

	if err := cmd.Execute(); err != nil {
		t.Fatalf("push command: %v", err)
	}

	m := reg.manifestForTag(t, tag)
	if got := m.Annotations[ocispec.AnnotationRevision]; got != revision {
		t.Errorf("revision annotation = %q, want %q", got, revision)
	}
	if got := m.Annotations[ocispec.AnnotationSource]; got != source {
		t.Errorf("source annotation = %q, want %q", got, source)
	}
	// Built-in annotations must survive alongside caller-supplied ones.
	if m.Annotations[ocispec.AnnotationTitle] == "" {
		t.Errorf("built-in title annotation was dropped")
	}
}

// pushTestRegistry is a minimal in-process OCI Distribution registry supporting
// the read-write subset oras.Copy exercises on push. Blobs and manifests are
// stored in memory keyed by digest; tags map to a manifest digest.
type pushTestRegistry struct {
	mu        sync.Mutex
	blobs     map[string][]byte
	manifests map[string][]byte
	tags      map[string]string
	nextID    int
}

func newPushTestRegistry() *pushTestRegistry {
	return &pushTestRegistry{
		blobs:     map[string][]byte{},
		manifests: map[string][]byte{},
		tags:      map[string]string{},
	}
}

func (p *pushTestRegistry) manifestForTag(t *testing.T, tag string) ocispec.Manifest {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	dig, ok := p.tags[tag]
	if !ok {
		t.Fatalf("no manifest pushed for tag %q", tag)
	}
	var m ocispec.Manifest
	if err := json.Unmarshal(p.manifests[dig], &m); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	return m
}

func (p *pushTestRegistry) handler() http.Handler {
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

func (p *pushTestRegistry) handleUpload(w http.ResponseWriter, r *http.Request) {
	repo := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/v2/"), "/blobs/uploads/", 2)[0]
	switch r.Method {
	case http.MethodPost:
		p.mu.Lock()
		p.nextID++
		id := strconv.Itoa(p.nextID)
		p.mu.Unlock()
		w.Header().Set("Location", fmt.Sprintf("/v2/%s/blobs/uploads/%s", repo, id))
		w.Header().Set("Range", "0-0")
		w.WriteHeader(http.StatusAccepted)
	case http.MethodPut:
		dig := r.URL.Query().Get("digest")
		body := readAllBody(r)
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

func (p *pushTestRegistry) handleBlob(w http.ResponseWriter, r *http.Request, path string) {
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

func (p *pushTestRegistry) handleManifest(w http.ResponseWriter, r *http.Request, path string) {
	ref := path[strings.Index(path, "/manifests/")+len("/manifests/"):]
	switch r.Method {
	case http.MethodPut:
		body := readAllBody(r)
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

func readAllBody(r *http.Request) []byte {
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
