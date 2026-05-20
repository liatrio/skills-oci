# 01-validation-telemetry-emission.md

Validation report for [`01-spec-telemetry-emission.md`](./01-spec-telemetry-emission.md), produced from the implementation on branch `jk/telemetry`.

## 1) Executive Summary

- **Overall:** **PASS** (no gates tripped).
- **Implementation Ready:** **Yes.** All five parent tasks deliver passing tests, the proof artifacts for every Functional Requirement exist and execute, and `go test ./...` + `go vet ./...` are clean on `jk/telemetry`.
- **Key metrics:**
  - Functional Requirements verified: **30 / 30** (100%).
  - Proof artifact tests passing: **42 / 42** named tests across the producer stack (envelope, transport, buffer, emitter, pull-wiring, schema lockstep, cmd-level parity).
  - Files changed (5 telemetry commits) vs. Relevant Files in `01-tasks-telemetry-emission.md`: **all in-scope**; supporting files (proofs, `pkg/tui/load/model.go`, `cmd/testregistry_test.go`) are linked to in-scope core changes via commit messages and task references.

## 2) Coverage Matrix

### Functional Requirements

| Requirement (abbreviated) | Status | Evidence |
| --- | --- | --- |
| Unit 1 — `Event` marshals to wire-conformant body | Verified | `TestEvent_GoldenBody` PASS; `pkg/telemetry/testdata/event-skill-downloaded.json` byte-equal to marshaled `Event`; commit `37f089a`. |
| Unit 1 — `schema_version: 1`, `event_type: "skill.downloaded"`, ULID `event_id` | Verified | Golden body shows `"schema_version":1,"event_type":"skill.downloaded"`; `TestEvent_IDAndTimestampFormats` PASS (50-iteration table over ULID + RFC 3339 regexes). |
| Unit 1 — `occurred_at` RFC 3339 UTC second precision | Verified | `TestEvent_IDAndTimestampFormats` regex `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`; `pkg/telemetry/event.go` uses `time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)`. |
| Unit 1 — `client.{name,version,os,arch}` populated | Verified | Golden body, `TestEvent_GoldenBody` PASS; `pkg/telemetry/event.go` injects `runtime.GOOS`/`runtime.GOARCH` and CLI version. |
| Unit 1 — `actor.kind: "anonymous"` | Verified | Golden body `"actor":{"kind":"anonymous"}`. |
| Unit 1 — `skill` payload fields | Verified | Golden body includes `namespace/name/version/digest/registry/oci_ref`; `pkg/oci/pull.go:235-236` builds `SkillDownloadInfo` from `PullResult` and forwards to emitter. |
| Unit 1 — `source.{command,trigger}` populated | Verified | `cmd/add.go:119-120` sets `Command:"add", Trigger:"user"`; `pkg/tui/load/model.go:238-239` sets `Command:"install", Trigger:"manifest"`. |
| Unit 1 — Reject construction with missing required field | Verified | `TestNewSkillDownloaded_RejectsMissingFields` PASS across 9 sub-tests (`missing_cli_version` through `missing_trigger`). |
| Unit 2 — Read env vars once at startup | Verified | `TestConfig_LdflagFallback` PASS; `pkg/telemetry/config.go` uses `sync.Once`-guarded cached `Config`. |
| Unit 2 — `off` is the only off value | Verified | `TestEmit_OffMakesNoNetworkCall`, `TestEmitter_OffIsNoOp` PASS; config code accepts only literal `off`. |
| Unit 2 — Compiled-in defaults via `-ldflags` | Verified | `TestConfig_LdflagFallback` PASS; `.github/workflows/release.yml:41-42` injects `pkg/telemetry.DefaultEndpoint`/`DefaultToken`. |
| Unit 2 — `POST /v1/events` with bearer auth | Verified | `TestEmit_PostsExpectedBody` PASS — handler asserts method, path, `Content-Type: application/json`, `Authorization: Bearer <token>`, and golden-equal body. |
| Unit 2 — 2-second hard timeout | Verified | `TestEmit_TimeoutBounded` PASS (3.00s server sleep, returns within bound with `*TransientError`). |
| Unit 2 — Non-blocking relative to command return | Verified | `cmd/root.go:81` `defer emitter.Wait()`; `TestEmitter_WaitBlocksUntilGoroutineFinishes` and `TestEmitter_WaitTimeoutSemantics` PASS. |
| Unit 2 — `2xx`/`4xx`/`5xx` classification | Verified | `TestEmit_PostsExpectedBody`, `TestEmit_4xxDropsNoRetry`, `TestEmit_5xxReturnsTransient` PASS. |
| Unit 2 — Off → no HTTP, no buffer file | Verified | `TestEmit_OffMakesNoNetworkCall`, `TestEmitter_OffIsNoOp` PASS; `httptest` handler fails on hit, no file under redirected cache. |
| Unit 3 — Persist failed events to `pending.ndjson` | Verified | `TestBuffer_AppendThenRead`, `TestEmitter_TransientRoutesToBuffer` PASS; `pkg/telemetry/buffer.go` writes NDJSON. |
| Unit 3 — Buffer file `0600`, parent `0700` | Verified | `TestBuffer_FilePermissions` PASS (Unix-only via `runtime.GOOS` guard). |
| Unit 3 — 1 MB cap, oldest-line eviction | Verified | `TestBuffer_CapAndEviction` PASS; appends 100 ~1 KB lines, asserts size ≤ 1 MB and newest retained. |
| Unit 3 — Drain up to 50 in FIFO order per success | Verified | `TestBuffer_DrainsInOrderOnSuccess`, `TestBuffer_DrainCapPerCall` PASS. |
| Unit 3 — Preserve `event_id` on re-send | Verified | `TestBuffer_PreservesEventID` PASS; buffer is byte-identity for stored lines. |
| Unit 3 — Tolerate truncated trailing line | Verified | `TestBuffer_TruncatedTrailingLineSkipped` PASS. |
| Unit 4 — One event per successful pull from `oci.Pull` success branch | Verified | `TestPull_EmitsOneEventOnSuccess` PASS; `pkg/oci/pull.go:235-236` fires the callback once after extraction. |
| Unit 4 — No events on failure / cache-hit / non-pull commands | Verified | `TestPull_EmitsZeroEventsOnFailure`, `TestInstall_EmitsPerPulledSkill_AndZeroOnCacheHit` PASS. |
| Unit 4 — `add` → `command=add, trigger=user` | Verified | `cmd/add.go:119-120`; golden body confirms shape. |
| Unit 4 — `install` → `command=install, trigger=manifest`, N events for N pulls | Verified | `pkg/tui/load/model.go:238-239`; `TestInstall_EmitsPerPulledSkill_AndZeroOnCacheHit` PASS. |
| Unit 4 — `oci_ref` is the fully-qualified expanded form | Verified | `PullResult.Reference` plumbed through `SkillDownloadInfo` to `telemetry.SkillDownloadedInput`; covered by `TestPull_EmitsOneEventOnSuccess` body assertion. |
| Unit 4 — No new cobra dep in `pkg/oci/pull.go` | Verified | `grep -n 'cobra' pkg/oci/pull.go` returns no hits; emitter contract is the narrow `SkillDownloadEmitter` interface defined in `pkg/oci/pull.go:41-43`. |
| Cross — Release-time ldflags injection | Verified | `.github/workflows/release.yml:35-42` reads `secrets.TELEMETRY_ENDPOINT`/`TOKEN`, falls back to empty. |
| Cross — Schema lockstep CI gate | Verified | `.github/workflows/ci.yml:33` runs `TestGolden_ValidatesAgainstSchema -v`; test PASS locally. |
| Cross — README opt-out documentation | Verified | `README.md:331-380` "Telemetry" section: what's sent, never-sent list, opt-out one-liner, env-var table, link to wire contract. |
| Cross — TUI/`--plain` parity for emission | Verified | `TestInstall_PlainAndTUIParity` PASS. |

