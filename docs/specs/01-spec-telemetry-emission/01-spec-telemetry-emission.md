# 01-spec-telemetry-emission.md

## Introduction/Overview

The `skills-oci` CLI needs to report when a skill artifact is successfully downloaded so the skills-platform collector can power adoption dashboards. This spec implements the **producer side** of the wire contract in [`docs/telemetry-data-contract.md`](../../telemetry-data-contract.md): one `skill.downloaded` event emitted per successful pull, sent best-effort and non-blocking over HTTP, with local buffering on failure. The corresponding collector-side contract lives in [`skills-platform/docs/telemetry-data-contract.md`](https://github.com/liatrio/skills-platform/blob/main/docs/telemetry-data-contract.md) and is reference-only for this spec.

**Primary goal:** make every successful `add` or `install` pull emit one well-formed `skill.downloaded` event to the configured collector, without ever delaying or failing the user-facing command, and without ever growing telemetry state unbounded on disk.

## Goals

- Emit exactly one `skill.downloaded` event per successful skill pull, conforming byte-for-byte to the envelope and field rules in `docs/telemetry-data-contract.md` (`schema_version: 1`).
- Make emission non-blocking and bounded: the HTTP call has a hard 2-second timeout and runs after the user-facing operation succeeds; failures never fail the command.
- Provide a reliable local buffer (`pending.ndjson` under `os.UserCacheDir()`) that captures failed sends, drains up to 50 events per successful send, preserves order, and is capped at 1 MB (oldest-line eviction).
- Honor the three configuration env vars (`SKILLS_OCI_TELEMETRY`, `SKILLS_OCI_TELEMETRY_ENDPOINT`, `SKILLS_OCI_TELEMETRY_TOKEN`) with `on` as the opt-out default and compiled-in `-ldflags` defaults for endpoint and token.
- Guarantee the "never sent" list: no paths, hostnames, file contents, credentials, identifiers, or environment variables (other than the explicit telemetry config) can leak into an event, verified by tests.

## User Stories

- **As a platform maintainer**, I want every successful skill pull from a real CLI build to produce a `skill.downloaded` event with the resolved version and digest, so that I can see real adoption signal in the dashboard without instrumenting consumers manually.
- **As a CLI user**, I want telemetry to be invisible to my command experience — never slowing my `add` or `install`, never making it fail, never logging noisy errors — so that the feature is something I forget exists.
- **As a privacy-conscious user**, I want to disable telemetry with a single env var (`SKILLS_OCI_TELEMETRY=off`) and have that take effect immediately, with zero network calls when off, so that I trust the tool.
- **As an oncall engineer for the collector**, I want the CLI's retry/buffer behavior to be safe and bounded — idempotent via `event_id`, capped at 1 MB on disk, and never retrying on `4xx` — so that a collector outage cannot turn into a retry storm or fill user disks.
- **As a contributor adding a new event type later**, I want the event-construction code to treat the envelope as canonical and the payload as pluggable, so that adding `skill.removed` or `skill.invoked` is an additive change with no breaking edits.

## Demoable Units of Work

### Unit 1: Event envelope and `skill.downloaded` payload (offline, in-process)

**Purpose:** Build the data model and serializer that turns a successful `PullResult` plus runtime context into a JSON body that conforms to the wire contract, with no network involvement. This is the foundation everything else builds on; it must be correct in isolation before any HTTP code is written.

**Functional Requirements:**

- The system shall provide a `telemetry.Event` Go type whose JSON marshaling produces a body that validates against the schema described in `docs/telemetry-data-contract.md` §"Wire shape" and §"Field rules" for `event_type: skill.downloaded`.
- The system shall populate `schema_version: 1`, `event_type: "skill.downloaded"`, and a client-generated ULID for `event_id` on every event.
- The system shall set `occurred_at` to the current UTC time formatted as RFC 3339 with second precision and a `Z` suffix (e.g. `2026-05-18T17:22:00Z`).
- The system shall populate `client` with `name: "skills-oci"`, the CLI version (matching `skills-oci --version`), `runtime.GOOS`, and `runtime.GOARCH`.
- The system shall populate `actor` with `kind: "anonymous"` and no other fields for this iteration.
- The system shall populate the `skill` payload with `namespace`, `name`, `version`, `digest` (`sha256:<hex>`), `registry`, and `oci_ref`, derived from the `oci.PullResult` and the original user-supplied reference.
- The system shall populate `source.command` with the cobra subcommand name (`add` or `install`) and `source.trigger` with `user` or `manifest`.
- The system shall reject construction of an event with any missing required field by returning an error to the caller — this is a producer bug and must surface in tests, not at runtime.

**Proof Artifacts:**

- Test: `pkg/telemetry/event_test.go` includes a golden-file test that marshals a fixed `Event` and asserts byte-equality with `testdata/event-skill-downloaded.json`, demonstrating that the envelope matches the documented wire shape exactly.
- Test: a table-driven test asserts that `event_id` matches the ULID regex `^[0-9A-HJKMNP-TV-Z]{26}$` and that `occurred_at` matches `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`, demonstrating contract-conformant ID and timestamp formats.
- Test: a "never sent" test asserts that the JSON body never contains substrings from a forbidden list (full paths, hostnames, env-var values other than the telemetry config) seeded by the test, demonstrating the privacy guarantee for this layer.

### Unit 2: Best-effort HTTP transport with timeout, non-blocking emission, and config

**Purpose:** Wire the event into an HTTP `POST /v1/events` call governed by `SKILLS_OCI_TELEMETRY{,_ENDPOINT,_TOKEN}`, with a 2-second hard timeout, non-blocking emission relative to the user-facing command, and clear handling of `2xx` / `4xx` / `5xx` / network errors per the contract.

**Functional Requirements:**

- The system shall read `SKILLS_OCI_TELEMETRY`, `SKILLS_OCI_TELEMETRY_ENDPOINT`, and `SKILLS_OCI_TELEMETRY_TOKEN` once at CLI startup and cache the resolved configuration.
- The system shall treat `SKILLS_OCI_TELEMETRY=off` as the only off value; any other value (including unset, empty, or arbitrary strings) leaves emission on.
- The system shall ship compiled-in defaults for `ENDPOINT` and `TOKEN` via `-ldflags`, with placeholder values until the hosted collector is stood up (see Open Questions); env-var overrides take precedence over compiled defaults.
- The system shall `POST` the event body to `<endpoint>` with `Content-Type: application/json` and `Authorization: Bearer <token>`.
- The system shall enforce a 2-second hard timeout (via `context.WithTimeout`) on the HTTP round trip; timeouts are treated as transient failures (buffered for retry).
- The system shall ensure emission never blocks the user-facing command return: emission begins after the success path completes, and the command must not visibly wait beyond the timeout — verified by a test that asserts the command path returns within a documented bound.
- The system shall treat `2xx` as success, `4xx` as a producer bug (drop the event, write one line to a debug log, never retry), and `5xx` or network/timeout errors as transient (buffered — see Unit 3).
- The system shall ensure that when telemetry is off, no HTTP call is initiated and no buffer file is opened.

**Proof Artifacts:**

- Test: integration test against a `httptest.Server` asserting that one successful pull (simulated) results in exactly one `POST /v1/events` with the expected headers and a body matching the Unit 1 golden, demonstrating end-to-end wire conformance.
- Test: a `4xx` server response causes the event to be dropped without retry and without buffer growth; a `5xx` response causes the event to land in the buffer file — demonstrating the retry policy contract.
- Test: with `SKILLS_OCI_TELEMETRY=off`, no HTTP call is made and no buffer file is created (verified by an `httptest.Server` that fails the test if hit, plus a filesystem check), demonstrating opt-out is honored at the earliest possible point.
- Test: with a server that sleeps 5 seconds, the client returns to the caller within ~2s (timeout) and the event is buffered, demonstrating the timeout bound.
- CLI: running a real `add` against a local registry with `SKILLS_OCI_TELEMETRY_ENDPOINT` pointed at a local debug HTTP echo server prints the received body — demonstrates end-to-end on a developer machine.

### Unit 3: Local buffer (`pending.ndjson`): append, cap, flush, ordering

**Purpose:** Make failed sends survive the process boundary so transient collector outages don't lose events, while guaranteeing the buffer never grows unbounded on disk and replay is safe via idempotency.

**Functional Requirements:**

- The system shall persist failed events as one JSON object per line in `<UserCacheDir>/skills-oci/telemetry/pending.ndjson` (using Go's `os.UserCacheDir()` for platform-correct paths on macOS, Linux, and Windows).
- The system shall create the buffer file and its parent directories on first write with `0700` directory mode and `0600` file mode (no other users can read pending events).
- The system shall enforce a hard 1 MB cap on the buffer file; when a new write would exceed the cap, the oldest line is dropped before appending, so the buffer never grows past the cap.
- The system shall, on every successful send, drain up to 50 buffered events in FIFO order before returning, preserving insertion order across successful and failed sends.
- The system shall preserve `event_id` exactly as originally generated when re-sending buffered events, so the collector's `(client_name, event_id)` dedupe makes replays safe.
- The system shall never block the user-facing command on buffer I/O beyond the 2s emission deadline; buffer writes happen on the same non-blocking emission goroutine as the HTTP call.
- The system shall tolerate a corrupt/truncated trailing line (last line missing newline or invalid JSON) by skipping it; earlier well-formed lines must still drain.

**Proof Artifacts:**

- Test: write 100 events of ~1 KB each to a forced-failure transport; assert that `pending.ndjson` stays under 1 MB and that the *most recent* events are retained (oldest evicted), demonstrating the cap and eviction policy.
- Test: with a server that fails 3 times then succeeds, assert that the 4th send drains the 3 buffered events in original order (verified by `event_id` sequence on the server), demonstrating order preservation and replay correctness.
- Test: a buffer file containing 60 well-formed lines drains exactly 50 on one successful send; the remaining 10 stay in the file for the next call, demonstrating the per-flush cap.
- Test: a buffer file whose last line is truncated mid-JSON loads the earlier lines successfully and drops the bad trailing line without erroring, demonstrating corruption tolerance.
- Test: file permissions on the created `pending.ndjson` are `0600` and its parent dir is `0700` on Unix (skipped on Windows), demonstrating the privacy posture for the on-disk buffer.

### Unit 4: Wiring into the pull path — `add` and `install`

**Purpose:** Connect the telemetry module to the actual success branch of `pkg/oci/pull.go`, with command/trigger context flowing in from `cmd/add.go` and `cmd/install.go` (TUI and plain paths alike). One pull = one event; no events for failures, no-ops, or commands that don't pull.

**Functional Requirements:**

- The system shall emit exactly one `skill.downloaded` event from the success branch of `oci.Pull`, after extraction completes and before the user-facing command returns its result to the user.
- The system shall not emit events on failed pulls (any error path in `oci.Pull`), dry-runs (none today, but reserved), commands that don't pull (`verify`, `remove`, `clean`, `push`, `register`, `collection`), or `install` no-ops where a skill is already present and no network fetch occurred.
- The `cmd/add` path shall pass `source.command = "add"` and `source.trigger = "user"`.
- The `cmd/install` path shall pass `source.command = "install"` and `source.trigger = "manifest"` for each skill it pulls; `install` shall emit N events for N pulled skills, not one summary.
- The system shall route the user-supplied original reference (which may be a short form) through to the event so `oci_ref` is the fully-qualified expanded form actually pulled.
- The system shall not introduce new direct dependencies in `pkg/oci/pull.go` on `cmd/` code; the wiring uses a callback or context value so the pull package stays cobra-free.

**Proof Artifacts:**

- Test: a `pkg/oci/pull_test.go` integration test against a local in-memory OCI registry (`oras-go` test helper or a `httptest`-backed fake) plus an `httptest.Server` collector asserts that a successful pull produces exactly one event with the expected `oci_ref`, `digest`, and `source.command`.
- Test: a failed pull (registry returns 404 or extraction fails) produces zero events on the collector, demonstrating the success-only guarantee.
- Test: an `install` run that pulls 3 missing skills emits 3 events with `source.trigger: "manifest"`; a re-run where all 3 are already present emits 0 events, demonstrating the no-event-on-cache-hit rule.
- CLI: running `skills-oci add ghcr.io/myorg/skills/example:1.0.0` against a real collector debug endpoint shows one event arriving with the resolved digest — end-to-end demo of the integration.

## Non-Goals (Out of Scope)

1. **Other event types**: `skill.removed`, `skill.invoked`, `install` summary events, and any non-pull lifecycle events are deferred. The envelope is built to accept new `event_type` values additively without a schema bump, but no producer code is written for them in this spec.
2. **Persistent user config / `config telemetry off` subcommand**: a future CLI subcommand may persist opt-out to user config, but env-var-only control is the v1 surface.
3. **Non-anonymous `actor.kind`**: `github_user` / `service_account` and the associated `id_hash` field are reserved per the contract but not implemented here.
4. **Collector / dashboard / storage**: everything beyond the wire is owned by skills-platform and is reference-only here.
5. **Custom event batching**: this spec does not bundle multiple events into one HTTP body. One event = one POST. The 50-events-per-send drain reuses the existing single-event endpoint sequentially.
6. **Retries with exponential backoff inside one command run**: failures buffer to disk and flush on the *next* successful call; we do not loop-retry within the same command.
7. **Metrics/observability for telemetry itself**: no Prometheus-style counters of "events emitted / dropped" inside the CLI. The collector is the source of truth for received counts.
8. **Schema versioning beyond v1**: this spec produces `schema_version: 1` only; future bumps are a separate change.
9. **Encrypted-at-rest buffer file**: the buffer holds non-PII per the contract; `0600` perms are the only protection.

## Design Considerations

No UI/UX design requirements. The feature is invisible by design — no new flags, no new TUI screens, no new log lines in the user-facing output path. The only user-visible surface is the absence of behavior change when telemetry is on, and the env var documented in `README.md` for opt-out.

## Repository Standards

Implementation must follow the patterns already established in this repository:

- **Module layout**: new code lives under `pkg/telemetry/` mirroring the existing `pkg/oci/` and `pkg/skill/` package style (small, focused, no cobra dependencies in `pkg/`).
- **Cobra commands stay in `cmd/`**: command-level wiring (env-var read, command/trigger context) belongs in `cmd/`, with `pkg/telemetry/` receiving structured arguments — same separation as `cmd/add.go` ↔ `pkg/oci/pull.go`.
- **No new direct deps unless needed**: Go's `net/http`, `context`, `encoding/json`, `os`, and `time` are sufficient for transport, config, and serialization. A ULID library is the only new direct dependency expected (see Technical Considerations).
- **Errors**: use `fmt.Errorf("...: %w", err)` wrapping, matching the rest of the codebase.
- **Tests**: table-driven `_test.go` files alongside the code, using `httptest` and `t.TempDir()` for I/O. The existing repo has no test infrastructure for OCI integration; this spec introduces lightweight HTTP-only tests, no real OCI dependency.
- **Build-time injection**: defaults injected via `-ldflags` in `.github/workflows/release.yml`, following the same pattern as `main.version`.
- **Commit style**: short imperative subject, matching recent log style (e.g., `add telemetry emission for skill.downloaded`).
- **License/copyright**: no new file headers required (the repo does not use per-file headers).

## Technical Considerations

- **ULID library**: client-side `event_id` generation uses a maintained Go ULID implementation. The leading candidate is `github.com/oklog/ulid/v2` — a single-purpose, well-maintained library that produces lexicographically sortable, time-prefixed 26-char ULIDs matching the spec referenced by the wire contract. Adding it brings ~no transitive deps. Justify in PR if a different lib is chosen.
- **Non-blocking emission model**: the simplest correct design is to launch a single goroutine per emission *after* the user-facing operation has returned its result, with the goroutine bounded by a `context.WithTimeout(2s)`. The CLI process must wait for that goroutine before exiting (otherwise a quick `add` would race against in-flight emission) — a small `sync.WaitGroup` or equivalent in `cmd/root.go`'s post-Execute path handles this. The contract permits the visible command output to land first; the process exits *after* emission settles (success, failure-buffered, or timeout).
- **Time source**: use `time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)` to honor the contract's second-precision requirement and avoid leaking sub-second timing.
- **HTTP client**: a single package-level `*http.Client` with `Timeout: 2 * time.Second` is sufficient. Do not reuse the OCI registry's auth client — telemetry must not pick up registry credentials.
- **Buffer file format**: NDJSON with `\n` terminators. Reading is line-by-line; writing appends; eviction is implemented by reading the file, dropping the first line, and rewriting (acceptable cost given the 1 MB cap). Use `O_APPEND` for the common append path; eviction takes the read-modify-write slow path only when the cap is exceeded.
- **Concurrency**: only the `install` command pulls multiple skills, and the existing code path is sequential. Telemetry can therefore assume one event at a time within a process; no buffer-file locking is required for in-process safety, but flock-style locking against concurrent CLI invocations is out of scope (cross-process races at most cause one duplicate write, which is safe under collector dedupe).
- **Latest standards alignment**:
  - **ULID spec** (living document) — `event_id` is a ULID; `oklog/ulid/v2` is the current reference Go implementation.
  - **RFC 3339 / ISO 8601** — `occurred_at` is RFC 3339 UTC, second precision, `Z` suffix; Go's `time.RFC3339` matches exactly.
  - **Best-effort telemetry pattern** — current guidance for CLI telemetry (e.g., `gh`, `kubectl`, `cargo`) consistently uses: opt-out env var, short timeout, no PII, persistent local buffer. Our design follows this directly and does not introduce novel patterns.
  - No tension between repository patterns and external guidance identified.

## Security Considerations

- **No new secrets in source tree**: the compiled-in `TOKEN` is treated as anti-abuse (per the collector's contract) and is injected via `-ldflags` at release time, *not* committed to this repo. CI must continue to load it from a release-time secret.
- **No credential leakage**: the telemetry HTTP client must not share state with `pkg/oci/auth.go` — verified by code review and by the "never sent" test (Unit 1).
- **No PII**: the contract enumerates what is never sent (paths, hostnames, env values, credentials, identifiers). Unit 1's "never sent" test enforces this for the body; Unit 2's "off" test enforces it at the transport layer (no calls when off).
- **Proof artifacts**: the golden file under `testdata/event-skill-downloaded.json` is synthetic — it must contain only made-up `namespace/name/digest` values, never real customer or registry data. CI commit hooks may scrub if a real digest leaks in.
- **Buffer file permissions**: `0600` file / `0700` parent, enforced and tested on Unix. Windows ACLs follow `os.UserCacheDir()`'s default per-user behavior.
- **Token in env**: when a user overrides `SKILLS_OCI_TELEMETRY_TOKEN`, the value lives in their shell env only and is not logged or buffered. Verify by grep in tests that the buffer file never contains the token substring.
- **Forward-looking**: when `actor.kind` graduates to non-anonymous, the hashing of identifiers (SHA-256 of the raw value, raw value never sent) belongs in this package and will be added under a separate spec.

## Success Metrics

1. **Wire conformance**: 100% of generated event bodies in CI pass JSON Schema validation against the collector's `event-v1.json` (vendored or fetched in CI to keep contracts in lockstep). Target: zero `4xx` rejections in any week of post-launch operation attributable to producer bugs.
2. **Performance budget**: emission adds < 5 ms wall-clock to the user-visible command return on the happy path (measured with the local test collector), and never adds more than 2 s in the worst case. Target: P99 user-visible overhead ≤ 50 ms when network is healthy.
3. **Reliability**: under a simulated 1-hour collector outage with one pull every 5 minutes, all 12 events land in the collector within 2 minutes of recovery, with zero data loss and zero duplicates (verified by `event_id` set equality).
4. **Disk safety**: under an indefinite collector outage with continuous pulls, `pending.ndjson` size never exceeds 1 MB (verified by stress test).
5. **Opt-out adoption ease**: `SKILLS_OCI_TELEMETRY=off skills-oci add ...` produces zero network calls and zero buffer-file mutations, verified end-to-end.

## Open Questions

1. **Compiled-in `ENDPOINT` and `TOKEN` defaults**: the collector is not yet stood up. What placeholder behavior do we want until then — keep the defaults empty (which means "telemetry effectively off in stock builds until a release populates them") or hard-code a known-bad value (which means stock builds emit-and-fail-quietly into the buffer)? Decision affects whether stock binaries emit anything at all pre-collector-launch. Tentative recommendation: empty defaults → treat empty endpoint as off, ship CI/release pipeline ready to inject real values on the day the collector stands up.
2. **CI verification of `event-v1.json` lockstep**: should this repo vendor a copy of `event-v1.json` from `skills-platform`, or fetch it at CI time from a pinned commit? The collector contract says CI fails on drift; this is the producer-side mirror. Tentative recommendation: vendor with a `go generate`-driven update script, so the schema is reviewable in PRs.
3. **Debug log destination for `4xx` drops**: the contract says "a single line is written to the CLI's debug log." We do not currently have a debug log surface. Should we add a `--debug-telemetry` flag, write to `stderr` only when an env var is set, or log to a sidecar file? Tentative recommendation: write to `<UserCacheDir>/skills-oci/telemetry/last-error.log` (single-line, overwrite-on-write), so it never reaches the user's terminal but is reachable for diagnosis.
