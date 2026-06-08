# 02-spec-catalog-sync.md

## Introduction/Overview

The catalog-sync workflow lets a platform engineer vendor a third-party skill
(an upstream GitHub directory) into Liatrio's internal registry and onto the
frontend through a reviewed pull request. Most of that workflow lives in the
`skills-platform` (`core`) repository and is **out of scope** for this spec.

This spec covers **only the `skills-oci` changes** the plan calls for:

1. Teach `pkg/oci.Push` to accept caller-supplied OCI manifest annotations (so a
   future CI workflow can stamp provenance onto pushed artifacts).
2. Retarget the existing `skills-oci catalog add` command so it writes a
   source-pin to a new `vendored.json` desired-state file instead of writing
   `catalog.json` rows and per-skill detail files. Discovery (in `core`) becomes
   the single writer of `catalog.json` and detail files; `catalog add` shrinks
   to a pure `vendored.json` mutator.

The primary goal is to make `skills-oci` produce exactly the inputs the rest of
the (out-of-scope) workflow consumes — a `vendored.json` entry and
annotation-stamped pushes — without `skills-oci` ever becoming a second writer
of the published catalog.

## Goals

- Add an additive `Annotations map[string]string` field to `pkg/oci.PushOptions`
  whose entries are merged onto the pushed manifest, with **caller keys
  overriding** built-in (SKILL.md-derived) keys.
- Introduce a `vendored.json` schema + load/validate/atomic-write support in
  `pkg/catalog`, matching the schema the plan defines (`schemaVersion: 1`,
  `skills[]` of source pins).
- Retarget `catalog add` to **upsert** one source-pin entry into `vendored.json`
  (warning + `y/n` confirmation on overwrite; `-y` to proceed non-interactively)
  and to stop writing `catalog.json` and detail files — while keeping its
  existing ref-resolution and `SKILL.md`-verification behavior unchanged.
- Maintain the repository's strict-TDD bar: ≥90% line coverage on new code and
  100% branch coverage on the critical pure logic (annotation merge,
  `vendored.json` validation, upsert, atomic write).

## User Stories

- **As a platform engineer**, I want `catalog add <url>` to record a reviewable
  source pin in `vendored.json` so that merging the PR is the single human
  approval that authorizes the (CI-driven) build of that skill.
- **As a platform engineer**, I want re-running `catalog add` for a skill I
  already vendored to update its pin in place (after warning me) so that a
  version bump is a clean, one-line, reviewable diff.
- **As the author of the future `catalog-sync.yml` CI workflow**, I want
  `pkg/oci.Push` to let me attach provenance annotations
  (`org.opencontainers.image.source`/`.revision`) so that every pushed artifact
  is self-describing for audit — without me being able to accidentally have my
  provenance values silently dropped.
- **As a maintainer of the published catalog**, I want `catalog add` to *not*
  write `catalog.json` or detail files so that discovery remains the single
  writer and there are no races or stale rows to reconcile.

## Demoable Units of Work

### Unit 1: `PushOptions.Annotations` passthrough (`pkg/oci`)

**Purpose:** Give callers (notably the future CI workflow) a way to stamp
arbitrary OCI manifest annotations — provenance in particular — onto a pushed
skill artifact. Serves the build half of the workflow without `skills-oci`
hard-coding any provenance policy.

**Functional Requirements:**

- The system shall add an exported field `Annotations map[string]string` to
  `pkg/oci.PushOptions`.
- The system shall merge `opts.Annotations` into the manifest annotation map
  that `Push` builds today, such that a caller-supplied key **overrides** any
  built-in key of the same name (built-ins fill only keys the caller did not
  supply).
- The system shall treat a `nil`/empty `Annotations` map as a no-op: behavior is
  byte-for-byte identical to today's `Push`.
- The system shall not require any change to existing `Push` callers (the field
  is optional and additive).

**Proof Artifacts:**

- Test: `pkg/oci` test `TestPush_MergesCallerAnnotations` passes — pushes to the
  in-process test registry with a caller annotation
  (`org.opencontainers.image.revision`), pulls the manifest back, and asserts
  the key/value is present. Demonstrates passthrough works end-to-end against a
  real (in-process) registry.
