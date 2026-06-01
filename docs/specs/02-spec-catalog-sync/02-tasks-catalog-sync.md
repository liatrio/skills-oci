# 02-tasks-catalog-sync.md

Implementation plan for the **skills-oci slice** of the catalog-sync feature
(spec: `02-spec-catalog-sync.md`; decisions: `02-questions-1-catalog-sync.md`).
All `core` / `skills-platform` work is out of scope.

Every sub-task follows the repo's **strict TDD** loop (CLAUDE.md): write the
failing test first (RED), minimum code to pass (GREEN), then refactor. Run
`go test ./...` and `go vet ./...` before each commit; `gofmt` applied.

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `pkg/oci/push.go` | Add the `Annotations map[string]string` field to `PushOptions` and merge caller keys over built-ins in `Push` (annotation map assembled ~push.go:120-144). |
| `pkg/oci/push_test.go` | **New.** Unit tests for the annotation passthrough (merge, override, nil no-op) pushing to the in-process test registry and pulling the manifest back. |
| `pkg/oci/testregistry_helpers` (see `cmd/testregistry_test.go`) | Reference pattern for an in-process OCI registry to push to / pull from in tests. |
| `pkg/catalog/vendored.go` | **New.** `Vendored`/`VendoredEntry` types, `LoadVendored`, `ValidateVendored`, pure `UpsertVendored`, and `WriteVendoredAtomic` (reusing package-level `writeAtomic` from `write.go`). |
| `pkg/catalog/vendored_test.go` | **New.** Table-driven tests: load round-trip, validation branches, upsert append/replace/ordering, atomic write. |
| `pkg/catalog/write.go` | Source of the reusable package-level `writeAtomic(path, body)` helper (write.go:31). No change expected; referenced by `WriteVendoredAtomic`. |
| `cmd/catalog_add.go` | Retarget: add `--vendored` + `-y`/`--yes` flags; remove `--catalog`/`--detail-dir`; replace catalog/detail write path with a `vendored.json` upsert + overwrite confirmation; drop now-dead helpers (`buildSkillDetail`, `loadCatalogFile`, `migrateToV2`, and any helper only they used). Keep resolve/fetch/verify + `resolveUpstreamInputs`/`resolveInternalRef`/`extractV2Namespace`. |
| `cmd/catalog_add_test.go` | Update/extend: assert `vendored.json` output, no `catalog.json`/detail written, overwrite confirm (y/n, `--plain` requires `-y`), dry-run, help surface. |
| `pkg/catalog/detail.go` | **Left intact, unused by the command** (per decision Q3-A). Do not delete. |
| `README.md` | Update the "Vendoring third-party skills (`catalog add`)" section: new `--vendored`/`-y` flags, removed `--catalog`/`--detail-dir`, "writes `vendored.json`" wording. |
| `docs/specs/02-spec-catalog-sync/proofs/` | **New dir.** Captured `--help` and `--plain` run output proof artifacts. |
| `docs/specs/catalog-sync-plan.md` | Superseded older draft; add a one-line "Superseded by `docs/specs/02-spec-catalog-sync/`" banner (or remove) so it doesn't mislead. |

### Notes

- Go unit tests live beside the code under test (`push.go` → `push_test.go`),
  same package where white-box access is needed.
- Test command: `go test ./...` (full) or `go test ./pkg/oci/... ./pkg/catalog/... ./cmd/...`
  for the touched packages; `go test -cover` to confirm coverage targets.
- Keep parsing/validation/upsert as **pure functions** in `pkg/catalog`; keep IO
  (stdin prompt, file rename, registry HTTP) at the command/registry edges.
