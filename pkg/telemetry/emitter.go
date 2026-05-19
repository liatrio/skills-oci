package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// SkillEmitter is the narrow interface the rest of the codebase depends on
// from pkg/telemetry. Tests and callers that want to short-circuit telemetry
// entirely can pass nil — see oci.PullOptions.Emitter — or pass a no-op
// implementation directly.
type SkillEmitter interface {
	EmitSkillDownloaded(input SkillDownloadedInput)
	Wait()
}

// Emitter orchestrates Config + Transport + Buffer. Use New for production;
// callers may pass nil where a SkillEmitter is accepted to disable telemetry.
type Emitter struct {
	cfg Config
	tx  *Transport
	buf *Buffer
	wg  sync.WaitGroup
}

// New builds an Emitter from the resolved config. Buffer is rooted at the
// (production) telemetry cache dir; if the cache dir can't be resolved the
// buffer is nil and transient failures are dropped (best-effort).
func New(cfg Config) *Emitter {
	e := &Emitter{cfg: cfg, tx: NewTransport(cfg)}
	if dir, err := telemetryCacheDir(); err == nil {
		e.buf = NewBuffer(dir)
	}
	return e
}

// NewWithBuffer is a test seam for injecting a Buffer rooted at a specific
// directory (e.g., t.TempDir()).
func NewWithBuffer(cfg Config, buf *Buffer) *Emitter {
	return &Emitter{cfg: cfg, tx: NewTransport(cfg), buf: buf}
}

// EmitSkillDownloaded constructs a skill.downloaded event from input and ships
// it asynchronously. On transient failure the event is buffered for next time;
// after a successful send the orchestrator drains up to perFlush buffered
// events. EmitSkillDownloaded never blocks beyond returning the construction
// error path; the network call happens on a goroutine awaited by Wait().
func (e *Emitter) EmitSkillDownloaded(in SkillDownloadedInput) {
	if e == nil || !e.cfg.Enabled || e.cfg.Endpoint == "" {
		return
	}
	evt, err := NewSkillDownloaded(in)
	if err != nil {
		// Producer bug; never fail the user-facing command. A future
		// refactor may want to surface this in last-error.log.
		return
	}

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		// Use a fresh context with the contract's 2s budget. The transport
		// also applies its own timeout, so this is belt-and-braces.
		ctx, cancel := context.WithTimeout(context.Background(), httpTimeout)
		defer cancel()

		emitErr := e.tx.Emit(ctx, evt)
		if emitErr != nil {
			var tr *TransientError
			if errors.As(emitErr, &tr) && e.buf != nil {
				if body, err := json.Marshal(evt); err == nil {
					_ = e.buf.Append(body)
				}
			}
			// Permanent errors are dropped (last-error.log was already
			// written by the transport).
			return
		}

		// Success — try to drain a few buffered entries.
		if e.buf != nil {
			drainCtx, drainCancel := context.WithTimeout(context.Background(), httpTimeout)
			defer drainCancel()
			_, _ = e.buf.Drain(drainCtx, e.tx.EmitRaw, 0)
		}
	}()
}

// Wait blocks until all in-flight emissions have settled. The root command
// must call this before returning so a quick subcommand doesn't race with
// telemetry.
func (e *Emitter) Wait() {
	if e == nil {
		return
	}
	e.wg.Wait()
}

// WaitTimeout is a convenience for callers that want a bounded wait. It
// returns true if all in-flight emissions settled within d, false on timeout.
func (e *Emitter) WaitTimeout(d time.Duration) bool {
	if e == nil {
		return true
	}
	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}
