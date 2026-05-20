package cmd

import (
	"testing"
	"time"

	"github.com/salaboy/skills-oci/pkg/telemetry"
)

// TestEmitter_WaitTimeoutSemantics proves WaitTimeout returns true when no
// goroutines are in flight. The deeper property — that Wait() blocks until
// in-flight emissions finish — is covered by
// TestEmitter_WaitBlocksUntilGoroutineFinishes in pkg/telemetry, which exercises
// it against a real httptest-backed transport.
func TestEmitter_WaitTimeoutSemantics(t *testing.T) {
	em := telemetry.New(telemetry.Config{Enabled: false})
	if !em.WaitTimeout(10 * time.Millisecond) {
		t.Errorf("WaitTimeout should return true when no goroutines are in flight")
	}
}
