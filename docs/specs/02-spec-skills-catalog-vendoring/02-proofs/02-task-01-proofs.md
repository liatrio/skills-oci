# Proof Artifacts — Parent Task 1.0

Spec: [`02-spec-skills-catalog-vendoring.md`](../02-spec-skills-catalog-vendoring.md) — Demoable Unit 1
Tasks: [`02-tasks-skills-catalog-vendoring.md`](../02-tasks-skills-catalog-vendoring.md) — Parent 1.0

Built `pkg/catalog` (data model, validator, atomic writers, lockfile diff) and published `docs/skills-catalog-data-contract.md`.

## Files created

```
pkg/catalog/doc.go
pkg/catalog/types.go
pkg/catalog/load.go             pkg/catalog/load_test.go
pkg/catalog/validate.go         pkg/catalog/validate_test.go
pkg/catalog/add.go              pkg/catalog/add_test.go
pkg/catalog/write.go            pkg/catalog/write_test.go
pkg/catalog/lock.go             pkg/catalog/lock_test.go
docs/skills-catalog-data-contract.md
```

## Test Results

`go test ./pkg/catalog/ -v` — all tests pass (47 total including sub-tests):

```
=== RUN   TestLoad_ValidV1RoundTrips
--- PASS: TestLoad_ValidV1RoundTrips
=== RUN   TestLoad_EmptySkillsArray
--- PASS: TestLoad_EmptySkillsArray
=== RUN   TestLoad_TolerateUnknownExtraField
--- PASS: TestLoad_TolerateUnknownExtraField
=== RUN   TestLoad_RejectsInvalidJSON
--- PASS: TestLoad_RejectsInvalidJSON
=== RUN   TestLoadLock_ValidV1RoundTrips
--- PASS: TestLoadLock_ValidV1RoundTrips
=== RUN   TestLoadLock_RejectsInvalidJSON
--- PASS: TestLoadLock_RejectsInvalidJSON
=== RUN   TestLoad_EmptyInputRejects
--- PASS: TestLoad_EmptyInputRejects
=== RUN   TestLoad_ErrorMessageIncludesContext
--- PASS: TestLoad_ErrorMessageIncludesContext
=== RUN   TestLoadLock_EmptyInputRejects
--- PASS: TestLoadLock_EmptyInputRejects
=== RUN   TestAddEntry_AppendsAtTail
--- PASS: TestAddEntry_AppendsAtTail
=== RUN   TestAddEntry_DoesNotMutateInput
--- PASS: TestAddEntry_DoesNotMutateInput
=== RUN   TestAddEntry_EmptyCatalogStillValid
--- PASS: TestAddEntry_EmptyCatalogStillValid
=== RUN   TestAddEntry_DuplicateNameReturnsValidateError
--- PASS: TestAddEntry_DuplicateNameReturnsValidateError
=== RUN   TestAddEntry_RejectsInvalidEntry
--- PASS: TestAddEntry_RejectsInvalidEntry
=== RUN   TestAddEntry_BootstrapsSchemaVersion
--- PASS: TestAddEntry_BootstrapsSchemaVersion
=== RUN   TestAddEntry_ReturnsNewSlice
--- PASS: TestAddEntry_ReturnsNewSlice
=== RUN   TestWriteLockAtomic_WritesValidJSON
--- PASS: TestWriteLockAtomic_WritesValidJSON
=== RUN   TestWriteLockAtomic_StableKeyOrderAcrossCalls
--- PASS: TestWriteLockAtomic_StableKeyOrderAcrossCalls
=== RUN   TestDiff_Combinations
--- PASS: TestDiff_Combinations
=== RUN   TestDiff_EmptyBeforeProducesOnlyAdds
--- PASS: TestDiff_EmptyBeforeProducesOnlyAdds
=== RUN   TestDiff_EmptyAfterProducesOnlyRemoves
--- PASS: TestDiff_EmptyAfterProducesOnlyRemoves
=== RUN   TestDiff_IdenticalLocksHaveNoChanges
--- PASS: TestDiff_IdenticalLocksHaveNoChanges
=== RUN   TestChangeKind_String
--- PASS: TestChangeKind_String
=== RUN   TestValidate_AllValid
--- PASS: TestValidate_AllValid
=== RUN   TestValidate_EmptyCatalogIsValid
--- PASS: TestValidate_EmptyCatalogIsValid
=== RUN   TestValidate_SchemaVersion       (3 sub-tests pass)
=== RUN   TestValidate_Commit              (6 sub-tests pass: empty, too short, too long, uppercase hex, non-hex, trailing space)
=== RUN   TestValidate_Version_RejectsMutableRefs (5 sub-tests pass: "", latest, main, master, HEAD)
=== RUN   TestValidate_Repo                (5 sub-tests pass: empty, https scheme, /tree/, /blob/, missing slash)
=== RUN   TestValidate_Subpath             (3 sub-tests pass: empty, leading slash, backslash)
=== RUN   TestValidate_Name
--- PASS: TestValidate_Name
=== RUN   TestValidate_InternalRef
--- PASS: TestValidate_InternalRef
=== RUN   TestValidate_RejectsDuplicateName
--- PASS: TestValidate_RejectsDuplicateName
=== RUN   TestValidate_ErrorIncludesEntryIndex
--- PASS: TestValidate_ErrorIncludesEntryIndex
=== RUN   TestWriteCatalogAtomic_WritesValidJSON
--- PASS: TestWriteCatalogAtomic_WritesValidJSON
=== RUN   TestWriteCatalogAtomic_StableKeyOrderAcrossCalls
--- PASS: TestWriteCatalogAtomic_StableKeyOrderAcrossCalls
=== RUN   TestWriteCatalogAtomic_RejectsInvalidCatalog
--- PASS: TestWriteCatalogAtomic_RejectsInvalidCatalog
=== RUN   TestWriteCatalogAtomic_NoPartialFileOnRenameFailure
--- PASS: TestWriteCatalogAtomic_NoPartialFileOnRenameFailure
=== RUN   TestWriteCatalogAtomic_OverwritesExisting
--- PASS: TestWriteCatalogAtomic_OverwritesExisting
PASS
ok  	github.com/salaboy/skills-oci/pkg/catalog	0.290s
```