### Repository Standards

| Standard | Status | Evidence |
| --- | --- | --- |
| Module layout (`pkg/telemetry/` mirrors `pkg/oci/`/`pkg/skill/`, no cobra in `pkg/`) | Verified | `grep -n cobra pkg/telemetry/` → no hits; `grep -n cobra pkg/oci/pull.go` → no hits. |
| Cobra wiring stays in `cmd/` | Verified | Emitter constructed in `cmd/root.go:86`, fetched via `cmd.Context()` in `cmd/add.go:63` and `cmd/install.go:35`. |
| Errors wrap with `fmt.Errorf("...: %w", err)` | Verified | Spot-checks in `pkg/telemetry/transport.go`, `pkg/telemetry/buffer.go`, `pkg/oci/pull.go`. |
| TDD with table-driven tests / `httptest` / `t.TempDir()` | Verified | `_test.go` files all use these patterns (e.g., `TestNewSkillDownloaded_RejectsMissingFields` is table-driven over 9 missing-field cases; transport tests use `httptest.NewServer`; buffer tests use `t.TempDir()`). |
| Build-time injection mirrors `main.version` | Verified | `release.yml` extends the existing `-X main.version=...` block with `-X .../pkg/telemetry.DefaultEndpoint=...` and `-X .../pkg/telemetry.DefaultToken=...`. |
| Commit style — Conventional commits | Verified | All 5 commits use `feat(telemetry): ...` subjects (`37f089a`, `52b0a5b`, `8935303`, `ad25419`, `9bfe260`). |
| `go vet ./...` and `go test ./...` clean | Verified | Both run with exit 0 on the validation host. |

