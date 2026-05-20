# Task 04 Proofs — Wire emission into oci.Pull, add, install

## Task Summary

This task connects the previously-isolated `pkg/telemetry` package to the
real pull path. Approach:

- `pkg/oci/pull.go` now accepts an `Emitter` (interface
  `SkillDownloadEmitter`), `Command`, `Trigger`, and `CLIVersion` on
  `PullOptions`. On the success branch — after extraction completes — the
  emitter is invoked once with a populated `SkillDownloadInfo`.
- `pkg/oci` stays free of any `cmd/` or `pkg/telemetry` dependency; the
  adapter (`cmd.SkillEmitterAdapter`) is the only file that knows both
  types.
- `cmd/root.go` constructs a process-scoped `*telemetry.Emitter` and stashes
  it in `cmd.Context()`. `cmd.ExecuteWithWait` (used by `main.go`) calls
  `emitter.Wait()` after `Execute()` returns — even on error — so a quick
  subcommand never races a goroutine.
- `cmd/add.go` plumbs the emitter with `command="add", trigger="user"`.
  `cmd/install.go` + `pkg/tui/load/model.go` plumb it with
  `command="install", trigger="manifest"` and never emit for cache-hit
  skills.

## What This Task Proves

- A successful `oci.Pull` produces exactly one event with the expected
  `source.command`, `source.trigger`, `skill.namespace`, `skill.name`,
  `skill.version`, `skill.digest`, `skill.registry`, and `oci_ref`.
- A failed pull (404 registry) produces zero events.
- `install` over a manifest with 3 missing skills emits 3 events with
  `trigger="manifest"`; a second run where all 3 are present emits 0.
- The plain and TUI paths produce identical event sets for the same input
  state.
- `EmitterFromContext(emptyContext)` returns nil; the adapter and emitter
  tolerate nil end-to-end.

## Evidence Summary

- `go test ./...` → all packages pass: cmd (4 tests), pkg/oci (3 tests),
  pkg/telemetry (29 tests across event/config/transport/buffer/emitter).
- `go vet ./...` is clean.
- `go build ./...` is clean.
- Helpers `cmd/testregistry_test.go` and `pkg/oci/pull_telemetry_test.go`
  build minimal OCI Distribution Spec-compatible fake registries via
  `httptest.NewServer` (no live network).

## Artifact: oci.Pull emits exactly one event on success

**What it proves:** Pull's success branch calls the emitter once with the
contract's required fields populated from the resolved manifest.

**Command:**

```bash
go test ./pkg/oci/... -run TestPull_EmitsOneEventOnSuccess -v
```

**Result summary:** PASS — server-backed pull → emitter receives one
`SkillDownloadInfo{Command:"add", Trigger:"user", Name:"example-skill",
Version:"1.0.0", Registry:<httptest host>, OCIRef:<host/repo:tag>,
Namespace:"liatrio-labs", Digest:"sha256:..."}`.

## Artifact: oci.Pull emits zero events on failure

**What it proves:** A registry returning 404 surfaces an error from Pull
and never invokes the emitter.

**Command:**

```bash
go test ./pkg/oci/... -run TestPull_EmitsZeroEventsOnFailure -v
```

**Result summary:** PASS.

## Artifact: N events for N pulls; zero on cache hit

**What it proves:** `install` over a 3-skill `skills.json` emits 3 events
on first run; a re-run where all directories are present emits 0
(cache-hit no-op).

**Command:**

```bash
go test ./cmd/... -run TestInstall_EmitsPerPulledSkill_AndZeroOnCacheHit -v
```

**Result summary:** PASS — round 1: 3 installed / 3 events / all with
`(install, manifest)`. Round 2: 3 skipped / 0 events.

## Artifact: TUI/plain parity

**What it proves:** The TUI code path (`load.LoadSkills` via `startLoad`)
and the `--plain` code path (`runInstallPlain` via the same
`load.LoadSkills`) produce byte-identical event sets given the same
manifest and registry state.

**Command:**

```bash
go test ./cmd/... -run TestInstall_PlainAndTUIParity -v
```

**Result summary:** PASS.

## Artifact: Whole-project build + vet + tests are clean

**Command:**

```bash
go build ./...  && go vet ./...  && go test ./...
```

**Result summary:** all of the above exit 0.

```text
?   	github.com/salaboy/skills-oci	[no test files]
ok  	github.com/salaboy/skills-oci/cmd	0.704s
ok  	github.com/salaboy/skills-oci/pkg/oci	0.451s
?   	github.com/salaboy/skills-oci/pkg/skill	[no test files]
ok  	github.com/salaboy/skills-oci/pkg/telemetry	3.368s
```

## Reviewer Conclusion

The success-branch emission is in place end-to-end, the failure and
cache-hit branches are correctly silent, and the package-boundary
constraint (no cobra or telemetry imports in `pkg/oci`) holds: the
adapter type in `cmd/root.go` is the only translator between
`oci.SkillDownloadInfo` and `telemetry.SkillDownloadedInput`. Both the
plain and TUI paths emit identical event sets.