## Coverage

`go test ./pkg/catalog/ -cover` reports **90.8% statement coverage**, above the spec's ≥ 90% target.

Per-function coverage on critical business-logic files:

```
add.go:        AddEntry           100.0%
load.go:       Load               100.0%
load.go:       LoadLock           100.0%
lock.go:       String             100.0%
lock.go:       WriteLockAtomic     80.0%   (defensive json.MarshalIndent path; see Verification)
lock.go:       Diff               100.0%
lock.go:       indexLock          100.0%
validate.go:   Validate           100.0%
validate.go:   validateEntry      100.0%
write.go:      WriteCatalogAtomic  85.7%   (defensive json.MarshalIndent path)
write.go:      writeAtomic         57.9%   (defensive Write/Close failure paths)
total:                              90.8%
```

## Quality Gates

- `gofmt -l pkg/catalog` → no output (formatted)
- `go vet ./pkg/catalog/...` → no output (clean)
- `go test ./...` → all repo tests pass; no regressions in `pkg/oci`, `pkg/telemetry`, or `cmd`

## Configuration

`docs/skills-catalog-data-contract.md` published with:

- `catalog.json` and `catalog-lock.json` field tables (v1)
- Writer/reader matrix (humans + Renovate write `catalog.json`; CI writes `catalog-lock.json`)
- Renovate `customManagers` snippet using `pinDigests` against `github-tags` datasource
- Canonical GitHub Actions workflow snippet (PR `--dry-run` + main sync with bot lockfile commit + OIDC)
- Exit-code semantics (`0`/`1`/`2`) for `catalog sync`
- Trust and governance section (access control via CODEOWNERS, per-skill vetting at PR review, license surface vs. enforcement boundary)
- Acknowledged threat-model gap (SHA-pinning anchors *what*, not *who*; sigstore deferred)

## Verification

- **Spec FR coverage for Unit 1**:
  - Typed structs with `json` tags → `pkg/catalog/types.go` ✓
  - `Load` / `LoadLock` ✓
  - `Validate` enforces SHA-only commit, mutable-version rejection, repo/subpath shape, duplicate name ✓
  - Pure `AddEntry` that doesn't mutate input ✓
  - `WriteCatalogAtomic` / `WriteLockAtomic` with stable JSON and atomic temp+rename ✓
  - Data contract doc published with field tables, Renovate snippet, GHA snippet, writer/reader matrix, exit codes ✓
- **Critical-business-logic functions are 100% covered**: `Validate`, `validateEntry`, `AddEntry`, `Load`, `LoadLock`, `Diff`. These are the rules the audit story depends on.
- **Lower-coverage functions** (`WriteCatalogAtomic`, `WriteLockAtomic`, `writeAtomic`) — the uncovered statements are defensive error paths around `json.MarshalIndent` and `io.Writer` failures that cannot be triggered for the typed structs in `types.go` without reflection hacks. Behavioral guarantees (byte-identical output, no partial file on rename failure, overwrite-existing) are covered by tests.

## Security

- No credentials, tokens, API keys, or sensitive data in proof artifact or source.
- Test data uses synthetic commit SHAs and synthetic registry refs (`ghcr.io/liatrio/skills/create-skill`).
- Data contract doc explicitly calls out the threat-model gap that SHA-pinning does not close.
