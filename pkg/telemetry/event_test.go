package telemetry

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// pinSeams swaps the package-level clock, entropy, ULID, and platform seams to
// deterministic values for the duration of the test. The previous values are
// restored on t.Cleanup.
func pinSeams(t *testing.T, fixedTime time.Time, fixedULID, fixedOS, fixedArch string) {
	t.Helper()
	origNow, origEntropy, origID, origOS, origArch := nowUTC, newEntropy, newEventID, platformOS, platformArch
	nowUTC = func() time.Time { return fixedTime }
	newEntropy = func() io.Reader { return bytes.NewReader(bytes.Repeat([]byte{0}, 16)) }
	newEventID = func(time.Time) (string, error) { return fixedULID, nil }
	platformOS = func() string { return fixedOS }
	platformArch = func() string { return fixedArch }
	t.Cleanup(func() {
		nowUTC, newEntropy, newEventID, platformOS, platformArch = origNow, origEntropy, origID, origOS, origArch
	})
}

func validInput() SkillDownloadedInput {
	return SkillDownloadedInput{
		CLIVersion: "0.1.0",
		Namespace:  "liatrio-labs",
		Name:       "example-skill",
		Version:    "1.2.0",
		Digest:     "sha256:abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234",
		Registry:   "ghcr.io",
		OCIRef:     "ghcr.io/liatrio-labs/skills/example-skill:1.2.0",
		Command:    "add",
		Trigger:    "user",
	}
}

func TestEvent_GoldenBody(t *testing.T) {
	pinSeams(t,
		time.Date(2026, 5, 18, 17, 22, 0, 0, time.UTC),
		"01HM3K9QZX7N8T6BVCQ2KX3RZA",
		"darwin", "arm64",
	)

	evt, err := NewSkillDownloaded(validInput())
	if err != nil {
		t.Fatalf("NewSkillDownloaded: %v", err)
	}

	got, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	want, err := os.ReadFile("testdata/event-skill-downloaded.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	// Strip trailing newline that some editors append.
	want = bytes.TrimRight(want, "\n")

	if !bytes.Equal(got, want) {
		t.Fatalf("event body does not match golden\n got: %s\nwant: %s", got, want)
	}
}

func TestEvent_IDAndTimestampFormats(t *testing.T) {
	// Restore real seams so we exercise the real ULID generator.
	ulidRe := regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)
	tsRe := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)

	for i := 0; i < 50; i++ {
		evt, err := NewSkillDownloaded(validInput())
		if err != nil {
			t.Fatalf("iter %d: NewSkillDownloaded: %v", i, err)
		}
		if !ulidRe.MatchString(evt.EventID) {
			t.Errorf("iter %d: event_id %q does not match ULID regex", i, evt.EventID)
		}
		if !tsRe.MatchString(evt.OccurredAt) {
			t.Errorf("iter %d: occurred_at %q does not match RFC3339-second regex", i, evt.OccurredAt)
		}
	}

	// Sanity: actor.kind is anonymous, schema_version is 1, event_type is fixed.
	evt, _ := NewSkillDownloaded(validInput())
	if evt.Actor.Kind != "anonymous" {
		t.Errorf("actor.kind = %q, want anonymous", evt.Actor.Kind)
	}
	if evt.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", evt.SchemaVersion)
	}
	if evt.EventType != "skill.downloaded" {
		t.Errorf("event_type = %q, want skill.downloaded", evt.EventType)
	}
}

func TestNewSkillDownloaded_RejectsMissingFields(t *testing.T) {
	type clearFn func(*SkillDownloadedInput)
	cases := []struct {
		name      string
		wantField string
		clear     clearFn
	}{
		{"missing cli version", "client.version", func(i *SkillDownloadedInput) { i.CLIVersion = "" }},
		{"missing namespace", "skill.namespace", func(i *SkillDownloadedInput) { i.Namespace = "" }},
		{"missing name", "skill.name", func(i *SkillDownloadedInput) { i.Name = "" }},
		{"missing version", "skill.version", func(i *SkillDownloadedInput) { i.Version = "" }},
		{"missing digest", "skill.digest", func(i *SkillDownloadedInput) { i.Digest = "" }},
		{"missing registry", "skill.registry", func(i *SkillDownloadedInput) { i.Registry = "" }},
		{"missing oci_ref", "skill.oci_ref", func(i *SkillDownloadedInput) { i.OCIRef = "" }},
		{"missing command", "source.command", func(i *SkillDownloadedInput) { i.Command = "" }},
		{"missing trigger", "source.trigger", func(i *SkillDownloadedInput) { i.Trigger = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := validInput()
			tc.clear(&in)
			_, err := NewSkillDownloaded(in)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			fre, ok := err.(*FieldRequiredError)
			if !ok {
				t.Fatalf("expected *FieldRequiredError, got %T: %v", err, err)
			}
			if fre.Field != tc.wantField {
				t.Errorf("FieldRequiredError.Field = %q, want %q", fre.Field, tc.wantField)
			}
		})
	}
}

func TestEvent_NeverContainsForbiddenSubstrings(t *testing.T) {
	hostname, _ := os.Hostname()
	home := os.Getenv("HOME")
	t.Setenv("SKILL_SECRET", "should-not-leak-12345")

	// Seed the input with values that do NOT contain any of the forbidden
	// substrings; the test proves the producer does not silently inject them.
	in := validInput()

	evt, err := NewSkillDownloaded(in)
	if err != nil {
		t.Fatalf("NewSkillDownloaded: %v", err)
	}
	body, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	got := string(body)

	forbidden := []string{
		"/Users/",
		"\\\\",
		"should-not-leak-12345",
	}
	if hostname != "" {
		forbidden = append(forbidden, hostname)
	}
	if home != "" {
		forbidden = append(forbidden, home)
	}

	for _, f := range forbidden {
		if strings.Contains(got, f) {
			t.Errorf("body unexpectedly contains forbidden substring %q\nbody: %s", f, got)
		}
	}
}
