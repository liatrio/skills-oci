package telemetry

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// kbPayload returns a JSON-ish line padded to approximately n bytes.
func kbPayload(eventID string, n int) []byte {
	pad := bytes.Repeat([]byte{'x'}, n-len(eventID)-30)
	return []byte(fmt.Sprintf(`{"event_id":"%s","p":"%s"}`, eventID, string(pad)))
}

func TestBuffer_AppendThenRead(t *testing.T) {
	b := NewBuffer(t.TempDir())
	lines := [][]byte{
		[]byte(`{"event_id":"A"}`),
		[]byte(`{"event_id":"B"}`),
		[]byte(`{"event_id":"C"}`),
	}
	for _, l := range lines {
		if err := b.Append(l); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	got, err := b.iterLines()
	if err != nil {
		t.Fatalf("iterLines: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for i := range got {
		if !bytes.Equal(got[i], lines[i]) {
			t.Errorf("line %d: got %s, want %s", i, got[i], lines[i])
		}
	}
}

func TestBuffer_CapAndEviction(t *testing.T) {
	b := NewBuffer(t.TempDir())
	// 100 ~1 KB lines -> ~100 KB total, well above no-cap; force cap below.
	b.maxBytes = 50 * 1024 // 50 KB

	const total = 100
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("evt-%03d", i)
		if err := b.Append(kbPayload(id, 1024)); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	fi, err := os.Stat(b.path())
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Size() > b.maxBytes {
		t.Errorf("file size %d > cap %d", fi.Size(), b.maxBytes)
	}

	lines, err := b.iterLines()
	if err != nil {
		t.Fatalf("iterLines: %v", err)
	}

	// Newest must be retained (FIFO eviction drops oldest first).
	last := lines[len(lines)-1]
	if !bytes.Contains(last, []byte("evt-099")) {
		t.Errorf("newest line missing: %s", last)
	}
	first := lines[0]
	if bytes.Contains(first, []byte("evt-000")) {
		t.Errorf("oldest line should have been evicted, still present: %s", first)
	}
}

// recorderEmit collects every line passed to emit.
type recorderEmit struct{ calls [][]byte }

func (r *recorderEmit) emit(_ context.Context, line []byte) error {
	cp := make([]byte, len(line))
	copy(cp, line)
	r.calls = append(r.calls, cp)
	return nil
}

func TestBuffer_DrainsInOrderOnSuccess(t *testing.T) {
	b := NewBuffer(t.TempDir())
	ids := []string{"AAA", "BBB", "CCC"}
	for _, id := range ids {
		if err := b.Append([]byte(fmt.Sprintf(`{"event_id":"%s"}`, id))); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	r := &recorderEmit{}
	drained, err := b.Drain(context.Background(), r.emit, 0)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if drained != 3 {
		t.Errorf("drained = %d, want 3", drained)
	}
	for i, id := range ids {
		if !bytes.Contains(r.calls[i], []byte(id)) {
			t.Errorf("call %d missing id %s: %s", i, id, r.calls[i])
		}
	}
	// File should now be empty (rewritten with zero lines).
	rest, _ := b.iterLines()
	if len(rest) != 0 {
		t.Errorf("buffer not drained: %d lines remain", len(rest))
	}
}

func TestBuffer_DrainCapPerCall(t *testing.T) {
	b := NewBuffer(t.TempDir())
	for i := 0; i < 60; i++ {
		if err := b.Append([]byte(fmt.Sprintf(`{"event_id":"E-%02d"}`, i))); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	r := &recorderEmit{}
	drained, err := b.Drain(context.Background(), r.emit, 0) // uses default perFlush=50
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if drained != 50 {
		t.Errorf("drained = %d, want 50", drained)
	}
	rest, _ := b.iterLines()
	if len(rest) != 10 {
		t.Errorf("remaining = %d, want 10", len(rest))
	}
	// Sanity: first remaining should be E-50.
	if !bytes.Contains(rest[0], []byte("E-50")) {
		t.Errorf("first remaining = %s, want contains E-50", rest[0])
	}
}

func TestBuffer_TruncatedTrailingLineSkipped(t *testing.T) {
	dir := t.TempDir()
	b := NewBuffer(dir)
	// Write 3 valid lines + a truncated partial line.
	body := []byte(`{"event_id":"A"}` + "\n" +
		`{"event_id":"B"}` + "\n" +
		`{"event_id":"C"}` + "\n" +
		`{"event_id":"D-partial`)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(b.path(), body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	r := &recorderEmit{}
	drained, err := b.Drain(context.Background(), r.emit, 0)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if drained != 3 {
		t.Errorf("drained = %d, want 3", drained)
	}
	for _, c := range r.calls {
		if bytes.Contains(c, []byte("D-partial")) {
			t.Errorf("truncated line was drained: %s", c)
		}
	}
	rest, _ := b.iterLines()
	if len(rest) != 0 {
		t.Errorf("buffer not empty after drain: %d lines remain", len(rest))
	}
}

func TestBuffer_FilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions not enforced on Windows")
	}
	// Mimic production layout: buffer dir is a fresh subdir created by Buffer
	// itself, not the pre-existing tempdir (whose perms come from the OS).
	root := t.TempDir()
	dir := filepath.Join(root, "skills-oci", "telemetry")
	b := NewBuffer(dir)
	if err := b.Append([]byte(`{"event_id":"X"}`)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	fi, err := os.Stat(b.path())
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("file perm = %o, want 0600", perm)
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("dir perm = %o, want 0700", perm)
	}
}

func TestBuffer_PreservesEventID(t *testing.T) {
	b := NewBuffer(t.TempDir())
	original := []byte(`{"event_id":"01HM3K9QZX7N8T6BVCQ2KX3RZA","payload":"x"}`)
	if err := b.Append(original); err != nil {
		t.Fatalf("Append: %v", err)
	}
	r := &recorderEmit{}
	if _, err := b.Drain(context.Background(), r.emit, 0); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("len(calls) = %d, want 1", len(r.calls))
	}
	if !bytes.Equal(r.calls[0], original) {
		t.Errorf("drained line not byte-equal to original\n got: %s\nwant: %s", r.calls[0], original)
	}
}

func TestBuffer_DrainTransientStopsEarly(t *testing.T) {
	b := NewBuffer(t.TempDir())
	for _, id := range []string{"A", "B", "C"} {
		if err := b.Append([]byte(fmt.Sprintf(`{"event_id":"%s"}`, id))); err != nil {
			t.Fatalf("Append %s: %v", id, err)
		}
	}
	count := 0
	emit := func(_ context.Context, _ []byte) error {
		count++
		if count == 2 {
			return &TransientError{StatusCode: 500, EventID: "B"}
		}
		return nil
	}
	drained, err := b.Drain(context.Background(), emit, 0)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if drained != 1 {
		t.Errorf("drained = %d, want 1", drained)
	}
	rest, _ := b.iterLines()
	if len(rest) != 2 {
		t.Errorf("remaining = %d, want 2 (the failed line + the unsent one)", len(rest))
	}
}

func TestBuffer_DrainPermanentDropsAndContinues(t *testing.T) {
	b := NewBuffer(t.TempDir())
	for _, id := range []string{"A", "B", "C"} {
		if err := b.Append([]byte(fmt.Sprintf(`{"event_id":"%s"}`, id))); err != nil {
			t.Fatalf("Append %s: %v", id, err)
		}
	}
	count := 0
	emit := func(_ context.Context, _ []byte) error {
		count++
		if count == 2 {
			return &PermanentError{StatusCode: 400, EventID: "B"}
		}
		return nil
	}
	drained, err := b.Drain(context.Background(), emit, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if drained != 2 {
		t.Errorf("drained = %d, want 2 (A and C, B dropped)", drained)
	}
	rest, _ := b.iterLines()
	if len(rest) != 0 {
		t.Errorf("buffer not empty: %v", rest)
	}
}

// TestBuffer_AtomicTmpRewrite checks the helper directly.
func TestBuffer_AtomicTmpRewrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pending.ndjson")
	if err := rewriteAtomic(path, [][]byte{[]byte("one"), []byte("two")}); err != nil {
		t.Fatalf("rewriteAtomic: %v", err)
	}
	data, _ := os.ReadFile(path)
	want := "one\ntwo\n"
	if string(data) != want {
		t.Errorf("content = %q, want %q", string(data), want)
	}
	// tmp should be gone after rename.
	if _, err := os.Stat(path + tmpFileExtension); !os.IsNotExist(err) {
		t.Errorf("tmp file not cleaned up: err=%v", err)
	}
}
