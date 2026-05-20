package telemetry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// Buffer is the persistent failure-tolerance layer for telemetry. Failed events
// are appended one JSON object per line to <dir>/pending.ndjson; the file is
// capped at maxBytes with FIFO eviction. Drain re-reads lines in insertion
// order, calling emit for each, up to perFlush per call.
//
// Append and Drain are safe to call from multiple goroutines: each emitter
// EmitSkillDownloaded runs on its own goroutine, so the install command can
// drive concurrent buffer mutations. mu serializes the read-modify-write of
// pending.ndjson to keep events from being lost or reordered.
type Buffer struct {
	dir      string
	maxBytes int64
	perFlush int
	mu       sync.Mutex
}

const (
	bufferFileName   = "pending.ndjson"
	defaultMaxBytes  = int64(1 << 20) // 1 MB
	defaultPerFlush  = 50
	dirPermissions   = 0o700
	fileWritePerm    = 0o600
	tmpFileExtension = ".tmp"
)

// NewBuffer constructs a Buffer rooted at dir with the spec defaults (1 MB cap,
// 50 events per flush).
func NewBuffer(dir string) *Buffer {
	return &Buffer{dir: dir, maxBytes: defaultMaxBytes, perFlush: defaultPerFlush}
}

// path returns the absolute pending.ndjson path under the buffer's root dir.
func (b *Buffer) path() string {
	return filepath.Join(b.dir, bufferFileName)
}

// ensureDir creates the buffer's parent directory with 0700 permissions.
func (b *Buffer) ensureDir() error {
	return os.MkdirAll(b.dir, dirPermissions)
}

// Append writes a single line (terminating newline added if absent) to the
// buffer. If the resulting file would exceed maxBytes, the oldest lines are
// evicted FIFO until the new line fits, then the file is rewritten atomically.
func (b *Buffer) Append(line []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if err := b.ensureDir(); err != nil {
		return fmt.Errorf("creating buffer dir: %w", err)
	}
	withNL := ensureNewline(line)

	fi, err := os.Stat(b.path())
	currentSize := int64(0)
	if err == nil {
		currentSize = fi.Size()
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("statting buffer: %w", err)
	}

	if currentSize+int64(len(withNL)) <= b.maxBytes {
		f, err := os.OpenFile(b.path(), os.O_APPEND|os.O_WRONLY|os.O_CREATE, fileWritePerm)
		if err != nil {
			return fmt.Errorf("opening buffer for append: %w", err)
		}
		if _, werr := f.Write(withNL); werr != nil {
			_ = f.Close()
			return fmt.Errorf("appending to buffer: %w", werr)
		}
		if cerr := f.Close(); cerr != nil {
			return fmt.Errorf("closing buffer after append: %w", cerr)
		}
		return nil
	}

	// Slow path: read all valid lines, evict oldest until the new line fits.
	lines, err := b.iterLines()
	if err != nil {
		return err
	}
	lines = append(lines, bytes.TrimRight(withNL, "\n"))
	for len(lines) > 0 && totalSize(lines) > b.maxBytes {
		lines = lines[1:]
	}
	return rewriteAtomic(b.path(), lines)
}

// Drain reads up to b.perFlush lines (or max if non-zero and smaller) and
// invokes emit on each in FIFO order.
//   - On nil: line is consumed.
//   - On *TransientError: stop; current and remaining lines are kept.
//   - On *PermanentError: drop the line and continue (producer bug, not
//     transient).
//   - On any other error: stop; current and remaining lines are kept.
//
// After draining, the file is rewritten atomically with whatever lines
// remain. Returns the number of lines successfully consumed.
func (b *Buffer) Drain(ctx context.Context, emit func(context.Context, []byte) error, max int) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	lines, err := b.iterLines()
	if err != nil {
		return 0, err
	}
	if len(lines) == 0 {
		return 0, nil
	}

	cap := b.perFlush
	if max > 0 && max < cap {
		cap = max
	}

	drained := 0
	remaining := make([][]byte, 0, len(lines))
	var finalErr error
	stopped := false

	for i, line := range lines {
		if stopped {
			remaining = append(remaining, line)
			continue
		}
		if drained >= cap {
			// Per-call drain cap reached; remaining lines persist for next call.
			remaining = append(remaining, lines[i:]...)
			break
		}
		if err := emit(ctx, line); err != nil {
			var perm *PermanentError
			if errors.As(err, &perm) {
				// drop and continue
				continue
			}
			var tr *TransientError
			if errors.As(err, &tr) {
				remaining = append(remaining, line)
				finalErr = err
				stopped = true
				continue
			}
			// Unknown error: behave like transient (keep line, stop).
			remaining = append(remaining, line)
			finalErr = err
			stopped = true
			continue
		}
		drained++
	}

	if err := rewriteAtomic(b.path(), remaining); err != nil {
		// Don't mask the emit error if there was one.
		if finalErr == nil {
			finalErr = err
		}
	}
	return drained, finalErr
}

// iterLines returns all well-formed lines from the buffer file, in FIFO
// (insertion) order. A truncated/invalid trailing line is silently dropped.
func (b *Buffer) iterLines() ([][]byte, error) {
	data, err := os.ReadFile(b.path())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading buffer: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}

	var out [][]byte
	// Split on \n; a trailing newline means an empty final segment which we drop.
	// A non-empty final segment without a trailing newline is a truncated line
	// and we also drop it.
	hadTrailingNewline := data[len(data)-1] == '\n'
	parts := bytes.Split(data, []byte("\n"))
	limit := len(parts)
	if hadTrailingNewline {
		// Last part is "" after split; drop it.
		limit = len(parts) - 1
	} else {
		// Last part is a truncated line; drop it.
		limit = len(parts) - 1
	}
	for i := 0; i < limit; i++ {
		if len(parts[i]) == 0 {
			continue
		}
		// Make a copy so callers can't mutate the slice into shared storage.
		cp := make([]byte, len(parts[i]))
		copy(cp, parts[i])
		out = append(out, cp)
	}
	return out, nil
}

func ensureNewline(line []byte) []byte {
	if len(line) > 0 && line[len(line)-1] == '\n' {
		return line
	}
	out := make([]byte, len(line)+1)
	copy(out, line)
	out[len(line)] = '\n'
	return out
}

func totalSize(lines [][]byte) int64 {
	var n int64
	for _, l := range lines {
		n += int64(len(l)) + 1 // +1 for the \n terminator
	}
	return n
}

// rewriteAtomic writes lines (each followed by \n) to path via a tmp-file +
// rename so a partially-written buffer cannot be observed.
func rewriteAtomic(path string, lines [][]byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPermissions); err != nil {
		return fmt.Errorf("creating buffer dir: %w", err)
	}
	tmp := path + tmpFileExtension
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fileWritePerm)
	if err != nil {
		return fmt.Errorf("opening tmp buffer: %w", err)
	}
	w := io.Writer(f)
	for _, l := range lines {
		if _, err := w.Write(l); err != nil {
			f.Close()
			return fmt.Errorf("writing tmp buffer: %w", err)
		}
		if _, err := w.Write([]byte{'\n'}); err != nil {
			f.Close()
			return fmt.Errorf("writing tmp newline: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing tmp buffer: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("renaming tmp buffer: %w", err)
	}
	return nil
}
