// Package config loads the project-level .skills-oci.yaml that lives in
// the consumer repository alongside catalog.json. Settings are optional;
// absent file produces a zero-value Config that the caller layers
// flags and env vars on top of (see precedence chain in
// docs/skills-catalog-data-contract.md).
//
// Forward-compatibility: unknown top-level keys are logged to stderr and
// otherwise ignored so the contract can grow additively across minor
// versions. Type mismatches on known keys are rejected with field-named
// errors.
package config
