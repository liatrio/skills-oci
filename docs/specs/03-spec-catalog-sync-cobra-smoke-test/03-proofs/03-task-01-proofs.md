# 03-task-01 Proofs — Cobra-level smoke test for `catalog sync` with DI refactor

Aggregated evidence for parent task 1.0 in [`03-tasks-catalog-sync-cobra-smoke-test.md`](../03-tasks-catalog-sync-cobra-smoke-test.md). Closes Spec 02 validation MEDIUM finding #1.

## Summary

- **8 new tests** in `cmd/catalog_sync_test.go` cover the full cobra-handler path: flag parsing, config→flag precedence, exit-code mapping (0/1/2), `--dry-run` no-op, `--only` filtering, and `--plain` golden output.
- **`runCatalogSyncWithDeps` coverage: 100%** (spec target ≥90%).
- **`parseSyncOpts` coverage: 100%**.
- **`runCatalogSync` production wrapper: 0%** — expected, matches the established `runCatalogAdd` precedent; covered by the real-world `--dry-run` proof in Spec 02.
- **Production behavior unchanged.** The refactor moves `os.Exit` out of the testable core and threads dependencies as parameters; the wrapper still calls `os.Exit(int(code))` with byte-identical semantics.
- **Repo-wide `go test ./...` clean.** `go vet` clean. `gofmt` clean for all files touched by this work.

## CLI Output

### Test run — all 8 tests pass

```text
=== RUN   TestRunCatalogSync_HappyPathExit0
--- PASS: TestRunCatalogSync_HappyPathExit0 (0.00s)
=== RUN   TestRunCatalogSync_FailureExit1
--- PASS: TestRunCatalogSync_FailureExit1 (0.00s)
=== RUN   TestRunCatalogSync_LockWriteFailureExit2
--- PASS: TestRunCatalogSync_LockWriteFailureExit2 (0.00s)
=== RUN   TestRunCatalogSync_DryRunNoLockWritten
--- PASS: TestRunCatalogSync_DryRunNoLockWritten (0.00s)
=== RUN   TestRunCatalogSync_OnlyFilterRespected
--- PASS: TestRunCatalogSync_OnlyFilterRespected (0.00s)
=== RUN   TestRunCatalogSync_ConcurrencyFromConfig
=== RUN   TestRunCatalogSync_ConcurrencyFromConfig/parseSyncOpts_picks_config
=== RUN   TestRunCatalogSync_ConcurrencyFromConfig/orchestrator_honors_cap
--- PASS: TestRunCatalogSync_ConcurrencyFromConfig (0.05s)
    --- PASS: TestRunCatalogSync_ConcurrencyFromConfig/parseSyncOpts_picks_config (0.00s)
    --- PASS: TestRunCatalogSync_ConcurrencyFromConfig/orchestrator_honors_cap (0.05s)
=== RUN   TestRunCatalogSync_AllowMissingLicenseFromConfig
=== RUN   TestRunCatalogSync_AllowMissingLicenseFromConfig/parseSyncOpts_picks_config
=== RUN   TestRunCatalogSync_AllowMissingLicenseFromConfig/allow_true_succeeds
=== RUN   TestRunCatalogSync_AllowMissingLicenseFromConfig/allow_false_fails
--- PASS: TestRunCatalogSync_AllowMissingLicenseFromConfig (0.01s)
=== RUN   TestRunCatalogSync_PlainOutputGolden
--- PASS: TestRunCatalogSync_PlainOutputGolden (0.00s)
PASS
ok  	github.com/salaboy/skills-oci/cmd	0.434s
```

Full output: [`cobra-tests-pass.txt`](./cobra-tests-pass.txt).

### Coverage report

```text
ok  	github.com/salaboy/skills-oci/cmd	(cached)	coverage: 7.3% of statements
github.com/salaboy/skills-oci/cmd/catalog_sync.go:68:	runCatalogSync			0.0%
github.com/salaboy/skills-oci/cmd/catalog_sync.go:97:	parseSyncOpts			100.0%
github.com/salaboy/skills-oci/cmd/catalog_sync.go:141:	runCatalogSyncWithDeps		100.0%
```

The 7.3% package-wide number is dominated by uncovered `cmd/` surface area outside Spec 03's scope (other subcommands, the production wrapper, etc.). The function-level coverage on the symbols Spec 03 added/refactored is the load-bearing number: **100% on both new symbols.**

Full output: [`cobra-tests-coverage.txt`](./cobra-tests-coverage.txt).

### Repo-wide regression check

```text
ok  	github.com/salaboy/skills-oci/cmd	0.442s
ok  	github.com/salaboy/skills-oci/pkg/catalog
ok  	github.com/salaboy/skills-oci/pkg/config
ok  	github.com/salaboy/skills-oci/pkg/oci
ok  	github.com/salaboy/skills-oci/pkg/scm
ok  	github.com/salaboy/skills-oci/pkg/telemetry
```

(`?` lines for the TUI packages and root are expected — no test files there. Full output: [`full-test-suite.txt`](./full-test-suite.txt).)

### Quality gates

```text
$ gofmt -l cmd/catalog_sync.go cmd/catalog_sync_test.go
(empty = clean)

$ go vet ./...
(empty = clean)

$ gofmt -l . (repo-wide)
cmd/register.go
pkg/skill/types.go
pkg/telemetry/transport.go
```