- Test: `TestPush_CallerAnnotationOverridesBuiltin` passes — caller supplies a
  key that `Push` also sets (e.g. `org.opencontainers.image.title`); pulled
  manifest shows the caller's value. Demonstrates the documented precedence.
- Test: `TestPush_NilAnnotations_UnchangedManifest` passes — demonstrates the
  no-op path leaves the existing annotation set intact.

### Unit 2: `vendored.json` types + IO (`pkg/catalog`)

**Purpose:** Define the `vendored.json` desired-state schema and the pure
load/validate/upsert + atomic-write primitives that `catalog add` (Unit 3) and
the out-of-scope `core` reader both depend on. Pure-core-first per repo TDD.

**Functional Requirements:**

- The system shall define `Vendored` and `VendoredEntry` Go types serializing to
  exactly the plan's JSON shape:
  - top-level: `schemaVersion` (int, value `1`), `skills` (array);
  - per entry: `name`, `namespace`, `repo`, `subpath`, `version`, `commit`,
    `internal_ref` (all strings).
- The system shall provide `LoadVendored([]byte) (Vendored, error)`; a
  missing-file case is handled by the caller as an empty
  `Vendored{SchemaVersion: 1}`.
- The system shall provide `ValidateVendored(Vendored) error` that rejects: an
  unsupported `schemaVersion`, any entry missing a required field, and any
  `commit` that is not a 40-character lowercase hex SHA (the load-bearing
  immutability property).
- The system shall provide a pure `UpsertVendored(v Vendored, e VendoredEntry)
  (next Vendored, replaced bool)` helper that replaces an existing entry matched
  by `(namespace, name)` in place (reporting `replaced = true`) or appends a new
  one, and returns `skills[]` in a deterministic order (sorted by `namespace`
  then `name`).
- The system shall provide `WriteVendoredAtomic(path string, v Vendored) error`
  that validates, then writes via temp-file + `os.Rename` (mirroring
  `WriteCatalogAtomic`), producing stable key/entry ordering.

**Proof Artifacts:**

- Test: `pkg/catalog` `TestLoadVendored_RoundTrip` passes — load → write → load
  yields an equal struct. Demonstrates schema fidelity.
- Test: table-driven `TestValidateVendored` passes covering each rejection
  branch (bad schemaVersion, each missing field, non-40-hex / uppercase commit)
  and the valid case. Demonstrates 100% branch coverage on validation.
- Test: `TestUpsertVendored` passes covering append-new, replace-existing
  (`replaced == true`), and deterministic ordering. Demonstrates idempotent
  mutation.
- Test: `TestWriteVendoredAtomic` passes — writes, reloads, and asserts the
  temp-file/rename path and stable ordering; demonstrates durable atomic write.

### Unit 3: Retarget `catalog add` to write `vendored.json` (`cmd`)

**Purpose:** Turn the existing authoring command into a pure `vendored.json`
mutator with an overwrite-confirmation UX, while preserving its
resolution/verification behavior. This is the user-facing slice.

**Functional Requirements:**

- The system shall keep `catalog add`'s existing input handling unchanged: URL or
  `--repo`/`--subpath`/`--version` flags, ref → commit-SHA resolution (branches
  resolved to head commit and recorded as the SHA), subpath fetch into a temp
  dir, and `SKILL.md` verification.
- The system shall replace the catalog/detail write path with a single
  `vendored.json` upsert: build a `VendoredEntry` from the resolved coordinates
  (`name`, `namespace`, `repo`, `subpath`, `version`, `commit`, `internal_ref`)
  and `UpsertVendored` + `WriteVendoredAtomic` it.
- The system shall expose a `--vendored` flag (default `vendored.json`) for the
  output path and shall **remove** the `--catalog` and `--detail-dir` flags and
  their write paths.
- The system shall, when an entry for `(namespace, name)` already exists, print a
  warning that it will be overwritten and prompt `y/n` on an interactive
  terminal; a negative/empty answer aborts without writing.