### Proof Artifacts

| Task | Proof Artifact | Status | Result |
| --- | --- | --- | --- |
| 1.0 | `TestEvent_GoldenBody` | Verified | PASS — byte-equal to `testdata/event-skill-downloaded.json`. |
| 1.0 | `TestEvent_IDAndTimestampFormats` | Verified | PASS — 50-iteration table. |
| 1.0 | `TestEvent_NeverContainsForbiddenSubstrings` | Verified | PASS — body contains none of `/Users/`, hostname, `$HOME`, or seeded env values. |
| 1.0 | `TestNewSkillDownloaded_RejectsMissingFields` | Verified | PASS — 9 sub-cases. |
| 1.0 | `pkg/telemetry/testdata/event-skill-downloaded.json` exists | Verified | Synthetic body using `liatrio-labs/example-skill`, dummy digest. |
| 2.0 | `TestEmit_PostsExpectedBody` | Verified | PASS — method/path/headers/body all asserted. |
| 2.0 | `TestEmit_4xxDropsNoRetry` | Verified | PASS — last-error.log written, no retry. |
| 2.0 | `TestEmit_5xxReturnsTransient` | Verified | PASS — `*TransientError`. |
| 2.0 | `TestEmit_TimeoutBounded` | Verified | PASS — 3.00s elapsed within bound. |
| 2.0 | `TestEmit_OffMakesNoNetworkCall` | Verified | PASS — `httptest` handler not invoked. |
| 2.0 | `TestConfig_LdflagFallback` | Verified | PASS. |
| 3.0 | `TestBuffer_CapAndEviction` | Verified | PASS — newest retained, oldest evicted, size ≤ 1 MB. |
| 3.0 | `TestBuffer_DrainsInOrderOnSuccess` | Verified | PASS — FIFO `event_id` sequence. |
| 3.0 | `TestBuffer_DrainCapPerCall` | Verified | PASS — 50-per-call cap. |
| 3.0 | `TestBuffer_TruncatedTrailingLineSkipped` | Verified | PASS. |
| 3.0 | `TestBuffer_FilePermissions` | Verified | PASS — `0o600`/`0o700`. |
| 3.0 | `TestBuffer_PreservesEventID` | Verified | PASS. |
| 4.0 | `TestPull_EmitsOneEventOnSuccess` | Verified | PASS — exact one POST with expected `oci_ref`, `digest`, `source`. |
| 4.0 | `TestPull_EmitsZeroEventsOnFailure` | Verified | PASS — registry 404 → 0 events. |
| 4.0 | `TestInstall_EmitsPerPulledSkill_AndZeroOnCacheHit` | Verified | PASS — combines both cases (N for N missing, 0 for cache hit). |
| 4.0 | `TestInstall_PlainAndTUIParity` | Verified | PASS — plain and TUI paths emit identical event sets. |
| 4.0 | `TestEmitter_WaitTimeoutSemantics` | Verified | PASS — root exit blocks on in-flight emission. |
| 5.0 | `pkg/telemetry/testdata/event-v1.json` | Verified | Present with `pkg/telemetry/testdata/README.md` sourcing note. |
| 5.0 | `TestGolden_ValidatesAgainstSchema` | Verified | PASS — golden body validates against vendored JSON Schema. |
| 5.0 | `.github/workflows/release.yml` ldflags diff | Verified | Lines 41-42 inject `pkg/telemetry.DefaultEndpoint`/`DefaultToken`. |
| 5.0 | `.github/workflows/ci.yml` lockstep step | Verified | Line 33 runs `TestGolden_ValidatesAgainstSchema`. |
| 5.0 | `README.md` Telemetry section | Verified | Lines 331-380; opt-out via `SKILLS_OCI_TELEMETRY=off`. |
| 5.0 | Proof markdown files `01-task-01-proofs.md` … `01-task-05-proofs.md` | Verified | All five present in `docs/specs/01-spec-telemetry-emission/01-proofs/`. |

## 3) Validation Issues

| Severity | Issue | Impact | Recommendation |
| --- | --- | --- | --- |
| LOW | `go.mod:36,38` marks `github.com/oklog/ulid/v2` and `github.com/santhosh-tekuri/jsonschema/v5` as `// indirect`, but `pkg/telemetry/event.go:10` imports `oklog/ulid/v2` directly (non-test). `go mod tidy` should split it into the direct `require` block. | Cosmetic; build and tests are unaffected. | Run `go mod tidy` and commit the resulting `go.mod`/`go.sum` change so the direct-vs-indirect classification matches actual import sites. |

No CRITICAL, HIGH, or MEDIUM findings.

## 4) Evidence Appendix

### Commits analyzed