- The confirmation prompt reads from an injected reader (`cmd.InOrStdin()`); tests
  pass `strings.NewReader(...)` — no real TTY needed. `--plain` is the explicit
  non-interactive signal (matches the repo's existing `--plain`-as-contract use);
  `-y`/`--yes` is the non-interactive override.

## Tasks

### [x] 1.0 `pkg/oci` — `PushOptions.Annotations` passthrough

Add an optional caller-supplied annotation map to `Push`, merged over the
built-in (SKILL.md-derived) annotations with **caller keys winning**, and a
`nil` map proven to be a no-op.

#### 1.0 Proof Artifact(s)

- Test: `go test ./pkg/oci/ -run TestPush_MergesCallerAnnotations -v` passes —
  pushes with `org.opencontainers.image.revision` set, pulls the manifest from
  the in-process registry, asserts the key/value is present. Demonstrates FR
  "merge `opts.Annotations` onto the manifest."
- Test: `go test ./pkg/oci/ -run TestPush_CallerAnnotationOverridesBuiltin -v`
  passes — caller-supplied `org.opencontainers.image.title` value lands on the
  pulled manifest, overriding the built-in. Demonstrates the "caller wins"
  precedence (decision Q4-A).
- Test: `go test ./pkg/oci/ -run TestPush_NilAnnotations_UnchangedManifest -v`
  passes — nil/empty `Annotations` leaves the existing built-in annotation set
  intact. Demonstrates the additive no-op guarantee.
- CLI/coverage: `go test -cover ./pkg/oci/` reports the new merge branch covered.

#### 1.0 Tasks

- [x] 1.1 RED: create `pkg/oci/push_test.go` with `TestPush_MergesCallerAnnotations`
  that stands up the in-process registry (pattern from `cmd/testregistry_test.go`),
  packages a fixture skill dir, calls `Push` with
  `Annotations: {"org.opencontainers.image.revision": "<40-hex>"}`, pulls the
  manifest, and asserts the annotation is present. Confirm it fails to compile
  (field absent) / fails the assertion.
- [x] 1.2 GREEN: add `Annotations map[string]string` to `PushOptions`
  (push.go:18) with a doc comment; in `Push`, after building the built-in
  `annotations` map (push.go:120-137) and before `oras.PackManifestOptions`
  (push.go:140), overlay `opts.Annotations` so caller keys overwrite built-ins.
  Guard for nil. Make 1.1 pass.
- [x] 1.3 RED→GREEN: add `TestPush_CallerAnnotationOverridesBuiltin` (caller key
  collides with a built-in, e.g. `org.opencontainers.image.title`) and confirm
  the caller value wins.
- [x] 1.4 RED→GREEN: add `TestPush_NilAnnotations_UnchangedManifest` asserting the
  built-in annotation set is byte-identical when `Annotations` is nil.
- [x] 1.5 REFACTOR + verify: `gofmt`, `go vet ./...`, `go test -cover ./pkg/oci/`;
  confirm 100% branch coverage on the merge logic. Commit
  `feat(oci): add PushOptions.Annotations passthrough`.

### [x] 2.0 `pkg/catalog` — `vendored.json` types + IO

Define the `vendored.json` schema and pure load/validate/upsert + atomic-write
primitives. Pure-core-first; no command wiring yet.

#### 2.0 Proof Artifact(s)

- Test: `go test ./pkg/catalog/ -run TestLoadVendored_RoundTrip -v` passes —
  load→write→load yields an equal struct. Demonstrates schema fidelity to the
  plan's JSON shape (`schemaVersion`, `skills[]` with the 7 fields).
- Test: `go test ./pkg/catalog/ -run TestValidateVendored -v` passes — table
  cases cover bad `schemaVersion`, each missing required field, and a
  non-40-hex / uppercase `commit` rejection plus the valid case. Demonstrates
  the commit-immutability guard (security requirement).
- Test: `go test ./pkg/catalog/ -run TestUpsertVendored -v` passes — append-new,
  replace-existing (`replaced == true`), deterministic ordering. Demonstrates
  idempotent mutation (decision Q2-A).
- Test: `go test ./pkg/catalog/ -run TestWriteVendoredAtomic -v` passes — write
  then reload, stable ordering, temp-file+rename path. Demonstrates durable write.
- Coverage: `go test -cover ./pkg/catalog/` shows 100% branch on
  `ValidateVendored`, `UpsertVendored`, `WriteVendoredAtomic`.

#### 2.0 Tasks

- [x] 2.1 RED: create `pkg/catalog/vendored_test.go` with `TestLoadVendored_RoundTrip`
  asserting `LoadVendored` parses the plan's exact JSON
  (`schemaVersion:1`, entry keys `name`,`namespace`,`repo`,`subpath`,`version`,
  `commit`,`internal_ref`). Fails (types/functions absent).
- [x] 2.2 GREEN: create `pkg/catalog/vendored.go` with `Vendored`
  (`SchemaVersion int json:"schemaVersion"`, `Skills []VendoredEntry json:"skills"`)
  and `VendoredEntry` (the 7 string fields with the exact JSON tags above), plus
  `LoadVendored([]byte) (Vendored, error)`. Make 2.1 pass.
- [x] 2.3 RED→GREEN: add table-driven `TestValidateVendored` covering: unsupported
  `schemaVersion`; each of the 7 fields missing; `commit` not 40 lowercase hex
  (too short, uppercase, non-hex); and a fully-valid entry. Implement
  `ValidateVendored(Vendored) error` (use a `^[0-9a-f]{40}$` regex for commit).
- [x] 2.4 RED→GREEN: add `TestUpsertVendored` (append-new; replace by
  `(namespace,name)` with `replaced==true`; deterministic order sorted by
  `namespace` then `name`). Implement pure
  `UpsertVendored(v Vendored, e VendoredEntry) (Vendored, bool)`.
- [x] 2.5 RED→GREEN: add `TestWriteVendoredAtomic` (validates before write;
  reload equality; stable key/entry order). Implement
  `WriteVendoredAtomic(path string, v Vendored) error` reusing `writeAtomic`
  (write.go:31) and `json.MarshalIndent` with a trailing newline (mirror
  `WriteSkillDetailAtomic`).
- [x] 2.6 REFACTOR + verify: `gofmt`, `go vet ./...`, `go test -cover ./pkg/catalog/`;
  confirm branch targets. Commit `feat(catalog): add vendored.json types and IO`.

### [ ] 3.0 `cmd` — retarget `catalog add` to write `vendored.json`

Turn `catalog add` into a pure `vendored.json` mutator: write only
`vendored.json` via `--vendored`; remove `--catalog`/`--detail-dir`; upsert with
an overwrite warning + `y/n` confirm (`-y` for non-interactive); keep
resolve/fetch/verify unchanged; update docs.

#### 3.0 Proof Artifact(s)

- CLI: `skills-oci catalog add --help` output (captured to
  `docs/specs/02-spec-catalog-sync/proofs/catalog-add-help.txt`) shows
  `--vendored` and `-y`/`--yes` and **no** `--catalog`/`--detail-dir`.
  Demonstrates the retargeted surface.
- Test: `go test ./cmd/ -run TestCatalogAdd_WritesVendored -v` passes — asserts a
  `vendored.json` with the expected entry **and** that no `catalog.json` or
  detail file exists in the temp dir. Demonstrates the new single target +
  non-goal (no catalog/detail writes).
- Test: `go test ./cmd/ -run TestCatalogAdd_OverwriteRequiresConfirm -v` passes —
  existing entry: piped `n` aborts (file unchanged); piped `y` overwrites;
  `--plain` without `-y` exits non-zero with a clear message; `--plain -y`
  overwrites. Demonstrates the confirmation UX across interactive/non-interactive
  (decision Q2-A refinement).
- Test: `go test ./cmd/ -run TestCatalogAdd_DryRun -v` passes — prints resolved
  entry, writes nothing. Demonstrates dry-run preserved.
- Proof log: captured `--plain` run for a 2-skill sequence (add, then
  overwrite-with-`-y`) at
  `docs/specs/02-spec-catalog-sync/proofs/catalog-add-plain-run.txt`.
- Regression: `go test ./...` green, including the intact-but-unused
  `pkg/catalog/detail*` tests.

#### 3.0 Tasks

- [ ] 3.1 RED: in `cmd/catalog_add_test.go`, add `TestCatalogAdd_WritesVendored`
  (DI fakes for resolver/fetcher, fixture upstream with `SKILL.md`) asserting the
  written `vendored.json` shape and the **absence** of `catalog.json`/detail
  files. Fails against current catalog-writing behavior.
- [ ] 3.1a RED cleanup (regression migration — FLAG 1): inventory the existing
  `cmd/catalog_add_test.go` cases that assert the **old** behavior
  (`catalog.json` migrate/append, `--detail-dir` detail writes, `latest_version`
  derivation, `migrateToV2`). Delete or rewrite each to the `vendored.json`
  contract so the suite compiles after 3.2/3.3. Preserve and keep green the
  cases that test still-valid behavior (URL/flag parsing, resolve, SKILL.md
  verify, SSRF `--repo` allow-list, dry-run). Do **not** modify
  `pkg/catalog/detail*_test.go`.
- [ ] 3.2 GREEN: retarget `runCatalogAddWithDeps` — keep Steps 1-5
  (resolveUpstreamInputs, name/internal_ref, resolve, fetch, verify) and
  `extractV2Namespace` for `namespace`; replace Steps 6-12 with: build a
  `catalog.VendoredEntry{name, namespace, repo, subpath, version, commit,
  internal_ref}`, `LoadVendored` existing (empty `Vendored{SchemaVersion:1}` if
  absent), `UpsertVendored`, and `WriteVendoredAtomic`. Make 3.1 pass.
- [ ] 3.3 GREEN (flags): in `newCatalogAddCmd`/`parseAddOpts`, add
  `--vendored` (default `vendored.json`) and `-y`/`--yes` (bool); remove
  `--catalog` and `--detail-dir`; update `addOpts` (`VendoredPath`, `Yes`; drop
  `CatalogPath`, `DetailDir`); update `Short`/`Long`/`Example` text to say
  `vendored.json`.
- [ ] 3.4 RED→GREEN (confirm UX): add `TestCatalogAdd_OverwriteRequiresConfirm`.
  Implement: when `UpsertVendored` reports `replaced`, print a warning to `out`;
  if `o.Yes` proceed; else if `o.Plain` return a non-zero error instructing
  `-y`; else prompt `[y/N]` and read a line from the injected reader
  (`cmd.InOrStdin()`), proceeding only on `y`/`yes`. Thread `--plain` and the
  reader into `runCatalogAddWithDeps`. Resolve before prompting only if needed;
  prefer loading `vendored.json` + checking existence before the network steps so
  a declined overwrite skips the fetch.
- [ ] 3.5 RED→GREEN (dry-run): update/confirm `TestCatalogAdd_DryRun` — prints the
  resolved `VendoredEntry` and writes nothing; keep `--dry-run`.
- [ ] 3.6 REFACTOR (scoped deletion — FLAG 2): delete now-dead code paths in
  `cmd/catalog_add.go` (`buildSkillDetail`, `loadCatalogFile`, `migrateToV2`, and
  helpers such as
  `deriveLatestVersion`/`isSemverTag`/`shortSHA`/`semverTagPattern`/`hasAnySourcePin`).
  **Before deleting each symbol**, confirm zero remaining references repo-wide
  (`rg '\bSymbolName\b' --type go`) and rely on the compiler/`go vet ./...` to
  catch any miss; leave any symbol still referenced in place. Do **not** delete
  or modify `pkg/catalog` exported helpers (`AddEntry`, `WriteCatalogAtomic`) or
  `pkg/catalog/detail.go` — they are out of this slice's scope.
- [ ] 3.7 Docs: update the README "Vendoring third-party skills" section
  (flags table, "writes `vendored.json`" wording, examples); add a "Superseded
  by `docs/specs/02-spec-catalog-sync/`" banner to
  `docs/specs/catalog-sync-plan.md`.
- [ ] 3.8 Proof capture: write `--help` and the 2-skill `--plain` run outputs to
  `docs/specs/02-spec-catalog-sync/proofs/` (placeholder/public values only — no
  tokens).
- [ ] 3.9 Verify + commit: `gofmt`, `go vet ./...`, `go test -cover ./...` green.
  Commit `feat(catalog): retarget \`catalog add\` to write vendored.json`.