- The system shall, in a non-interactive context (no TTY, `--plain`, or piped
  stdin), require a `-y`/`--yes` flag to overwrite; absent it, the command shall
  exit non-zero with a message instructing the user to pass `-y`.
- The system shall keep `--dry-run` working: it prints the resolved entry and the
  would-be action (`add`/`overwrite`) and writes nothing.
- The user shall be able to run the command under `--plain` for scripting/CI; the
  plain path shall be parseable and shall not block on a prompt.

**Proof Artifacts:**

- CLI: `skills-oci catalog add --help` output shows `--vendored` and `-y`/`--yes`
  and no longer shows `--catalog`/`--detail-dir`. Demonstrates the retargeted
  surface.
- Test: `cmd` `TestCatalogAdd_WritesVendored` passes (DI fakes for
  resolver/fetcher) — asserts a new `vendored.json` with the expected entry and
  that no `catalog.json`/detail file is written. Demonstrates the new target.
- Test: `TestCatalogAdd_OverwriteRequiresConfirm` passes — existing entry, piped
  `n` aborts (no write); piped `y` overwrites; `--plain` without `-y` exits
  non-zero; `--plain -y` overwrites. Demonstrates the confirmation UX across
  interactive and non-interactive modes.
- Test: `TestCatalogAdd_DryRun` passes — prints resolved entry, writes nothing.
- Proof log: captured `--plain` run output for a 2-skill sequence (one add, one
  overwrite-with-`-y`) saved under the spec's `proofs/` directory.

## Non-Goals (Out of Scope)

1. **All `core` / `skills-platform` work**: the `vendored.json` *reader*,
   `skills-discover sync-plan`, the `catalog-sync.yml` workflow, discovery's
   vendored processing path, vendored-only refresh mode, and
   `FetchSourceRevision` on `RegistryAdapter` — handled by another agent.
2. **The `digest`/`commit` data-contract amendment** to per-skill detail files.
   Because the retargeted `catalog add` no longer writes detail files, this has
   no `skills-oci` producer and lands entirely in `core`. `pkg/catalog/detail.go`
   (`SkillDetail`, `SkillVersion`, `WriteSkillDetailAtomic`,
   `ValidateSkillDetail`) and its tests are **left intact** but unused by
   `catalog add` — not deleted in this spec.
3. **A `catalog sync` subcommand in `skills-oci`.** The superseded
   `docs/specs/catalog-sync-plan.md` draft proposed one; the current design moves
   sync/build to CI + `core`. No `sync` command is added here.
4. **Pushing from `catalog add`.** `catalog add` still never contacts the
   destination registry; it only resolves, verifies, and writes `vendored.json`.
5. **Renovate configuration** for `vendored.json` (deferred to a follow-up).
6. **Signatures / build-provenance attestations.** Unit 1 only provides the
   annotation *passthrough*; choosing/stamping annotation values is the
   (out-of-scope) CI workflow's job.
7. **Auth for private upstreams, non-GitHub hosts, sparse checkout** — unchanged
   from `catalog add` today.

## Design Considerations

No graphical UI. The only interaction-design element is the overwrite
confirmation in `catalog add`:

- Interactive (TTY): print `entry <ns>/<name> already exists in vendored.json;
  overwrite? [y/N]` and read a line from stdin; default (empty) = No.
- Non-interactive (no TTY / `--plain` / piped stdin): do not prompt. Require
  `-y`/`--yes`. Without it, print a clear error naming the conflicting entry and
  instructing the user to pass `-y`, and exit non-zero.
- TUI vs. plain parity: per repo standards, the `--plain` path is the contract;
  the prompt belongs to interactive mode only and must never block scripted runs.

## Repository Standards

Follow the standards in `CLAUDE.md`:

- **Strict TDD** (RED → GREEN → REFACTOR); no production code before a failing
  test. ≥90% line coverage on new code; 100% branch coverage on the critical
  pure logic (annotation merge, `vendored.json` validation/upsert, atomic write).
