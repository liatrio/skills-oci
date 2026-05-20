package telemetry

import (
	"crypto/rand"
	"fmt"
	"io"
	"runtime"
	"time"

	"github.com/oklog/ulid/v2"
)

// EventType is the dotted "<noun>.<past-tense-verb>" type identifier carried in
// every event. Today only one value is produced.
const (
	EventTypeSkillDownloaded = "skill.downloaded"

	clientName    = "skills-oci"
	actorAnonymous = "anonymous"
	schemaVersion = 1
)

// Event is the on-the-wire envelope POSTed to /v1/events. Field order in this
// struct is the field order in the marshaled JSON body and must match the
// "Wire shape" section of docs/telemetry-data-contract.md.
type Event struct {
	SchemaVersion int          `json:"schema_version"`
	EventID       string       `json:"event_id"`
	EventType     string       `json:"event_type"`
	OccurredAt    string       `json:"occurred_at"`
	Client        ClientInfo   `json:"client"`
	Actor         Actor        `json:"actor"`
	Skill         SkillPayload `json:"skill"`
	Source        SourceInfo   `json:"source"`
}

// ClientInfo identifies the producing CLI build and host platform.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

// Actor identifies who (or what kind of principal) triggered the event.
// For this iteration the only kind is "anonymous".
type Actor struct {
	Kind string `json:"kind"`
}

// SkillPayload is the skill-specific body for event_type=skill.downloaded.
type SkillPayload struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	Digest    string `json:"digest"`
	Registry  string `json:"registry"`
	OCIRef    string `json:"oci_ref"`
}

// SourceInfo names the CLI surface that drove the event.
type SourceInfo struct {
	Command string `json:"command"`
	Trigger string `json:"trigger"`
}

// SkillDownloadedInput is the structured input the orchestrator passes to
// NewSkillDownloaded. Every string field is required.
type SkillDownloadedInput struct {
	CLIVersion string

	Namespace string
	Name      string
	Version   string
	Digest    string
	Registry  string
	OCIRef    string

	Command string
	Trigger string
}

// FieldRequiredError is returned by NewSkillDownloaded when a required input
// string is empty. The Field name is the JSON key from the contract.
type FieldRequiredError struct {
	Field string
}

func (e *FieldRequiredError) Error() string {
	return fmt.Sprintf("telemetry: required field %q is empty", e.Field)
}

// Clock, entropy, and platform seams allow tests to pin deterministic values.
// Production callers use the defaults (wall clock, crypto/rand, build runtime).
var (
	nowUTC     = func() time.Time { return time.Now().UTC() }
	newEntropy = func() io.Reader { return rand.Reader }
	newEventID = func(t time.Time) (string, error) {
		id, err := ulid.New(ulid.Timestamp(t), newEntropy())
		if err != nil {
			return "", err
		}
		return id.String(), nil
	}
	platformOS   = func() string { return runtime.GOOS }
	platformArch = func() string { return runtime.GOARCH }
)

// NewSkillDownloaded constructs a fully-populated skill.downloaded event.
// Returns a *FieldRequiredError if any required input string is empty.
func NewSkillDownloaded(in SkillDownloadedInput) (*Event, error) {
	required := []struct {
		name string
		val  string
	}{
		{"client.version", in.CLIVersion},
		{"skill.namespace", in.Namespace},
		{"skill.name", in.Name},
		{"skill.version", in.Version},
		{"skill.digest", in.Digest},
		{"skill.registry", in.Registry},
		{"skill.oci_ref", in.OCIRef},
		{"source.command", in.Command},
		{"source.trigger", in.Trigger},
	}
	for _, r := range required {
		if r.val == "" {
			return nil, &FieldRequiredError{Field: r.name}
		}
	}

	t := nowUTC()
	id, err := newEventID(t)
	if err != nil {
		return nil, fmt.Errorf("generating event_id: %w", err)
	}

	return &Event{
		SchemaVersion: schemaVersion,
		EventID:       id,
		EventType:     EventTypeSkillDownloaded,
		OccurredAt:    t.Truncate(time.Second).Format(time.RFC3339),
		Client: ClientInfo{
			Name:    clientName,
			Version: in.CLIVersion,
			OS:      platformOS(),
			Arch:    platformArch(),
		},
		Actor: Actor{Kind: actorAnonymous},
		Skill: SkillPayload{
			Namespace: in.Namespace,
			Name:      in.Name,
			Version:   in.Version,
			Digest:    in.Digest,
			Registry:  in.Registry,
			OCIRef:    in.OCIRef,
		},
		Source: SourceInfo{
			Command: in.Command,
			Trigger: in.Trigger,
		},
	}, nil
}
