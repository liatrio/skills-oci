package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// PermanentError wraps a 4xx response. The CLI must not retry — the event is
// dropped and a single line is written to last-error.log.
type PermanentError struct {
	StatusCode int
	EventID    string
}

func (e *PermanentError) Error() string {
	return fmt.Sprintf("telemetry: permanent send failure: status=%d event_id=%s", e.StatusCode, e.EventID)
}

// TransientError wraps a 5xx, network, or timeout failure. The orchestrator
// routes these to the local NDJSON buffer for later retry.
type TransientError struct {
	StatusCode int    // 0 when no HTTP response was received
	EventID    string
	Cause      error
}

func (e *TransientError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("telemetry: transient send failure (status=%d event_id=%s): %v", e.StatusCode, e.EventID, e.Cause)
	}
	return fmt.Sprintf("telemetry: transient send failure: status=%d event_id=%s", e.StatusCode, e.EventID)
}

func (e *TransientError) Unwrap() error { return e.Cause }

// telemetryCacheDir is a seam so tests can redirect last-error.log to a
// temporary directory. Production: <UserCacheDir>/skills-oci/telemetry.
var telemetryCacheDir = func() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolving user cache dir: %w", err)
	}
	return filepath.Join(base, "skills-oci", "telemetry"), nil
}

const (
	httpTimeout     = 2 * time.Second
	contentTypeJSON = "application/json"
	eventsPath      = "/v1/events"
)

// Transport posts events to the configured collector endpoint over HTTP with
// a hard 2-second timeout. It does no retrying; classify and route is the
// orchestrator's job (see emitter.go).
type Transport struct {
	cfg    Config
	client *http.Client
}

// NewTransport builds a Transport using a package-private *http.Client with
// the contract-mandated 2-second timeout.
func NewTransport(cfg Config) *Transport {
	return &Transport{
		cfg: cfg,
		client: &http.Client{
			Timeout: httpTimeout,
		},
	}
}

// Emit sends one event to the configured endpoint. Returns:
//   - nil on 2xx success,
//   - *PermanentError on 4xx (also writes a line to last-error.log),
//   - *TransientError on 5xx, network error, or timeout.
//
// When telemetry is disabled or no endpoint is configured, Emit is a no-op
// returning nil.
func (t *Transport) Emit(ctx context.Context, evt *Event) error {
	if !t.cfg.Enabled || t.cfg.Endpoint == "" {
		return nil
	}

	body, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshaling event: %w", err)
	}
	return t.send(ctx, evt.EventID, body)
}

// EmitRaw sends a pre-marshaled event line. Used when draining the buffer so
// the original event_id (encoded in the line) is preserved exactly.
func (t *Transport) EmitRaw(ctx context.Context, line []byte) error {
	if !t.cfg.Enabled || t.cfg.Endpoint == "" {
		return nil
	}
	// Pull the event_id out of the JSON body for error tagging only; failure
	// to parse is non-fatal here (the body is still sent verbatim).
	var probe struct {
		EventID string `json:"event_id"`
	}
	_ = json.Unmarshal(line, &probe)
	return t.send(ctx, probe.EventID, line)
}

func (t *Transport) send(ctx context.Context, eventID string, body []byte) error {
	reqCtx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, t.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return &TransientError{EventID: eventID, Cause: fmt.Errorf("building request: %w", err)}
	}
	req.Header.Set("Content-Type", contentTypeJSON)
	if t.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+t.cfg.Token)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return &TransientError{EventID: eventID, Cause: err}
	}
	statusErr := classifyHTTPStatus(resp.StatusCode, eventID)
	closeErr := resp.Body.Close()

	if statusErr != nil {
		var perm *PermanentError
		if errors.As(statusErr, &perm) {
			writeLastError(perm)
		}
		return statusErr
	}
	if closeErr != nil {
		return &TransientError{
			EventID: eventID,
			Cause:   fmt.Errorf("closing response body: %w", closeErr),
		}
	}
	return nil
}

// classifyHTTPStatus maps an HTTP status code to nil (2xx), *PermanentError
// (4xx), or *TransientError (everything else, including 5xx).
func classifyHTTPStatus(status int, eventID string) error {
	switch {
	case status >= 200 && status < 300:
		return nil
	case status >= 400 && status < 500:
		return &PermanentError{StatusCode: status, EventID: eventID}
	default:
		return &TransientError{StatusCode: status, EventID: eventID}
	}
}

// writeLastError writes a single line describing a permanent send failure to
// <UserCacheDir>/skills-oci/telemetry/last-error.log. Best-effort; any write
// error is silently swallowed because telemetry must never fail the command.
func writeLastError(perm *PermanentError) {
	dir, err := telemetryCacheDir()
	if err != nil {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	path := filepath.Join(dir, "last-error.log")
	line := fmt.Sprintf("%s status=%d event_id=%s\n",
		time.Now().UTC().Format(time.RFC3339), perm.StatusCode, perm.EventID)
	_ = os.WriteFile(path, []byte(line), 0o600)
}