- **One concern per package**: `pkg/oci` does not parse SKILL.md; `pkg/catalog`
  does not talk to registries; `cmd` dispatches to core packages. Keep
  `vendored.json` parsing/validation/upsert as pure functions in `pkg/catalog`;
  keep IO (stdin prompt, file rename) at the edges.
- **Dependency injection** in `cmd` mirrors `cmd/catalog_add.go`'s existing
  resolver/fetcher interfaces and `runCatalogAddWithDeps` structure; the
  confirmation prompt must be injectable (e.g. an `io.Reader` for stdin / a
  confirm func) so tests don't need a real TTY.
- **Atomic writes** via temp-file + `os.Rename`, copying `WriteCatalogAtomic`.
- **Error wrapping** with context (`fmt.Errorf("...: %w", err)`).
- **Conventional commits**; table-driven tests; `go test ./...` and `go vet
  ./...` clean before commit; `gofmt` applied.

## Technical Considerations

- **`vendored.json` JSON shape is a cross-repo contract**: `core` reads exactly
  what `skills-oci` writes. Field names must match the plan precisely —
  top-level `schemaVersion` (camelCase) and `skills`; entry keys `name`,
  `namespace`, `repo`, `subpath`, `version`, `commit`, `internal_ref`. Note this
  differs from `catalog.json`'s `schema_version` (snake_case); follow the plan's
  `vendored.json` spelling, not the catalog's.
- **`version` vs `commit`**: `version` records the human-facing immutable ref
  (tag or SHA the user vendored at); `commit` is always the resolved 40-hex SHA.
  This matches `catalog add`'s existing resolve behavior (tag kept as `version`,
  SHA captured as `commit`; branch swapped to its head SHA for both immutability
  and recorded as the commit).
- **Annotation merge implementation**: build the existing built-in map first,
  then overlay `opts.Annotations` (caller overrides). Keep the change localized
  to where the annotation map is assembled in `pkg/oci/push.go`.
- **Confirmation/TTY detection**: detect non-interactive via the injected
  stdin/term check rather than calling out to a global; gate on the existing
  `--plain` flag too. Keep the detection testable without a PTY.
- **Reuse, not rewrite**: keep `catalog add`'s resolve/fetch/verify code paths;
  only the write target/shape and the added confirmation logic change.

## Security Considerations

- **`commit` immutability is the trust anchor.** Validation must enforce a full
  40-char lowercase hex SHA; a short or mutable value would undermine the
  SHA-pin + PR-review trust boundary the plan relies on. This is covered by
  `ValidateVendored` branch tests.
- **No new secrets.** `catalog add` continues to fetch upstream anonymously and
  never contacts the destination registry; Unit 1's passthrough carries only
  non-secret provenance strings.
- **Proof artifacts** committed under the spec's `proofs/` directory must contain
  only public upstream coordinates and command output — no tokens or registry
  credentials.
- **Annotations are unsigned** in v1 (matches the plan); nothing in `skills-oci`
  verifies them. The passthrough must not imply trust it doesn't provide.

## Success Metrics

1. **Behavioral**: `catalog add <url>` produces a valid `vendored.json` entry and
   writes neither `catalog.json` nor any detail file (asserted by Unit 3 tests).
2. **Provenance-ready**: a `Push` with caller annotations yields a manifest
   carrying those annotations, caller-overriding built-ins (asserted by Unit 1
   tests against the in-process registry).
3. **Coverage**: ≥90% line coverage on new code; 100% branch coverage on
   annotation merge, `ValidateVendored`, `UpsertVendored`, and
   `WriteVendoredAtomic`. `go test ./...` and `go vet ./...` pass.
4. **No regressions**: existing `catalog add` resolve/verify tests and all other
   suites continue to pass; `detail.go` and its tests remain green though unused
   by the command.

## Open Questions

No open questions at this time. The four design decisions (output target/flags,
upsert + overwrite-confirmation UX, digest/commit out-of-scope, annotation merge
precedence) were resolved in `02-questions-1-catalog-sync.md`.
