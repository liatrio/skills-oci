package telemetry

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// TestGolden_ValidatesAgainstSchema is the producer-side mirror of the
// collector's schema-lockstep CI gate. It compiles testdata/event-v1.json and
// asserts that the canonical golden body validates against it.
func TestGolden_ValidatesAgainstSchema(t *testing.T) {
	c := jsonschema.NewCompiler()
	schemaFile, err := os.ReadFile("testdata/event-v1.json")
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if err := c.AddResource("file://event-v1.json", bytes.NewReader(schemaFile)); err != nil {
		t.Fatalf("add schema resource: %v", err)
	}
	schema, err := c.Compile("file://event-v1.json")
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}

	body, err := os.ReadFile("testdata/event-skill-downloaded.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("unmarshal golden: %v", err)
	}
	if err := schema.Validate(doc); err != nil {
		t.Fatalf("golden does not validate: %v", err)
	}
}

// TestGolden_ValidatesAgainstSchema_RejectsBadEvent is a sanity check that the
// schema actually constrains the body — without it, a permissive schema would
// silently pass any golden.
func TestSchema_RejectsBadEvent(t *testing.T) {
	c := jsonschema.NewCompiler()
	schemaFile, err := os.ReadFile("testdata/event-v1.json")
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if err := c.AddResource("file://event-v1.json", bytes.NewReader(schemaFile)); err != nil {
		t.Fatalf("add schema resource: %v", err)
	}
	schema, err := c.Compile("file://event-v1.json")
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	cases := []map[string]any{
		{ /* empty doc, all required fields missing */ },
		{
			"schema_version": 2, // wrong version
			"event_id":       "01HM3K9QZX7N8T6BVCQ2KX3RZA",
			"event_type":     "skill.downloaded",
			"occurred_at":    "2026-05-18T17:22:00Z",
		},
		{
			"schema_version": 1,
			"event_id":       "TOOSHORT",
			"event_type":     "skill.downloaded",
			"occurred_at":    "2026-05-18T17:22:00Z",
		},
	}
	for i, doc := range cases {
		if err := schema.Validate(doc); err == nil {
			t.Errorf("case %d: expected validation error, got nil", i)
		}
	}
}
