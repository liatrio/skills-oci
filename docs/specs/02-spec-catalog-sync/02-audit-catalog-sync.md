# 02-audit-catalog-sync.md

Planning audit for the skills-oci slice of catalog-sync. Run 2 (post-remediation).

## Executive Summary

- Overall Status: **PASS**
- Required Gate Failures: 0
- Flagged Risks: 2 (both folded into tasks; now addressed)

## Gateboard

| Gate | Status | Note (<=10 words) | Evidence |
| --- | --- | --- | --- |
| Requirement-to-test traceability | PASS | every FR maps to a test/observable artifact | traceability table below |
| Proof artifact verifiability | PASS | all artifacts have exact cmd/path | tasks `#.0 Proof Artifact(s)` |
| Repository standards consistency | PASS | 4 sources read, no conflicts, README reviewed | Standards Evidence Table |
| Open question resolution | PASS | 4 decisions resolved; spec has none open | `02-questions-1` (answered) |
| Regression-risk blind spots | FLAG (addressed) | obsolete catalog-writing tests migrated in task 3.1a | see FLAG 1 |
| Non-goal leakage | FLAG (addressed) | scoped deletion guard added to task 3.6 | see FLAG 2 |

## Standards Evidence Table

| Source File | Read | Standards Extracted | Conflicts |
| --- | --- | --- | --- |
| `CLAUDE.md` | yes | strict TDD RED→GREEN→REFACTOR; ≥90% line / 100% branch on critical pure logic; one-concern-per-package; `--plain` parity; conventional commits; `go test`+`go vet` pre-commit | none |
| `README.md` | yes | documents `catalog add` flags `--catalog`/`--detail-dir` (must update); `--plain` is the scripting contract; push annotation set documented | none |
| `.github/workflows/_test.yml` | yes | CI = `go build ./...` + `go vet ./...` + `go test ./...` | none |
| `.github/workflows/pr-title.yml` | yes | Conventional-Commits PR title enforced (squash-merge) | none |
| `AGENTS.md` / `CONTRIBUTING.md` / PR template | not found | — | — |

## Requirement-to-Test Traceability

| Functional Requirement (spec) | Task | Test / Observable Artifact |
| --- | --- | --- |
| U1: add `Annotations` field | 1.2 | `TestPush_MergesCallerAnnotations` |
| U1: caller key overrides built-in | 1.3 | `TestPush_CallerAnnotationOverridesBuiltin` |
| U1: nil = no-op / no caller change | 1.4, 1.5 | `TestPush_NilAnnotations_UnchangedManifest` + `go test ./...` |
| U2: `Vendored`/`VendoredEntry` types | 2.2 | `TestLoadVendored_RoundTrip` |
| U2: `LoadVendored` | 2.2 | `TestLoadVendored_RoundTrip` |
| U2: `ValidateVendored` (schema/fields/40-hex commit) | 2.3 | `TestValidateVendored` (table) |
| U2: `UpsertVendored` (replace/append/order) | 2.4 | `TestUpsertVendored` |
| U2: `WriteVendoredAtomic` | 2.5 | `TestWriteVendoredAtomic` |
| U3: keep resolve/fetch/verify | 3.1, 3.1a, 3.2 | `TestCatalogAdd_WritesVendored` + preserved resolve/verify/SSRF cases |
| U3: write only vendored.json (no catalog/detail) | 3.1, 3.2 | `TestCatalogAdd_WritesVendored` (asserts absence) |
| U3: `--vendored` added; `--catalog`/`--detail-dir` removed | 3.3, 3.8 | `proofs/catalog-add-help.txt` (CLI `--help`) |
| U3: overwrite warn + `y/n` confirm | 3.4 | `TestCatalogAdd_OverwriteRequiresConfirm` |
| U3: non-interactive requires `-y` | 3.4 | `TestCatalogAdd_OverwriteRequiresConfirm` (`--plain` w/o `-y`) |
| U3: `--dry-run` preserved | 3.5 | `TestCatalogAdd_DryRun` |
| U3: `--plain` parseable / non-blocking | 3.4, 3.8 | confirm test `--plain` path + `proofs/catalog-add-plain-run.txt` |

No FR is left without a planned test or observable artifact.

## FLAG Findings (addressed in Run 2)

1. **Obsolete catalog-writing tests (regression risk).** — Addressed by new task
   **3.1a**: inventory + migrate/delete the old `catalog.json`/detail/`migrateToV2`
   assertions to the `vendored.json` contract while preserving the still-valid
   parse/resolve/verify/SSRF/dry-run cases; `pkg/catalog/detail*_test.go` left
   untouched.

2. **Dead-code deletion scope (non-goal leakage).** — Addressed by tightening
   task **3.6**: confirm zero repo-wide references before deleting each symbol,
   lean on the compiler/`go vet`, and explicitly exclude `pkg/catalog` exported
   helpers (`AddEntry`, `WriteCatalogAtomic`) and `detail.go` from deletion.

## User-Approved Remediation Plan

- **Approved** (user: "fold in the flags"). Both FLAG items folded into the task
  list (3.1a added; 3.6 tightened) and traceability updated. **Completed.**

## Re-Audit Delta (Run 2)

- Changed gate statuses: Regression-risk and Non-goal-leakage FLAGs → **addressed**
  (advisory; never REQUIRED failures).
- Still-failing REQUIRED gates: none (none were failing in Run 1).
- Newly introduced findings: none. Task 3.1a added; 3.6 reworded; traceability
  row for "keep resolve/fetch/verify" now cites 3.1a + preserved cases.

## Chain-of-Verification

- Self-question "do all REQUIRED gates pass with explicit evidence?" → yes;
  each REQUIRED gate has a cited source (traceability table, Standards Evidence
  Table, answered questions file).
- Fact-check: spec FR list cross-checked against task test artifacts — no
  unmapped FR found. Standards rows verified against files actually read this
  session.
- Inconsistencies: none unresolved. Both FLAGs are advisory, not REQUIRED
  failures.
- Final synthesis: REQUIRED gates PASS. Next action: user review of the two
  FLAGs (no remediation strictly required to proceed), then `/SDD-3-manage-tasks`.
