package telemetry

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// pinSeamsForEmitter is a thin wrapper that pins the event seams so emitted
// events have deterministic event_id / occurred_at / platform values.
func pinSeamsForEmitter(t *testing.T) {
	pinSeams(t,
		time.Date(2026, 5, 18, 17, 22, 0, 0, time.UTC),
		"01HM3K9QZX7N8T6BVCQ2KX3RZA",
		"darwin", "arm64",
	)
}

func TestEmitter_SuccessDrainsBuffer(t *testing.T) {
	useFreshConfig(t)

	var (
		mu       sync.Mutex
		received [][]byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		received = append(received, body)
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	cfg := Config{Enabled: true, Endpoint: srv.URL + "/v1/events", Token: "t"}
	buf := NewBuffer(filepath.Join(t.TempDir(), "buf"))
	// Pre-seed two buffered entries (synthetic — content shape doesn't matter
	// for transport).
	if err := buf.Append([]byte(`{"event_id":"OLD-1","payload":"a"}`)); err != nil {
		t.Fatalf("seed Append: %v", err)
	}
	if err := buf.Append([]byte(`{"event_id":"OLD-2","payload":"b"}`)); err != nil {
		t.Fatalf("seed Append: %v", err)
	}

	e := NewWithBuffer(cfg, buf)
	pinSeamsForEmitter(t)
	e.EmitSkillDownloaded(validInput())
	e.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 3 {
		t.Fatalf("server received %d events, want 3", len(received))
	}
	// Buffer should now be empty.
	rest, _ := buf.iterLines()
	if len(rest) != 0 {
		t.Errorf("buffer not drained: %d lines remain", len(rest))
	}
}

func TestEmitter_TransientRoutesToBuffer(t *testing.T) {
	useFreshConfig(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	cfg := Config{Enabled: true, Endpoint: srv.URL + "/v1/events", Token: "t"}
	buf := NewBuffer(filepath.Join(t.TempDir(), "buf"))
	e := NewWithBuffer(cfg, buf)

	pinSeamsForEmitter(t)
	e.EmitSkillDownloaded(validInput())
	e.Wait()

	lines, err := buf.iterLines()
	if err != nil {
		t.Fatalf("iterLines: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("buffer has %d lines, want 1", len(lines))
	}
	var probe struct {
		EventID string `json:"event_id"`
	}
	if err := json.Unmarshal(lines[0], &probe); err != nil {
		t.Fatalf("unmarshal buffered line: %v", err)
	}
	if probe.EventID != "01HM3K9QZX7N8T6BVCQ2KX3RZA" {
		t.Errorf("buffered event_id = %q, want pinned ULID", probe.EventID)
	}
}

func TestEmitter_OffIsNoOp(t *testing.T) {
	useFreshConfig(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("emitter must not hit network when disabled")
	}))
	t.Cleanup(srv.Close)

	cfg := Config{Enabled: false, Endpoint: srv.URL + "/v1/events", Token: "t"}
	buf := NewBuffer(filepath.Join(t.TempDir(), "buf"))
	e := NewWithBuffer(cfg, buf)

	pinSeamsForEmitter(t)
	e.EmitSkillDownloaded(validInput())
	e.Wait()

	// No buffer file should exist (Emitter never touched it).
	if _, err := os.Stat(buf.path()); !os.IsNotExist(err) {
		t.Errorf("buffer file should not exist; err=%v", err)
	}
}

func TestEmitter_EmptyEndpointIsNoOp(t *testing.T) {
	useFreshConfig(t)
	cfg := Config{Enabled: true, Endpoint: "", Token: ""}
	buf := NewBuffer(filepath.Join(t.TempDir(), "buf"))
	e := NewWithBuffer(cfg, buf)
	pinSeamsForEmitter(t)
	e.EmitSkillDownloaded(validInput())
	e.Wait()
	if _, err := os.Stat(buf.path()); !os.IsNotExist(err) {
		t.Errorf("buffer file unexpectedly exists: err=%v", err)
	}
}

func TestEmitter_WaitBlocksUntilGoroutineFinishes(t *testing.T) {
	useFreshConfig(t)

	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(80 * time.Millisecond)
		hits.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	cfg := Config{Enabled: true, Endpoint: srv.URL + "/v1/events", Token: "t"}
	buf := NewBuffer(filepath.Join(t.TempDir(), "buf"))
	e := NewWithBuffer(cfg, buf)

	pinSeamsForEmitter(t)
	e.EmitSkillDownloaded(validInput())

	// Immediately after Emit, hits may still be 0; after Wait it MUST be 1.
	e.Wait()
	if hits.Load() != 1 {
		t.Errorf("after Wait(), hits = %d, want 1", hits.Load())
	}
}

func TestEmitter_NilReceiverIsSafe(t *testing.T) {
	var e *Emitter
	e.EmitSkillDownloaded(validInput())
	e.Wait()
	if !e.WaitTimeout(10 * time.Millisecond) {
		t.Errorf("nil WaitTimeout should return true")
	}
}
