package config

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestLoad_ValidYAML(t *testing.T) {
	input := []byte(`
catalog:
  default_namespace: ghcr.io/liatrio/skills
  allow_missing_license: true
  concurrency: 8
`)

	got, err := Load(input)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Catalog.DefaultNamespace != "ghcr.io/liatrio/skills" {
		t.Errorf("DefaultNamespace = %q, want %q", got.Catalog.DefaultNamespace, "ghcr.io/liatrio/skills")
	}
	if !got.Catalog.AllowMissingLicense {
		t.Error("AllowMissingLicense = false, want true")
	}
	if got.Catalog.Concurrency != 8 {
		t.Errorf("Concurrency = %d, want 8", got.Catalog.Concurrency)
	}
}

func TestLoad_EmptyInputReturnsZeroValue(t *testing.T) {
	got, err := Load(nil)
	if err != nil {
		t.Fatalf("Load(nil): %v", err)
	}
	if got != (Config{}) {
		t.Errorf("Load(nil) returned non-zero: %+v", got)
	}

	got, err = Load([]byte("   \n  "))
	if err != nil {
		t.Fatalf("Load(whitespace): %v", err)
	}
	if got != (Config{}) {
		t.Errorf("Load(whitespace) returned non-zero: %+v", got)
	}
}

func TestLoad_PartialKeysOK(t *testing.T) {
	// Setting only one key inside catalog should leave the others at zero.
	input := []byte(`
catalog:
  default_namespace: ghcr.io/example
`)
	got, err := Load(input)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Catalog.DefaultNamespace != "ghcr.io/example" {
		t.Errorf("DefaultNamespace = %q", got.Catalog.DefaultNamespace)
	}
	if got.Catalog.AllowMissingLicense {
		t.Error("AllowMissingLicense should default to false")
	}
	if got.Catalog.Concurrency != 0 {
		t.Errorf("Concurrency = %d, want 0 (caller applies default)", got.Catalog.Concurrency)
	}
}

func TestLoad_UnknownTopLevelKeyWarnsButSucceeds(t *testing.T) {
	input := []byte(`
catalog:
  default_namespace: ghcr.io/example
future_section:
  not_a_real_key: value
`)
	stderr := captureStderr(t, func() {
		if _, err := Load(input); err != nil {
			t.Fatalf("Load rejected unknown key: %v", err)
		}
	})
	if !strings.Contains(stderr, "future_section") {
		t.Errorf("stderr %q should warn about unknown key", stderr)
	}
}

func TestLoad_NullCatalogSucceeds(t *testing.T) {
	// `catalog:` present with a null value exercises the validateRaw
	// !ok branch and must produce a zero-value Config without error.
	got, err := Load([]byte("catalog: null\n"))
	if err != nil {
		t.Fatalf("Load(catalog: null): %v", err)
	}
	if got != (Config{}) {
		t.Errorf("Load(catalog: null) returned non-zero: %+v", got)
	}
}

func TestLoad_ScalarCatalogRejects(t *testing.T) {
	// `catalog: 42` short-circuits validateRaw (not a mapping) but the
	// typed second pass cannot decode a scalar into CatalogConfig.
	_, err := Load([]byte("catalog: 42\n"))
	if err == nil {
		t.Fatal("Load accepted scalar catalog, want error")
	}
}

func TestLoad_SequenceCatalogRejects(t *testing.T) {
	// A sequence-typed `catalog:` passes the untyped first pass and
	// short-circuits validateRaw (not a mapping), so the error must come
	// from the typed second yaml.Unmarshal.
	input := []byte("catalog:\n  - item1\n  - item2\n")
	_, err := Load(input)
	if err == nil {
		t.Fatal("Load accepted sequence catalog, want error")
	}
}

func TestLoad_TypeMismatchRejects(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantField string
	}{
		{
			name:      "concurrency string",
			input:     "catalog:\n  concurrency: \"four\"\n",
			wantField: "concurrency",
		},
		{
			name:      "default_namespace int",
			input:     "catalog:\n  default_namespace: 123\n",
			wantField: "default_namespace",
		},
		{
			name:      "allow_missing_license string",
			input:     "catalog:\n  allow_missing_license: \"yes\"\n",
			wantField: "allow_missing_license",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load([]byte(tc.input))
			if err == nil {
				t.Fatalf("Load accepted %s, want error", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantField) {
				t.Errorf("error %q lacks %q context", err.Error(), tc.wantField)
			}
		})
	}
}

func TestLoad_RejectsNegativeConcurrency(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"zero", "0"},
		{"negative", "-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := []byte("catalog:\n  concurrency: " + tc.value + "\n")
			_, err := Load(input)
			if err == nil {
				t.Fatalf("Load accepted concurrency=%s, want error", tc.value)
			}
			if !strings.Contains(err.Error(), "concurrency") {
				t.Errorf("error %q lacks 'concurrency' context", err.Error())
			}
		})
	}
}

func TestLoad_MalformedYAMLRejects(t *testing.T) {
	input := []byte("catalog:\n  default_namespace: [unclosed\n")
	_, err := Load(input)
	if err == nil {
		t.Fatal("Load accepted malformed YAML")
	}
}

// captureStderr runs fn while capturing anything written to os.Stderr,
// then returns the captured bytes as a string. Restores os.Stderr on
// return. Callers must not run in parallel — this swaps the global
// os.Stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	fn()
	w.Close()
	<-done
	os.Stderr = orig
	return buf.String()
}