```text
9bfe260 feat(telemetry): release-time defaults, schema lockstep CI, README opt-out docs
ad25419 feat(telemetry): wire skill.downloaded emission into add and install
8935303 feat(telemetry): add pending.ndjson buffer with cap and ordered drain
52b0a5b feat(telemetry): add HTTP transport and env-var config
37f089a feat(telemetry): add skill.downloaded event model
```

Each commit message references the parent task it implements (Tasks 1.0 → 5.0 in `01-tasks-telemetry-emission.md`); commits land in dependency order (event model → transport → buffer → wiring → release/CI/docs).

### Commands executed

```bash
$ go vet ./...                                   # exit 0, no output
$ go test ./...                                  # all packages PASS
$ go test -count=1 ./cmd/... ./pkg/telemetry/... ./pkg/oci/...
ok  github.com/salaboy/skills-oci/cmd          0.640s
ok  github.com/salaboy/skills-oci/pkg/telemetry 3.536s
ok  github.com/salaboy/skills-oci/pkg/oci      0.243s

$ go test -count=1 ./pkg/telemetry/... -run TestGolden_ValidatesAgainstSchema -v
--- PASS: TestGolden_ValidatesAgainstSchema (0.00s)
ok  github.com/salaboy/skills-oci/pkg/telemetry 0.316s

$ go test -count=1 ./cmd/... ./pkg/telemetry/... ./pkg/oci/... \
    -run 'TestPull_Emits|TestInstall_|TestRoot_Waits|TestEmitter_|TestBuffer_|TestEmit_|TestEvent_|TestNewSkillDownloaded|TestConfig_|TestGolden_' -v
# 42 named tests, all PASS (full list captured in this report's Coverage Matrix).
```

### File-integrity classification

- **Core implementation files (all in `Relevant Files`):** `pkg/telemetry/event.go`, `pkg/telemetry/config.go`, `pkg/telemetry/transport.go`, `pkg/telemetry/buffer.go`, `pkg/telemetry/emitter.go`, `pkg/telemetry/doc.go`, `pkg/telemetry/schema_test.go`, `pkg/telemetry/testdata/{event-skill-downloaded.json,event-v1.json}`, `pkg/oci/pull.go`, `cmd/root.go`, `cmd/add.go`, `cmd/install.go`, `pkg/tui/load/model.go`, `.github/workflows/release.yml`, `.github/workflows/ci.yml`, `README.md`, `go.mod`, `go.sum`.
- **Supporting verification files (linked to in-scope tasks via commit messages and `01-tasks-telemetry-emission.md`):** `pkg/telemetry/{event,config,transport,buffer,emitter}_test.go`, `pkg/telemetry/testdata/README.md`, `pkg/oci/pull_telemetry_test.go`, `cmd/{install,root}_test.go`, `cmd/testregistry_test.go`, `pkg/tui/add/model.go`, `main.go` (signature-propagation only), `docs/specs/01-spec-telemetry-emission/01-proofs/*.md`, `docs/specs/01-spec-telemetry-emission/01-tasks-telemetry-emission.md`.
- **No unmapped out-of-scope core changes detected.**

### Security check (GATE F)

`grep -RInE 'sk-[A-Za-z0-9]{20,}|ghp_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16}|Bearer [A-Za-z0-9._-]{20,}|password\s*[:=]\s*"[^"]+"' docs/specs/01-spec-telemetry-emission/ pkg/telemetry/testdata/` returns zero matches. The golden body uses synthetic values (`liatrio-labs/example-skill`, dummy 64-char hex digest); proof markdown files reference tokens only as documentation placeholders.

## Gate Outcomes

- **GATE A (CRITICAL/HIGH blocker):** PASS — no CRITICAL/HIGH findings.
- **GATE B (no `Unknown` in Coverage Matrix):** PASS.
- **GATE C (Proof Artifacts accessible and functional):** PASS — every named test runs and passes; every fixture/file referenced exists.
- **GATE D (file integrity):** PASS — D1 satisfied (no unmapped out-of-scope core changes); D2 satisfied (supporting files linked through commits and task notes); D3 N/A.
- **GATE E (repository standards):** PASS — Conventional commits, cobra-free `pkg/`, table-driven tests, `httptest` and `t.TempDir()` only, no live registry calls.
- **GATE F (no real credentials in proofs):** PASS.

## What Comes Next

Implementation is ready for merge. Recommended pre-merge steps:

1. Run `go mod tidy` to clean up the LOW-severity `// indirect` classification on `oklog/ulid/v2` (and `santhosh-tekuri/jsonschema/v5` if it stays test-only the indirect tag will return). Commit alongside.
2. Final human code review of the five-commit stack on `jk/telemetry`.
3. Open PR into `main`; the new `.github/workflows/ci.yml` schema-lockstep step will gate future drift automatically.

---

**Validation Completed:** 2026-05-19
**Validation Performed By:** Claude (Opus 4.7) via `/sdd-skill-poc` Phase 4
