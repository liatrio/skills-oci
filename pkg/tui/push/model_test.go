package push

import (
	"reflect"
	"testing"
)

func TestNewModel_StoresAnnotations(t *testing.T) {
	anns := map[string]string{
		"org.opencontainers.image.revision": "abc123",
		"org.opencontainers.image.source":   "https://example.com/repo",
	}

	m := NewModel("ghcr.io/org/skills/my-skill:1.0.0", ".", false, anns)

	if !reflect.DeepEqual(m.annotations, anns) {
		t.Fatalf("annotations = %v, want %v", m.annotations, anns)
	}
}

func TestNewModel_NilAnnotations(t *testing.T) {
	m := NewModel("ghcr.io/org/skills/my-skill:1.0.0", ".", false, nil)

	if m.annotations != nil {
		t.Fatalf("annotations = %v, want nil", m.annotations)
	}
}
