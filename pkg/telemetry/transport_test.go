package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// redirectCacheDir swaps the cache-dir seam to point at a t.TempDir() for the
// duration of the test.
func redirectCacheDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev := telemetryCacheDir
	telemetryCacheDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { telemetryCacheDir = prev })
	return dir
}

// buildGoldenEvent constructs the canonical golden event using the
// deterministic seams pinned in event_test.go.
func buildGoldenEvent(t *testing.T) *Event {
	t.Helper()
	pinSeams(t,
		time.Date(2026, 5, 18, 17, 22, 0, 0, time.UTC),
		"01HM3K9QZX7N8T6BVCQ2KX3RZA",
		"darwin", "arm64",
	)
	evt, err := NewSkillDownloaded(validInput())
	if err != nil {
		t.Fatalf("NewSkillDownloaded: %v", err)
	}
	return evt
}

func TestEmit_PostsExpectedBody(t *testing.T) {
	useFreshConfig(t)

	var (
		gotMethod string
		gotPath   string
		gotCT     string
		gotAuth   string
		gotBody   []byte
		hits      int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	tr := NewTransport(Config{Enabled: true, Endpoint: srv.URL + "/v1/events", Token: "abc"})
	evt := buildGoldenEvent(t)
	if err := tr.Emit(context.Background(), evt); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	if hits != 1 {
		t.Errorf("hits = %d, want 1", hits)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/events" {
		t.Errorf("path = %q, want /v1/events", gotPath)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotCT)
	}
	if gotAuth != "Bearer abc" {
		t.Errorf("Authorization = %q, want Bearer abc", gotAuth)
	}

	want, err := os.ReadFile("testdata/event-skill-downloaded.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	want = bytes.TrimRight(want, "\n")
	if !bytes.Equal(gotBody, want) {
		t.Errorf("body mismatch\n got: %s\nwant: %s", gotBody, want)
	}
}

func TestEmit_4xxDropsNoRetry(t *testing.T) {
	useFreshConfig(t)
	dir := redirectCacheDir(t)

	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	tr := NewTransport(Config{Enabled: true, Endpoint: srv.URL + "/v1/events", Token: "abc"})
	evt := buildGoldenEvent(t)
	err := tr.Emit(context.Background(), evt)
	var perm *PermanentError
	if !errors.As(err, &perm) {
		t.Fatalf("expected *PermanentError, got %T: %v", err, err)
	}
	if perm.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d, want 400", perm.StatusCode)
	}
	if hits != 1 {
		t.Errorf("hits = %d, want 1 (no retry)", hits)
	}

	logBytes, err := os.ReadFile(filepath.Join(dir, "last-error.log"))
	if err != nil {
		t.Fatalf("read last-error.log: %v", err)
	}
	logStr := string(logBytes)
	if !bytes.Contains(logBytes, []byte("status=400")) {
		t.Errorf("last-error.log missing status=400: %q", logStr)
	}
	if !bytes.Contains(logBytes, []byte(evt.EventID)) {
		t.Errorf("last-error.log missing event_id %q: %q", evt.EventID, logStr)
	}
}

func TestEmit_5xxReturnsTransient(t *testing.T) {
	useFreshConfig(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	tr := NewTransport(Config{Enabled: true, Endpoint: srv.URL + "/v1/events", Token: "abc"})
	err := tr.Emit(context.Background(), buildGoldenEvent(t))
	var tr2 *TransientError
	if !errors.As(err, &tr2) {
		t.Fatalf("expected *TransientError, got %T: %v", err, err)
	}
	if tr2.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", tr2.StatusCode)
	}
}

func TestEmit_TimeoutBounded(t *testing.T) {
	useFreshConfig(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(3 * time.Second)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	tr := NewTransport(Config{Enabled: true, Endpoint: srv.URL + "/v1/events", Token: "abc"})
	start := time.Now()
	err := tr.Emit(context.Background(), buildGoldenEvent(t))
	elapsed := time.Since(start)

	var tr2 *TransientError
	if !errors.As(err, &tr2) {
		t.Fatalf("expected *TransientError, got %T: %v", err, err)
	}
	// Upper bound: 2s emitter timeout + 2.5s CI scheduling slack.
	if elapsed > 4500*time.Millisecond {
		t.Errorf("elapsed = %v, want <= 4.5s", elapsed)
	}
}

func TestEmit_OffMakesNoNetworkCall(t *testing.T) {
	useFreshConfig(t)
	dir := redirectCacheDir(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("transport must not hit the network when disabled")
	}))
	t.Cleanup(srv.Close)

	tr := NewTransport(Config{Enabled: false, Endpoint: srv.URL + "/v1/events", Token: "abc"})
	if err := tr.Emit(context.Background(), buildGoldenEvent(t)); err != nil {
		t.Fatalf("Emit (off): %v", err)
	}
	// No file should be created under the redirected cache dir.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read tempdir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("cache dir not empty: %v", entries)
	}
}

func TestClassifyHTTPStatus(t *testing.T) {
	cases := []struct {
		code int
		kind string
	}{
		{200, "nil"},
		{202, "nil"},
		{299, "nil"},
		{400, "permanent"},
		{401, "permanent"},
		{403, "permanent"},
		{422, "permanent"},
		{499, "permanent"},
		{500, "transient"},
		{502, "transient"},
		{599, "transient"},
		{301, "transient"}, // unexpected non-2xx non-4xx is transient
	}
	for _, tc := range cases {
		err := classifyHTTPStatus(tc.code, "evt")
		switch tc.kind {
		case "nil":
			if err != nil {
				t.Errorf("code %d: expected nil, got %v", tc.code, err)
			}
		case "permanent":
			var p *PermanentError
			if !errors.As(err, &p) {
				t.Errorf("code %d: expected *PermanentError, got %T", tc.code, err)
			}
		case "transient":
			var tr *TransientError
			if !errors.As(err, &tr) {
				t.Errorf("code %d: expected *TransientError, got %T", tc.code, err)
			}
		}
	}
}

// TestEmit_EmptyEndpointIsNoOp ensures that an empty endpoint (the stock-build
// state until release-time -ldflags inject one) results in no network call
// and no error.
func TestEmit_EmptyEndpointIsNoOp(t *testing.T) {
	useFreshConfig(t)
	tr := NewTransport(Config{Enabled: true, Endpoint: "", Token: ""})
	if err := tr.Emit(context.Background(), buildGoldenEvent(t)); err != nil {
		t.Fatalf("Emit (empty endpoint): %v", err)
	}
}

// TestEmit_EmitRawPreservesBody ensures that EmitRaw posts the line verbatim,
// without re-marshaling.
func TestEmit_EmitRawPreservesBody(t *testing.T) {
	useFreshConfig(t)
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	tr := NewTransport(Config{Enabled: true, Endpoint: srv.URL + "/v1/events", Token: "abc"})
	raw, err := json.Marshal(buildGoldenEvent(t))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := tr.EmitRaw(context.Background(), raw); err != nil {
		t.Fatalf("EmitRaw: %v", err)
	}
	if !bytes.Equal(gotBody, raw) {
		t.Errorf("body mismatch: got %s want %s", gotBody, raw)
	}
}
