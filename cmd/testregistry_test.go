package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/liatrio/skills-oci/pkg/oci"
	"github.com/liatrio/skills-oci/pkg/skill"
)

// buildSkillArtifact constructs an in-memory skill artifact (config + layer +
// manifest) using the same media types as pkg/oci/push.go.
//
// Returns (manifestBytes, manifestDigestString, blobsByDigestString).
func buildSkillArtifact(t *testing.T, name, version string) ([]byte, string, map[string][]byte) {
	t.Helper()

	// Tar.gz layer containing a SKILL.md.
	var layerBuf bytes.Buffer
	gw := gzip.NewWriter(&layerBuf)
	tw := tar.NewWriter(gw)
	content := []byte("# " + name + "\n")
	hdr := &tar.Header{
		Name: filepath.Join(name, "SKILL.md"),
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
	layerDig := digest.FromBytes(layer)

	cfg := skill.SkillConfig{Name: name, Version: version}
	cfgBytes, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal cfg: %v", err)
	}
	cfgDig := digest.FromBytes(cfgBytes)

	manifest := ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: oci.ArtifactType,
		Config: ocispec.Descriptor{
			MediaType: oci.ConfigMediaType,
			Digest:    cfgDig,
			Size:      int64(len(cfgBytes)),
		},
		Layers: []ocispec.Descriptor{{
			MediaType: oci.ContentMediaType,
			Digest:    layerDig,
			Size:      int64(len(layer)),
		}},
	}
	manBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	manDig := digest.FromBytes(manBytes).String()

	blobs := map[string][]byte{
		cfgDig.String():   cfgBytes,
		layerDig.String(): layer,
	}
	return manBytes, manDig, blobs
}

func serveManifest(w http.ResponseWriter, r *http.Request, manBytes []byte, manDig string) {
	w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
	w.Header().Set("Docker-Content-Digest", manDig)
	w.Header().Set("Content-Length", fmt.Sprint(len(manBytes)))
	if r.Method == http.MethodGet {
		_, _ = w.Write(manBytes)
	}
}
