package config

import (
	"bytes"
	"fmt"
	"os"
	"sort"

	"gopkg.in/yaml.v3"
)

// knownTopLevelKeys is the closed set of top-level keys the v1 contract
// recognizes. Anything else is forwarded-compatibility: warn-and-ignore.
var knownTopLevelKeys = map[string]struct{}{
	"catalog": {},
}

// Load parses .skills-oci.yaml bytes into a Config. Empty input returns
// the zero value (no error). Unknown top-level keys are logged to stderr
// and otherwise ignored so the contract can grow additively. Type
// mismatches on known keys (e.g. `concurrency: "four"`) reject with a
// field-named error. `catalog.concurrency`, when explicitly set, must be
// a positive int; zero or negative values reject. Absent values leave
// the zero default in place so the caller's precedence chain runs.
func Load(data []byte) (Config, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return Config{}, nil
	}

	// First pass: parse into an untyped map so we can detect unknown
	// top-level keys and distinguish "field absent" from "field set to
	// zero". Both are important for the forward-compat and validation
	// stories.
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Config{}, fmt.Errorf("parsing .skills-oci.yaml: %w", err)
	}
	warnUnknown(raw)

	if err := validateRaw(raw); err != nil {
		return Config{}, err
	}

	// Second pass: tolerant decode into the typed Config. By this point
	// we already know the values that matter are well-typed.
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing .skills-oci.yaml: %w", err)
	}
	return cfg, nil
}

// validateRaw inspects the untyped map and enforces type and value
// constraints on known fields. Returns field-named errors so callers
// can produce useful UX.
func validateRaw(raw map[string]any) error {
	catRaw, ok := raw["catalog"].(map[string]any)
	if !ok {
		// `catalog:` may be absent or `null`; both are fine.
		return nil
	}

	if v, present := catRaw["default_namespace"]; present {
		if _, isString := v.(string); !isString {
			return fmt.Errorf("catalog.default_namespace must be a string, got %T", v)
		}
	}
	if v, present := catRaw["allow_missing_license"]; present {
		if _, isBool := v.(bool); !isBool {
			return fmt.Errorf("catalog.allow_missing_license must be a bool, got %T", v)
		}
	}
	if v, present := catRaw["concurrency"]; present {
		n, isInt := v.(int)
		if !isInt {
			return fmt.Errorf("catalog.concurrency must be an integer, got %T", v)
		}
		if n <= 0 {
			return fmt.Errorf("catalog.concurrency must be positive, got %d", n)
		}
	}
	return nil
}

// warnUnknown writes one warning line per unknown top-level key.
// Sorted so the output is deterministic across runs (useful for tests).
func warnUnknown(raw map[string]any) {
	var unknown []string
	for k := range raw {
		if _, ok := knownTopLevelKeys[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	for _, k := range unknown {
		fmt.Fprintf(os.Stderr, "skills-oci: warning: unknown key %q in .skills-oci.yaml (ignored)\n", k)
	}
}
