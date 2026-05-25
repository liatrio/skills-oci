# Checkpoint — Cobra-level smoke test for `catalog sync` (follow-up to Spec 02)

**Status:** ready for `/SDD-1-generate-spec` to formalize.
**Estimated effort:** 30–60 minutes of implementation; small, self-contained spec.
**Prior spec:** [`02-spec-skills-catalog-vendoring.md`](./02-spec-skills-catalog-vendoring.md) — shipped on `feat/catalog-vendoring` branch, 4 commits ahead of `main`.
**Validation that surfaced this gap:** [`02-validation-skills-catalog-vendoring.md`](./02-validation-skills-catalog-vendoring.md) — MEDIUM issue #1.

## Background — what's already shipped

`skills-oci catalog sync` is implemented and working end-to-end. The orchestrator in `pkg/catalog/sync.go` has 13 tests with fake `Fetcher` / `LicenseReader` / `Pusher` interfaces. A real-world `--dry-run` against `anthropics/skills` produces the spec-committed `--plain` output and exits cleanly. All four parent tasks for Spec 02 are marked `[x]`.

## Background — the gap

Spec 02's Demoable Unit 4 listed `cmd/catalog_sync_test.go` as a proof artifact. That file was not produced; the rationale (`docs/specs/02-spec-skills-catalog-vendoring/02-proofs/02-task-04-proofs.md`) cites the orchestrator-level fakes as covering the same behaviors.

**What's not covered:** the cobra wiring in `cmd/catalog_sync.go::runCatalogSync` — specifically:

1. Flag parsing (`--dry-run`, `--only`, `--catalog`, `--lock`, `--concurrency`, `--allow-missing-license`)
2. Config + flag precedence resolution (flag > project config > built-in default for `concurrency` and `allow-missing-license`)
3. Mapping orchestrator results to exit codes (0 / 1 / 2)
4. `--plain` output formatting through the full status callback chain

A regression in any of those between flag parsing and `catalog.Sync(...)` invocation would slip past every existing test.

## Proposed approach — mirror the `catalog add` pattern

`cmd/catalog_add.go` already solves this exact problem. It splits the cobra handler into two functions:

- `runCatalogAdd(cmd *cobra.Command, args []string) error` — the production wrapper that reads flags and constructs the production adapters
- `runCatalogAddWithDeps(ctx, out, opts, cfg, resolver, fetcher) error` — the testable core that takes interfaces

The companion test file (`cmd/catalog_add_test.go`) calls `runCatalogAddWithDeps` directly with `fakeResolver` / `fakeFetcher` test doubles. Coverage on `runCatalogAddWithDeps` is 91.5%; the production wrapper at 0% is exercised by the real-world proof artifact.

**Apply the same split to `runCatalogSync`.** Move the orchestrator call into a new `runCatalogSyncWithDeps(ctx, out, opts, fet, lic, push, emitter) error` (or similar). Tests inject fakes; production wraps with `scmFetcherAdapter{}`, `skillLicenseReader{}`, `ociPusherAdapter{}`, and the real telemetry emitter.

## Test cases to cover

