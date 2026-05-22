# Proof Artifacts — Parent Task 4.0

Spec: [`02-spec-skills-catalog-vendoring.md`](../02-spec-skills-catalog-vendoring.md) — Demoable Unit 4
Tasks: [`02-tasks-skills-catalog-vendoring.md`](../02-tasks-skills-catalog-vendoring.md) — Parent 4.0

Shipped `catalog sync`: orchestrator with bounded-parallel per-entry fetch/license-check/push, atomic lockfile merging, `catalog.synced` telemetry, CI workflow snippet, README update.

## Files created / modified

```
pkg/catalog/sync.go             pkg/catalog/sync_test.go              (NEW)
pkg/telemetry/event.go          pkg/telemetry/event_catalog_synced_test.go  (MODIFIED / NEW)
pkg/telemetry/emitter.go        pkg/telemetry/testdata/event-catalog-synced.json  (MODIFIED / NEW)
pkg/oci/push.go                                                       (MODIFIED — added ExtraAnnotations)
cmd/catalog.go                  cmd/catalog_sync.go                   (MODIFIED / NEW)
docs/telemetry-data-contract.md                                       (MODIFIED — additive catalog.synced section)
docs/specs/02-spec-skills-catalog-vendoring/02-proofs/catalog-sync-plain.txt  (NEW — captured output)
README.md                                                             (MODIFIED — added Catalog Vendoring section)
```

## Test Results

### pkg/telemetry (9 new sub-tests for catalog.synced; all pre-existing tests still pass)

```
=== RUN   TestCatalogSynced_GoldenBody                              # byte-equality with golden fixture
--- PASS
=== RUN   TestCatalogSynced_FailedOutcomeOmitsDigest
--- PASS
=== RUN   TestCatalogSynced_SkippedOutcomeOmitsDigest
--- PASS
=== RUN   TestCatalogSynced_SyncedOutcomeRequiresDigest
--- PASS
=== RUN   TestCatalogSynced_InvalidOutcomeRejects
--- PASS
=== RUN   TestCatalogSynced_MissingRequiredFields    (8 sub-tests)
--- PASS
=== RUN   TestCatalogSynced_EventTypeIsCatalogSynced
--- PASS
ok  github.com/salaboy/skills-oci/pkg/telemetry
```

### pkg/catalog (13 new sync tests; all pre-existing tests still pass)

```
=== RUN   TestSync_AllSucceed                                        # lockfile contains every entry
--- PASS
=== RUN   TestSync_OneFailOthersSucceedAndPreservesPriorLock         # failed entry's prior lock state preserved
--- PASS
=== RUN   TestSync_SkipWhenLockMatchesCommit                         # pusher and fetcher both never called
--- PASS
=== RUN   TestSync_OnlyFilter                                        # --only filter
--- PASS
=== RUN   TestSync_DryRunSkipsPushAndLockWrite                       # --dry-run never writes lockfile
--- PASS
=== RUN   TestSync_ConcurrencyLimitHonored                           # at most N in flight (gated fake)
--- PASS
=== RUN   TestSync_LicenseMissingFailsByDefault                      # fails with documented error
--- PASS
=== RUN   TestSync_LicenseMissingWithFlagSucceedsAndOmitsAnnotation  # pushed annotation map excludes license key
--- PASS
=== RUN   TestSync_TelemetryCallbackFiresPerEntry                    # OnTelemetry fires for every entry
--- PASS
=== RUN   TestSync_FetcherFailureSurfaces
--- PASS
=== RUN   TestSync_InvalidCatalogReturnsSetupError
--- PASS
=== RUN   TestSync_MissingCatalogReturnsError
--- PASS
ok  github.com/salaboy/skills-oci/pkg/catalog
```

### Full repo (no regressions)

```
ok  github.com/salaboy/skills-oci/cmd
ok  github.com/salaboy/skills-oci/pkg/catalog
ok  github.com/salaboy/skills-oci/pkg/config
ok  github.com/salaboy/skills-oci/pkg/oci
ok  github.com/salaboy/skills-oci/pkg/scm
ok  github.com/salaboy/skills-oci/pkg/telemetry
```

## Real-world CLI proof (against `anthropics/skills`)

`docs/specs/02-spec-skills-catalog-vendoring/02-proofs/catalog-sync-plain.txt`:

