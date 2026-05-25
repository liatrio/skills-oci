# 03-spec-catalog-sync-cobra-smoke-test.md

## Introduction/Overview

`skills-oci catalog sync` is implemented and exercised end-to-end (13 orchestrator tests in `pkg/catalog/sync_test.go`, plus a real-world `--dry-run` proof against `anthropics/skills`). What it does **not** have is direct test coverage of the cobra handler in `cmd/catalog_sync.go::runCatalogSync` — flag parsing, config+flag precedence, exit-code mapping, and `--plain` output formatting all run only through the production wrapper today. A regression in any of those layers would slip past every existing test. This spec closes the gap by refactoring `runCatalogSync` to mirror the dependency-injected split already used by `cmd/catalog_add.go` and adding a focused cobra-level test suite. Source checkpoint: [`02-spec-skills-catalog-vendoring/checkpoint-cobra-smoke-test-followup.md`](../02-spec-skills-catalog-vendoring/checkpoint-cobra-smoke-test-followup.md).

## Goals

- Refactor `cmd/catalog_sync.go` to expose a `runCatalogSyncWithDeps(...)` core that accepts `Fetcher`, `LicenseReader`, `Pusher`, and telemetry emitter as injected dependencies, mirroring the `runCatalogAddWithDeps` pattern already in `cmd/catalog_add.go`.
- Add 8 cobra-level smoke tests in `cmd/catalog_sync_test.go` that cover exit-code semantics (0/1/2), flag→config precedence, `--dry-run` no-op behavior, `--only` filtering, and `--plain` golden output.
- Reach **≥90% line coverage** on the new `runCatalogSyncWithDeps` (matching the 91.5% coverage of `runCatalogAddWithDeps`).
- Downgrade the validation MEDIUM finding in [`02-validation-skills-catalog-vendoring.md`](../02-spec-skills-catalog-vendoring/02-validation-skills-catalog-vendoring.md) (issue #1) to "Verified" after re-running SDD-4.

## User Stories

- **As a future maintainer of `skills-oci`**, I want a cobra-level test for `catalog sync` so that I get a CI failure (not a silent production regression) the next time someone edits flag parsing, exit-code mapping, or the `--plain` status format.
- **As the reviewer of Spec 02's validation report**, I want the cobra-smoke-test MEDIUM issue closed so that the validation matrix reflects actual coverage instead of an orchestrator-level fakes argument-by-analogy.
- **As an end user running `catalog sync` in CI**, I want the documented exit-code contract (0 = clean, 1 = entry failure, 2 = lockfile-write failure) to be regression-tested so my CI gating logic stays trustworthy.

## Demoable Units of Work

### Unit 1: Cobra-level smoke test for `catalog sync`

**Purpose:** Replace the indirect coverage of the `catalog sync` cobra handler (currently inferred from orchestrator-level fakes) with direct, regression-trapping tests that exercise the full flag-parse → config-resolve → orchestrator-call → exit-code path. Refactor `runCatalogSync` to support dependency injection without changing observable production behavior.

**Functional Requirements:**
- The system shall expose a new exported-to-package function `runCatalogSyncWithDeps(ctx context.Context, out io.Writer, opts syncOpts, fet catalog.Fetcher, lic catalog.LicenseReader, push catalog.Pusher, emitter *telemetry.Emitter, cliVersion string) error` (final signature may vary; the principle is "interfaces in, no production adapters constructed inside").
- The system shall expose a new function `parseSyncOpts(cmd *cobra.Command, cfg config.Config) syncOpts` (or equivalent) that performs the flag-read + config-merge logic, callable from tests without invoking cobra's `Execute()`.
- The system shall keep the production `runCatalogSync` as a thin wrapper (≤10 lines) that constructs `scmFetcherAdapter{}`, `skillLicenseReader{}`, `ociPusherAdapter{}`, the real emitter, and forwards to `runCatalogSyncWithDeps`.
- The system shall preserve every existing observable behavior: byte-identical `--plain` output, identical exit-code semantics, identical flag names, identical telemetry events. No spec/contract changes.
- The system shall provide a test file `cmd/catalog_sync_test.go` with the following cases:
  1. `TestRunCatalogSync_HappyPathExit0` — 2-entry catalog, both fakes succeed, lockfile written, exit code 0, stdout matches golden.
  2. `TestRunCatalogSync_FailureExit1` — one fake fetcher returns error → exit code 1, lockfile still written, failed entry's prior lock state preserved (or absent on first run).
  3. `TestRunCatalogSync_LockWriteFailureExit2` — simulate lockfile-write failure (read-only parent dir, or fake lockfile writer) → exit code 2.
  4. `TestRunCatalogSync_DryRunNoLockWritten` — `--dry-run` set; pusher never invoked; lockfile not created.
  5. `TestRunCatalogSync_OnlyFilterRespected` — `--only foo,bar`; unnamed entries absent from result; output reflects only named entries.
  6. `TestRunCatalogSync_ConcurrencyFromConfig` — `.skills-oci.yaml` sets `concurrency: 2`; no `--concurrency` flag; orchestrator runs with 2 workers (channel-gated fake asserts the bound).
  7. `TestRunCatalogSync_AllowMissingLicenseFromConfig` — `.skills-oci.yaml` sets `allow_missing_license: true`; entry with empty license succeeds.
  8. `TestRunCatalogSync_PlainOutputGolden` — capture stdout, assert byte-equality with `cmd/testdata/catalog-sync-plain.golden`.
- The system shall include a golden file `cmd/testdata/catalog-sync-plain.golden` whose content matches the existing `02-proofs/catalog-sync-plain.txt` for the happy-path scenario (or is a deliberate, documented subset/restructure of it).
- The system shall support a `-update` flag (`go test -run TestRunCatalogSync_PlainOutputGolden -update`) that regenerates the golden file from current stdout, so future intentional `--plain` format changes do not require hand-editing.
- The system shall move the `os.Exit` call out of the testable core: `runCatalogSyncWithDeps` returns `(syncExitCode, error)` and the production `runCatalogSync` wrapper is the only caller of `os.Exit(int(code))`. Tests assert directly on the returned code without spawning subprocesses. Production exit behavior is byte-identical to today.
- The test suite shall reuse the existing `fakeFetcher` / `fakeLicenseReader` / `fakePusher` from `pkg/catalog/sync_test.go` — either by copying them into the new test file or by extracting to `internal/catalogtest/` if a second consumer materializes. (Default: copy. Don't add an extraction package until two consumers exist.)
- The system shall keep `go test ./...` passing repo-wide with no other regressions, `gofmt` clean, and `go vet ./...` clean.

**Proof Artifacts:**
- Test file: `cmd/catalog_sync_test.go` exists and contains the 8 named test functions, demonstrating direct cobra-handler coverage.
- Coverage report: `go test -cover -run TestRunCatalogSync ./cmd/...` shows ≥90% coverage on `runCatalogSyncWithDeps`, demonstrating the testable core is well-exercised.
- CLI run: `go test ./... | tail -5` shows all packages pass, demonstrating no regression elsewhere.
- Golden file: `cmd/testdata/catalog-sync-plain.golden` exists, demonstrating the `--plain` format is locked down.
- Validation re-run: SDD-4 output on Spec 02 reclassifies the cobra-smoke-test MEDIUM finding as Verified, demonstrating the originating gap is closed.

## Non-Goals (Out of Scope)

1. **No TUI work.** The `pkg/tui/catalog` deferral from Spec 02 stays deferred; the test suite covers only the `--plain` path. (TUI rendering is a presentation layer over the same orchestrator and inherits coverage transitively.)
2. **No real-registry push proof.** Capturing `catalog-lock-after-sync.txt` from a `localhost:5000` registry is a separate Spec 02 MEDIUM follow-up; it's a doc-only deliverable that adds no code coverage and lands independently.
3. **No spec or data-contract changes.** Flag names, exit-code semantics, telemetry event shapes, lockfile schema, and `--plain` output format are all frozen. This is pure test addition + refactor for testability.
4. **No new dependencies.** All test doubles are package-private struct fakes; no new imports beyond what's already used by `pkg/catalog/sync_test.go`.
5. **No extraction to `internal/catalogtest/` yet.** Copy the fakes into `cmd/catalog_sync_test.go`. Don't extract until a second consumer exists (rule of three).
6. **No coverage push on the production wrapper.** The 5-to-10-line `runCatalogSync` shim that constructs production adapters and calls `os.Exit` stays at 0% unit coverage — it's covered by the real-world `--dry-run` proof artifact from Spec 02. Matches the established `runCatalogAdd` precedent.

## Design Considerations

No specific design requirements identified. This is a test-and-refactor unit with no user-facing UI surface.

## Repository Standards

- **Strict TDD (mandatory per `CLAUDE.md`):** RED → GREEN → REFACTOR. Each of the 8 test functions must be committed in failing state (or the test must demonstrably fail before the refactor lands), then made pass by the refactor and any small follow-ups.
- **Test placement:** `cmd/catalog_sync_test.go` lives next to the file it tests (`cmd/catalog_sync.go`), matching the `cmd/catalog_add_test.go` precedent.
- **Pure functions at the core:** `parseSyncOpts` must have no IO and no network — flag-read + config-merge only, identical to `parseAddOpts` in shape.
- **One concern per package:** keep production adapters (`scmFetcherAdapter`, `skillLicenseReader`, `ociPusherAdapter`) in `catalog_sync.go`; the testable core in `runCatalogSyncWithDeps` accepts the interfaces directly.
- **Table-driven tests where appropriate:** the 8 tests above are distinct enough to be separate functions, but each test that has parameterized inputs (e.g., precedence variants) should use a table.
- **Test naming:** `TestRunCatalogSync_<scenario>` — matches the `TestRunCatalogAdd_<scenario>` style already in `catalog_add_test.go`.
- **Arrange-Act-Assert:** every test should be visually scannable as three sections.
- **Deterministic golden file:** `cmd/testdata/catalog-sync-plain.golden` must be byte-identical across runs; concurrent-worker scheduling that could reorder lines must be deterministically ordered in the fake (or the golden test must operate on a sorted-then-compared form).
- **Conventional commits:** `test(catalog): …` for the new tests, `refactor(catalog): …` for the dependency-injection split. Land as one or two commits, not eight.
- **Quality gates before commit:** `go test ./...`, `go vet ./...`, `gofmt -l .` (must be empty).
- **Update the Spec 02 task list:** task 4.16's deferral note in `docs/specs/02-spec-skills-catalog-vendoring/02-tasks-skills-catalog-vendoring.md` should point at this spec for the discharge.

## Technical Considerations

- **Mirror, don't reinvent.** The shape of the split is already in `cmd/catalog_add.go::runCatalogAddWithDeps` (line 123). Read that and replicate the pattern. Do not introduce a new dependency-injection idiom.
- **Exit-code testability (decided).** The current `runCatalogSync` calls `os.Exit` directly inside the handler (lines 99–106). The refactor moves `os.Exit` out: `runCatalogSyncWithDeps` returns `(syncExitCode, error)`; the production wrapper does `os.Exit(int(code))`. Tests assert directly on the returned code — no subprocess test infrastructure needed. Observable production behavior is unchanged.
- **Concurrency assertion in test 6.** `TestRunCatalogSync_ConcurrencyFromConfig` needs a fake `Fetcher` that gates on a buffered channel of size `expectedConcurrency` and asserts the peak in-flight count never exceeds it. Pattern: `inflight := atomic.Int32{}; max := atomic.Int32{}` in the fake, plus a small synchronization knob to keep all fetches simultaneously in-flight long enough to observe the cap.
- **Golden file maintenance (decided).** The golden file is regenerable via a `-update` flag: `go test -run TestRunCatalogSync_PlainOutputGolden -update` rewrites `cmd/testdata/catalog-sync-plain.golden` from current stdout. Standard Go pattern — declare `var updateGolden = flag.Bool("update", false, "regenerate golden files")` at the top of `catalog_sync_test.go` and gate the write on it. Adds ~5 lines; pays back the first intentional `--plain` format change.
- **Config plumbing in tests.** Tests need to inject a `config.Config` value without writing a `.skills-oci.yaml` to disk. Either pass `cfg` directly into `parseSyncOpts` (preferred) or stash it on `cmd.Context()` via the existing `configFromContext` accessor. Match whatever pattern `catalog_add_test.go` already uses.
- **Lockfile-write-failure simulation (decided).** Test 3 points `--lock` at a path under a `t.TempDir()` subdirectory chmod'd to `0500`. No new production interface. Caveat: `chmod 0500` does not reliably block writes when running as root in some CI containers; if the test flakes there, the fallback is to introduce a `catalog.LockWriter` interface and inject a fake — but only if flakiness materializes, not preemptively.
- **No live network in any test.** Every fetcher in the test suite must be a fake. The 13 existing orchestrator tests are the model.

## Security Considerations

No new security surface. Tests run entirely against in-memory fakes and temp directories. No credentials, tokens, or registry endpoints are involved. The golden file at `cmd/testdata/catalog-sync-plain.golden` contains synthetic test data (made-up entry names, digests, repos) — verify before commit that no production refs, real digests, or real upstream URLs leak in. Same convention as the existing `02-proofs/catalog-sync-plain.txt`.

## Success Metrics

1. **Test count:** 8 new `TestRunCatalogSync_*` functions exist in `cmd/catalog_sync_test.go` and all pass under `go test ./...`.
2. **Coverage:** `go test -cover ./cmd/...` reports ≥90% line coverage on `runCatalogSyncWithDeps`; the production-wrapper `runCatalogSync` at 0% is acceptable (matches `runCatalogAdd` precedent).
3. **No regressions:** `go test ./...` repo-wide passes; `go vet ./...` clean; `gofmt -l .` empty.
4. **Validation clears:** Re-running `/SDD-4-validate-spec-implementation` against Spec 02 downgrades the cobra-smoke-test MEDIUM finding to Verified.
5. **Observable behavior unchanged:** Manual `--dry-run` against `anthropics/skills` produces byte-identical `--plain` output to the existing `02-proofs/catalog-sync-plain.txt` (modulo timestamp/short-SHA values that vary by upstream state).
6. **Diff size:** Single PR / one-or-two commits; total LoC delta under ~400 lines (production refactor stays minimal; bulk of the diff is test code).

## Branching

This work stacks on **`feat/catalog-vendoring`** (currently 4 commits ahead of `main`, no PR yet). New commits land on the same branch and ship together — either in the same PR as the original catalog-vendoring work, or as a quick follow-up commit before that PR is opened. If `feat/catalog-vendoring` opens a PR before this work starts, revisit the policy at `/SDD-2` time.

## Open Questions

No open questions at this time. All four major design decisions (os.Exit-out-of-core refactor, branch policy, lockfile-fail test mechanism, golden -update flag) are locked in above.
