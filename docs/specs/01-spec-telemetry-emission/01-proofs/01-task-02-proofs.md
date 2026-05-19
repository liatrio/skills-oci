# Task 02 Proofs — HTTP transport with config and timeout

## Task Summary

This task adds the best-effort HTTP transport that turns an `Event` into a
`POST /v1/events` request, with a hard 2-second timeout, env-var
configuration (`SKILLS_OCI_TELEMETRY{,_ENDPOINT,_TOKEN}`), `-ldflags`
fallback defaults, and 2xx/4xx/5xx/timeout classification per the wire
contract.

## What This Task Proves

- The transport posts the contract-conformant body to `<endpoint>/v1/events`
  with `Content-Type: application/json` and `Authorization: Bearer <token>`.
- `LoadConfig` reads the three env vars exactly once, treats `off` as the
  only off value, and falls back to `-ldflags`-injected defaults when env
  vars are empty.
- 4xx → `*PermanentError` returned, no retry, single line written to
  `<UserCacheDir>/skills-oci/telemetry/last-error.log` with the status code
  and event id.
- 5xx → `*TransientError` returned (so the orchestrator can route to the
  NDJSON buffer in Task 03).
- Timeout: a 3-second handler sleep is bounded to ≤ 4.5 s wall-clock and
  returns `*TransientError` wrapping a deadline-exceeded cause.
- When telemetry is off, no HTTP call is made and no file is created under
  the (redirected) cache directory.

## Evidence Summary

- `go test ./pkg/telemetry/... -v` → 18 tests pass, 0 fail (incl. golden,
  config, transport, classify, raw-body).
- `go vet ./pkg/telemetry/...` is clean.
- No new direct deps beyond Task 01.

## Artifact: Full transport + config test suite

**What it proves:** Every functional requirement in spec Unit 2 has a
passing named test.

**Why it matters:** This is the wire-conformance proof end-to-end against an
`httptest.Server` — the only producer-side gap closed before integration in
Task 04 is the in-flight orchestration (Task 04 itself).

**Command:**

~~~bash
go test ./pkg/telemetry/... -v
~~~

**Result summary:** PASS — `TestEmit_PostsExpectedBody`,
`TestEmit_4xxDropsNoRetry`, `TestEmit_5xxReturnsTransient`,
`TestEmit_TimeoutBounded` (3.00 s elapsed, within the 4.5 s bound),
`TestEmit_OffMakesNoNetworkCall`, `TestClassifyHTTPStatus`,
`TestEmit_EmptyEndpointIsNoOp`, `TestEmit_EmitRawPreservesBody`,
`TestLoadConfig_EnvOverrides` (7 sub-tests), `TestConfig_LdflagFallback`.

~~~
=== RUN   TestEmit_PostsExpectedBody
--- PASS: TestEmit_PostsExpectedBody (0.00s)
=== RUN   TestEmit_4xxDropsNoRetry
--- PASS: TestEmit_4xxDropsNoRetry (0.00s)
=== RUN   TestEmit_5xxReturnsTransient
--- PASS: TestEmit_5xxReturnsTransient (0.00s)
=== RUN   TestEmit_TimeoutBounded
--- PASS: TestEmit_TimeoutBounded (3.00s)
=== RUN   TestEmit_OffMakesNoNetworkCall
--- PASS: TestEmit_OffMakesNoNetworkCall (0.00s)
=== RUN   TestEmit_EmptyEndpointIsNoOp
--- PASS: TestEmit_EmptyEndpointIsNoOp (0.00s)
PASS
ok  	github.com/salaboy/skills-oci/pkg/telemetry	3.350s
~~~

## Artifact: 4xx writes last-error.log

**What it proves:** A 4xx response leaves a forensic breadcrumb at
`<cacheDir>/last-error.log` containing the status code and event_id, while
returning `*PermanentError` to the caller and not retrying.

**Why it matters:** Resolves Open Question #3 from the spec — `4xx` drops
are diagnosable without polluting the user's terminal.

**Sub-test command:**

~~~bash
go test ./pkg/telemetry/... -run TestEmit_4xxDropsNoRetry -v
~~~

**Result summary:** PASS — server hit exactly once, `last-error.log` contains
both `status=400` and the generated event_id.

## Artifact: Empty endpoint short-circuit

**What it proves:** With an empty endpoint (the stock-build state before
release-time `-ldflags` populate it), `Emit` returns nil without touching
the network.

**Why it matters:** Resolves Open Question #1 from the spec — stock builds
are effectively off until the collector is stood up; no failure noise, no
buffer growth from never-deliverable events.

## Artifact: `go vet` clean

**Command:**

~~~bash
go vet ./pkg/telemetry/...
~~~

**Result summary:** Exit 0, no findings.

## Reviewer Conclusion

The transport layer correctly classifies HTTP outcomes, honors the 2-second
timeout, never blocks beyond the timeout, never retries 4xx, and is fully
off when the user opts out or when no endpoint is configured. Combined with
the Task 01 model, this completes the wire-conformance story. Task 03 will
add persistent buffering for the transient-failure path.