Adapt the existing fakes from `pkg/catalog/sync_test.go` (they're package-private; copy them into `cmd/catalog_sync_test.go` or extract to a shared `internal/catalogtest/` helper if a second consumer ever shows up).

| Test | What it asserts |
| --- | --- |
| `TestRunCatalogSync_HappyPathExit0` | 2-entry catalog, all succeed, lockfile written, exit code 0, stdout matches the spec's `--plain` golden |
| `TestRunCatalogSync_FailureExit1` | 2-entry catalog, one entry's fetcher returns error → exit 1, lockfile written, failed entry's prior lock state preserved (or absent if no prior) |
| `TestRunCatalogSync_LockWriteFailureExit2` | Simulate lockfile-write failure (set `--lock` to a path whose parent dir is read-only, or use a fake `Lockfile` writer if you abstract that) → exit code 2 |
| `TestRunCatalogSync_DryRunNoLockWritten` | `--dry-run` set; pusher never called; lockfile not created |
| `TestRunCatalogSync_OnlyFilterRespected` | `--only foo,bar`; unnamed entries don't appear in the result; output reflects only the named entries |
| `TestRunCatalogSync_ConcurrencyFromConfig` | `.skills-oci.yaml` sets `concurrency: 2`; with no `--concurrency` flag, orchestrator runs with 2 workers (assert via a channel-gated fake) |
| `TestRunCatalogSync_AllowMissingLicenseFromConfig` | `.skills-oci.yaml` sets `allow_missing_license: true`; entry with empty license succeeds (instead of failing by default) |
| `TestRunCatalogSync_PlainOutputGolden` | Capture stdout, assert byte-equality with `cmd/testdata/catalog-sync-plain.golden` |

That's 8 tests. The first three are the load-bearing exit-code assertions the validation report flagged.

## Files to touch

| File | Change |
| --- | --- |
| `cmd/catalog_sync.go` | Refactor: extract `runCatalogSyncWithDeps` taking `Fetcher`, `LicenseReader`, `Pusher`, telemetry emitter as parameters. `runCatalogSync` becomes a 5-line wrapper that builds production adapters and calls through. The flag parsing and config-resolution logic moves into `parseSyncOpts(cmd, cfg) syncOpts` for testability. |
| `cmd/catalog_sync_test.go` | New. The 8 tests above. Re-use `fakeFetcher` / `fakeLicenseReader` / `fakePusher` from `pkg/catalog/sync_test.go` (copy or extract). |
| `cmd/testdata/catalog-sync-plain.golden` | New. Captured plain output for the happy-path test. |
| `docs/specs/02-spec-skills-catalog-vendoring/02-tasks-skills-catalog-vendoring.md` | Update 4.16's deferral note to point at the new spec/branch. |
| `docs/specs/02-spec-skills-catalog-vendoring/02-validation-skills-catalog-vendoring.md` | Re-run SDD-4; the MEDIUM issue should clear. |

## Repo state references

Run these to orient a future session:

```sh
git log --oneline main..feat/catalog-vendoring
# 6d039ab feat(catalog): add catalog sync subcommand, catalog.synced telemetry, and CI integration
# e405cfc feat(catalog): add catalog add subcommand and pkg/config loader
# c99356c feat(scm): add GitHub URL parser, tag resolver, and shallow SHA fetcher
# e0580c5 feat(catalog): add v1 data contract types, validator, and atomic writers

# Key files to read first:
# 1. cmd/catalog_add.go      — the pattern to mirror (runCatalogAddWithDeps)
# 2. cmd/catalog_add_test.go — the test style to mirror (fakeResolver/fakeFetcher)
# 3. cmd/catalog_sync.go     — the file to refactor
# 4. pkg/catalog/sync_test.go — existing fakes to re-use (fakeFetcher, fakeLicenseReader, fakePusher)
```

## Definition of done

- All 8 tests pass.
- `runCatalogSyncWithDeps` coverage ≥ 90%.
- The existing `--plain` golden in `02-proofs/catalog-sync-plain.txt` still matches what the new golden test asserts.
- `go test ./...` passes repo-wide, no regressions.
- `gofmt` / `go vet` clean.
- SDD-4 re-run on the spec downgrades the cobra-smoke-test MEDIUM issue to Verified.
- Commit follows conventional-commit prefix `test(catalog):` or `refactor(catalog):` depending on the shape of the change.

## What this is NOT

- **Not** a full TUI implementation. The `pkg/tui/catalog` deferral from Spec 02 stays deferred until adoption signals justify the work.
- **Not** a real-registry push proof. That separate MEDIUM follow-up (run `catalog sync` against `localhost:5000`, capture `catalog-lock-after-sync.txt`) is a doc-only deliverable; it doesn't add code coverage and can land independently.
- **Not** a change to spec/contract behavior. Pure test-side and minor refactor of the cobra handler.

## How to invoke

Future session:

```
/SDD-1-generate-spec

Feature: Cobra-level smoke test for skills-oci `catalog sync`.

Context: A checkpoint file at docs/specs/02-spec-skills-catalog-vendoring/checkpoint-cobra-smoke-test-followup.md describes the gap, the proposed approach (mirror cmd/catalog_add.go's runCatalogAddWithDeps split), and the 8 test cases that should be covered. Use it as the primary input — do not re-elicit requirements that are already settled there.

Working directory: /Users/zachjorgensen/Documents/Liatrio/Repos/skills-projects/skills-oci
Prior spec for conventions: docs/specs/02-spec-skills-catalog-vendoring/

User directive: lightweight, single demoable unit. Branch off feat/catalog-vendoring (or a fresh branch from main if that one has merged).
```

Expected spec output: ~1 page, one Demoable Unit, one parent task, ~8 sub-tasks. Should not need a clarifying-questions round.
