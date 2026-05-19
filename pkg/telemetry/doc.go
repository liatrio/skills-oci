// Package telemetry implements the producer side of the skills-oci wire contract
// defined in docs/telemetry-data-contract.md. It emits one skill.downloaded event
// per successful pull, best-effort and non-blocking, with local NDJSON buffering
// on transient failure.
package telemetry
