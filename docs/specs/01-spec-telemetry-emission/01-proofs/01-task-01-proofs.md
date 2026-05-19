# Task 01 Proofs — Event model and `skill.downloaded` envelope

## Task Summary

This task introduces the pure, IO-free core of `pkg/telemetry`: a Go `Event`
type whose JSON marshaling produces a body byte-equal to the wire shape in
`docs/telemetry-data-contract.md`. A `NewSkillDownloaded(input)` constructor
generates a ULID `event_id`, RFC-3339-second `occurred_at`, and validates that
every required input string is non-empty. No HTTP, no filesystem.

## What This Task Proves

- The `Event` struct marshals to a body byte-equal to the spec's canonical
  example (golden fixture).
- `event_id` is a 26-character Crockford-base32 ULID and `occurred_at` is
  RFC 3339 UTC with second precision and a `Z` suffix.
- Constructing an event with any missing required field returns a typed
  `*FieldRequiredError` naming the offending field.
- The marshaled body never contains forbidden substrings (paths, hostname,
  `$HOME`, environment-variable values).
- `actor.kind` is fixed to `anonymous`, `schema_version` is `1`, and
  `event_type` is `skill.downloaded`.

## Evidence Summary

- `go test ./pkg/telemetry/... -run "TestEvent|TestNewSkillDownloaded" -v`
  → all four test functions pass (12 sub-tests in total).
- `go vet ./pkg/telemetry/...` is clean.
- Dependency added: `github.com/oklog/ulid/v2 v2.1.1` (single direct dep).

## Artifact: Golden-body byte-equality test

**What it proves:** `json.Marshal(*Event)` is byte-equal to the documented
wire shape, so the producer cannot silently drift from the contract.

**Why it matters:** This is the lockstep guarantee between this CLI and the
collector. Any field reorder, rename, or addition will fail this test
immediately and force the contract to be updated deliberately.

**Command:**

~~~bash
go test ./pkg/telemetry/... -run TestEvent_GoldenBody -v
~~~

**Result summary:** PASS. Marshaled output equals
`pkg/telemetry/testdata/event-skill-downloaded.json` byte-for-byte.

~~~
=== RUN   TestEvent_GoldenBody
--- PASS: TestEvent_GoldenBody (0.00s)
PASS
ok  	github.com/salaboy/skills-oci/pkg/telemetry
~~~

## Artifact: ULID + RFC 3339 format conformance

**What it proves:** Across 50 generated events, `event_id` always matches the
ULID regex and `occurred_at` always matches the contract's RFC 3339 second-
precision regex.

**Why it matters:** Idempotency on the collector depends on every `event_id`
being a valid ULID; analytics depend on parseable timestamps.

**Command:**

~~~bash
go test ./pkg/telemetry/... -run TestEvent_IDAndTimestampFormats -v
~~~

**Result summary:** PASS — all 50 iterations satisfy both regexes; sanity
assertions on `actor.kind`, `schema_version`, and `event_type` also pass.

## Artifact: Required-field validation

**What it proves:** `NewSkillDownloaded` returns a typed
`*FieldRequiredError` for every required input string when empty, naming the
offending JSON field (e.g. `skill.namespace`, `source.command`).

**Why it matters:** Producer bugs that would otherwise emit malformed events
are caught at the call site, not at the collector's `4xx` boundary.

**Command:**

~~~bash
go test ./pkg/telemetry/... -run TestNewSkillDownloaded_RejectsMissingFields -v
~~~

**Result summary:** PASS — 9 sub-tests, one per required field.

## Artifact: "Never sent" forbidden-substring guard

**What it proves:** The marshaled body does not contain `/Users/`, escaped
backslashes, the host's hostname, `$HOME`, or a seeded env-var value.

**Why it matters:** Enforces the §"What is NEVER sent" privacy guarantee at
the model layer, so a future refactor can't accidentally leak local context
into the JSON body.

**Command:**

~~~bash
go test ./pkg/telemetry/... -run TestEvent_NeverContainsForbiddenSubstrings -v
~~~

**Result summary:** PASS — no forbidden substring found in the body.

## Artifact: `go vet` is clean

**Command:**

~~~bash
go vet ./pkg/telemetry/...
~~~

**Result summary:** Exit 0, no findings.

## Reviewer Conclusion

The envelope and `skill.downloaded` payload are correct, deterministic
under deterministic seams, contract-conformant under real entropy, and
guarded against both producer bugs (missing fields) and privacy regressions
(forbidden substrings). The model is ready to be wired into the transport
layer in Task 02.