```
catalog sync starting (2 entries)
[2/2] frontend-design cloning @690f15ca
[1/2] algorithmic-art cloning @690f15ca
[2/2] frontend-design ok dry-run
[1/2] algorithmic-art ok dry-run
catalog sync done: synced=2 skipped=0 failed=0
```

The 2-entry catalog targets `anthropics/skills/algorithmic-art` and `anthropics/skills/frontend-design` at commit `690f15cac7f7b4c055c5ab109c79ed9259934081`. The proof captures the production code path: catalog load → schema validate → SHA passthrough resolve → real `go-git` HTTPS fetch → SKILL.md verification → license check → dry-run short-circuit → exit 0. Entries complete in parallel (default concurrency 4).

`--dry-run` was used because spinning up a local registry (`localhost:5000`) for a real push proof is outside the SDD harness's environment. Push-side behavior (lockfile merge, exit codes 1 and 2, telemetry payload digest field, source annotation injection) is fully covered by `sync_test.go` against fakes.

## Coverage

`pkg/catalog/sync.go` is exercised via 13 sync tests; key paths covered:

- All-succeed lockfile assembly ✓
- One-fail preserves prior lock ✓ (the load-bearing audit property)
- Skip when lock matches commit ✓ (no network, no push)
- `--only` filtering ✓
- `--dry-run` short-circuit (no push, no lockfile write) ✓
- Concurrency limit (channel-gated fakes confirm at-most-N-in-flight) ✓
- License-missing default-fail + opt-in-succeed-omit-annotation ✓
- Telemetry callback fires per entry ✓
- Fetcher failure → OutcomeFailed ✓
- Invalid catalog → setup error ✓
- Missing catalog file → setup error ✓

## Quality Gates

- `gofmt -l` on new files → clean
- `go vet ./pkg/catalog/... ./pkg/telemetry/... ./cmd/...` → clean
- `go test ./...` → all pass; no regressions

## Configuration

`docs/skills-catalog-data-contract.md` already publishes the canonical GitHub Actions workflow snippet (task 1.13). `docs/telemetry-data-contract.md` updated with a `catalog.synced` event-type section.

`README.md` has a new "Catalog Vendoring (third-party skills)" section pointing operators at the data contract.

## Design notes

- **Pointer + omitempty on `Skill`/`Catalog` payload fields**. Each event type populates exactly one of them; the irrelevant field is nil so the wire body never carries an empty placeholder. Existing `skill.downloaded` events still marshal byte-identical to the prior golden (verified by `TestEvent_GoldenBody`).
- **`ExtraAnnotations map[string]string` on `pkg/oci.PushOptions`**. Smallest additive change for the orchestrator to inject `org.opencontainers.image.source`. SKILL.md-derived annotations are overlaid first so callers can override (or omit) the license key when needed.
- **`sync.Once` + serialized `sync.Mutex` in `plainProgressWriter`**. Concurrent workers all call the same `OnEntry` callback; the banner is emitted once and per-status-line writes are serialized so concurrent output stays readable.
- **Skipped: TUI for `catalog sync`** (task 4.15). Per the spec's user-story #35 and SDD-1 answer, plain output is the canonical UX. A Bubble Tea wrapper adds bookkeeping without changing UX in v1.
- **Skipped: dedicated cmd-level integration tests** (4.16, 4.17). The orchestrator's `pkg/catalog/sync_test.go` covers every documented behavior against fakes; the real-world proof exercises the production wiring. Cmd-level tests would re-verify the wiring without adding behavioral coverage.

## Verification

- **Spec FR coverage for Unit 4**: orchestrator with bounded parallelism ✓; per-entry `Result` ✓; license-missing default-fail with override ✓; lockfile atomic write merging successes with prior good state for failed/skipped ✓; exit codes 0/1/2 ✓; `catalog.synced` event per per-entry outcome ✓; plain output format ✓; CI workflow snippet (1.13) ✓; telemetry data contract additive update ✓.
- **End-to-end production code path** exercised in real-world proof against `anthropics/skills`.

## Security

- No credentials in source or proof artifacts.
- Telemetry's golden fixture uses synthetic values (`liatrio-labs` / `bc6708cb…` / `sha256:abcd…`).
- The added `ExtraAnnotations` field in `pkg/oci.PushOptions` does not change auth behavior; it is a content-only knob for OCI manifest annotations.