Note: the 3 repo-wide `gofmt` flags are **pre-existing drift in `main`** — `git diff main..HEAD --stat` shows zero diff for those files. Not introduced by Spec 03. Suggested follow-up (out of scope here): a `chore(repo): gofmt` cleanup pass.

Full output: [`quality-gates.txt`](./quality-gates.txt).

## Test Results

The 8 cobra-level tests map 1:1 to Spec 03 § Demoable Unit 1 functional requirements:

| Test | What it locks down |
| --- | --- |
| `TestRunCatalogSync_HappyPathExit0` | 2-entry catalog, all succeed → exit 0, lockfile written, both pushes recorded |
| `TestRunCatalogSync_FailureExit1` | One fetcher errors → exit 1, lockfile written, failed entry's prior good state preserved verbatim |
| `TestRunCatalogSync_LockWriteFailureExit2` | `chmod 0500` on temp dir blocks lockfile write → exit 2, error propagated. Skips when running as root. |
| `TestRunCatalogSync_DryRunNoLockWritten` | `--dry-run` → pusher never called, lockfile absent, exit 0 |
| `TestRunCatalogSync_OnlyFilterRespected` | `--only` filters fetcher AND pusher calls to the named subset |
| `TestRunCatalogSync_ConcurrencyFromConfig` | Two subtests: (a) `parseSyncOpts` resolves cfg.Catalog.Concurrency when no flag set; (b) gated fetcher's peak inflight is exactly the cap |
| `TestRunCatalogSync_AllowMissingLicenseFromConfig` | Three subtests: precedence resolution + allow=true succeeds + allow=false fails on the same empty-license fixture |
| `TestRunCatalogSync_PlainOutputGolden` | Byte-equal compare against `cmd/testdata/catalog-sync-plain.golden`; `-update` flag regenerates |

## Configuration

### Refactor diff (production wrapper shrunk to thin shim)

The full diff is at [`runCatalogSync-refactor.diff`](./runCatalogSync-refactor.diff). Shape change:

```text
BEFORE (cmd/catalog_sync.go::runCatalogSync, lines 48–108)
- Flag-read + config-merge + adapter construction + orchestrator call +
  exit-code mapping + os.Exit, ALL in the cobra RunE handler. Untestable
  without subprocess tricks; flag parsing and exit-code mapping had zero
  direct coverage.

AFTER
- runCatalogSync (cobra RunE)  ── 18 LoC, calls parseSyncOpts +
                                  runCatalogSyncWithDeps, translates
                                  returned code into os.Exit.
- parseSyncOpts                ── pure flag-read + cfg-merge, 28 LoC.
- runCatalogSyncWithDeps       ── orchestrator wrapper, takes interfaces
                                  for fetch/license/push + emitter.
                                  Returns (syncExitCode, error).
```

Mirrors the dependency-injection split in `cmd/catalog_add.go::runCatalogAddWithDeps` (line 123).

### Golden file (deterministic 2-entry happy path)

```text
catalog sync starting (2 entries)
[1/2] create-skill cloning @bc6708cb
[1/2] create-skill pushing
[1/2] create-skill ok sha256:1
[2/2] other-skill cloning @d4f8a2e9
[2/2] other-skill pushing
[2/2] other-skill ok sha256:2
catalog sync done: synced=2 skipped=0 failed=0
```

Synthetic data only — no real upstream URLs, no real digests. Test pusher returns canned `sha256:1…` / `sha256:2…` so future format changes are easy to spot in diff.

Regeneration:

```bash
go test ./cmd/ -run TestRunCatalogSync_PlainOutputGolden -update
```

## Verification

| Spec 03 Proof Artifact | Status | Evidence |
| --- | --- | --- |
| `cmd/catalog_sync_test.go` exists with 8 named test functions | ✅ | `cobra-tests-pass.txt` lists all 8 PASS |
| `go test -run TestRunCatalogSync ./cmd/...` passes | ✅ | `cobra-tests-pass.txt` |
| `runCatalogSyncWithDeps` coverage ≥90% | ✅ (100%) | `cobra-tests-coverage.txt` |
| `go test ./...` repo-wide passes | ✅ | `full-test-suite.txt` |
| `cmd/testdata/catalog-sync-plain.golden` committed | ✅ | `Configuration` section above |
| Refactor diff shows DI split mirroring `runCatalogAdd` | ✅ | `runCatalogSync-refactor.diff` |
| `gofmt -l` clean for new files; `go vet` clean | ✅ | `quality-gates.txt` |
| SDD-4 re-run downgrades Spec 02 issue #1 | ⏭ | Pending sub-task 1.14 |

## Security Check

- Golden file uses synthetic data (`sha256:1…`, `sha256:2…`, `bc6708cb…`, `d4f8a2e9…`) — no real upstream URLs, no real digests, no credentials.
- All test fixtures are in-memory or under `t.TempDir()` — nothing persists.
- No credentials, tokens, registry endpoints, or client data anywhere in the test code or proof artifacts.
- The 256-line `runCatalogSync-refactor.diff` contains only Go source — reviewed for stray secrets, none present.
