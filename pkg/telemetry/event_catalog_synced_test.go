package telemetry

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

func validCatalogInput() CatalogSyncedInput {
	return CatalogSyncedInput{
		CLIVersion:   "0.1.0",
		Name:         "create-skill",
		InternalRef:  "ghcr.io/liatrio/skills/create-skill",
		Tag:          "v1.0.0",
		Commit:       "bc6708cbbc37adb919157f04d31e601e68f4b9c2",
		Digest:       "sha256:abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234",
		UpstreamRepo: "anthropics/skills",
		Outcome:      "synced",
		Trigger:      "user",
	}
}

func TestCatalogSynced_GoldenBody(t *testing.T) {
	pinSeams(t,
		time.Date(2026, 5, 22, 18, 30, 14, 0, time.UTC),
		"01HM3K9QZX7N8T6BVCQ2KX3RZB",
		"darwin", "arm64",
	)

	evt, err := NewCatalogSynced(validCatalogInput())
	if err != nil {
		t.Fatalf("NewCatalogSynced: %v", err)
	}

	got, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	want, err := os.ReadFile("testdata/event-catalog-synced.json")
	if err != nil {
		t.Fatalf("ReadFile golden: %v", err)
	}
	want = bytes.TrimRight(want, "\n")
	if !bytes.Equal(got, want) {
		t.Errorf("marshalled body mismatch.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestCatalogSynced_FailedOutcomeOmitsDigest(t *testing.T) {
	// digest is empty for failed outcomes; should NOT error.
	in := validCatalogInput()
	in.Outcome = "failed"
	in.Digest = ""

	evt, err := NewCatalogSynced(in)
	if err != nil {
		t.Fatalf("NewCatalogSynced failed: %v", err)
	}
	if evt.Catalog.Digest != "" {
		t.Errorf("Digest = %q, want empty", evt.Catalog.Digest)
	}
	if evt.Catalog.Outcome != "failed" {
		t.Errorf("Outcome = %q, want failed", evt.Catalog.Outcome)
	}
}

func TestCatalogSynced_SkippedOutcomeOmitsDigest(t *testing.T) {
	in := validCatalogInput()
	in.Outcome = "skipped"
	in.Digest = ""

	evt, err := NewCatalogSynced(in)
	if err != nil {
		t.Fatalf("NewCatalogSynced skipped: %v", err)
	}
	if evt.Catalog.Outcome != "skipped" {
		t.Errorf("Outcome = %q, want skipped", evt.Catalog.Outcome)
	}
}

func TestCatalogSynced_SyncedOutcomeRequiresDigest(t *testing.T) {
	in := validCatalogInput()
	in.Digest = ""
	_, err := NewCatalogSynced(in)
	var fre *FieldRequiredError
	if !errors.As(err, &fre) || fre.Field != "catalog.digest" {
		t.Errorf("expected FieldRequiredError on catalog.digest, got %v", err)
	}
}

func TestCatalogSynced_InvalidOutcomeRejects(t *testing.T) {
	in := validCatalogInput()
	in.Outcome = "completed" // not in the enum
	_, err := NewCatalogSynced(in)
	var ioe *InvalidOutcomeError
	if !errors.As(err, &ioe) {
		t.Fatalf("expected InvalidOutcomeError, got %v", err)
	}
	if ioe.Outcome != "completed" {
		t.Errorf("InvalidOutcomeError.Outcome = %q, want %q", ioe.Outcome, "completed")
	}
}

func TestCatalogSynced_MissingRequiredFields(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*CatalogSyncedInput)
		wantField string
	}{
		{"missing cli version", func(i *CatalogSyncedInput) { i.CLIVersion = "" }, "client.version"},
		{"missing name", func(i *CatalogSyncedInput) { i.Name = "" }, "catalog.name"},
		{"missing internal ref", func(i *CatalogSyncedInput) { i.InternalRef = "" }, "catalog.internal_ref"},
		{"missing tag", func(i *CatalogSyncedInput) { i.Tag = "" }, "catalog.tag"},
		{"missing commit", func(i *CatalogSyncedInput) { i.Commit = "" }, "catalog.commit"},
		{"missing upstream repo", func(i *CatalogSyncedInput) { i.UpstreamRepo = "" }, "catalog.upstream_repo"},
		{"missing outcome", func(i *CatalogSyncedInput) { i.Outcome = "" }, "catalog.outcome"},
		{"missing trigger", func(i *CatalogSyncedInput) { i.Trigger = "" }, "source.trigger"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := validCatalogInput()
			tt.mutate(&in)
			_, err := NewCatalogSynced(in)
			var fre *FieldRequiredError
			if !errors.As(err, &fre) {
				t.Fatalf("expected FieldRequiredError, got %v", err)
			}
			if fre.Field != tt.wantField {
				t.Errorf("FieldRequiredError.Field = %q, want %q", fre.Field, tt.wantField)
			}
		})
	}
}

func TestCatalogSynced_EventTypeIsCatalogSynced(t *testing.T) {
	evt, err := NewCatalogSynced(validCatalogInput())
	if err != nil {
		t.Fatalf("NewCatalogSynced: %v", err)
	}
	if evt.EventType != EventTypeCatalogSynced {
		t.Errorf("EventType = %q, want %q", evt.EventType, EventTypeCatalogSynced)
	}
	if evt.Catalog == nil {
		t.Error("Catalog payload is nil")
	}
	if evt.Skill != nil {
		t.Error("Skill payload should be nil for catalog.synced events")
	}
	if evt.Source.Command != "catalog sync" {
		t.Errorf("source.command = %q, want %q", evt.Source.Command, "catalog sync")
	}
}
