# Task 03 Proofs — Retarget `catalog add` to write `vendored.json`

## Task Summary

This task proves `catalog add` is now a pure `vendored.json` mutator: it keeps
its existing resolve → fetch → verify behavior but replaces the
`catalog.json` + per-skill-detail write path with a single `vendored.json`
upsert, adds an overwrite-confirmation UX, exposes `--vendored` and `-y`/`--yes`,
and removes `--catalog`/`--detail-dir`. Now-dead helpers
(`buildSkillDetail`, `loadCatalogFile`, `migrateToV2`, `deriveLatestVersion`,
etc.) were deleted; `pkg/catalog/detail.go` and `pkg/catalog` exported helpers
were left intact per the slice's non-goals (decision Q3-A).

## What This Task Proves

- `catalog add` writes a valid `vendored.json` entry and writes **no**
  `catalog.json` or detail file.
- Re-adding an existing `(namespace, name)` overwrites in place, gated by a
  confirmation: interactive prompt (`y`/`N`, empty = abort), `--plain`/non-TTY
  requires `-y` and otherwise exits non-zero, `-y` overwrites.
- `--dry-run` prints the resolved entry plus the would-be action and writes
  nothing.
- Branch inputs still record the resolved SHA in `version` (no mutable branch
  name persisted); SSRF `--repo` guard, URL/flag parsing, timeout plumbing, and
  SKILL.md verification all still pass.
- The retargeted CLI surface shows `--vendored` and `-y`/`--yes` and no longer
  shows `--catalog`/`--detail-dir`.

## Evidence Summary

- The full `cmd` test suite passes, including the new
  `TestCatalogAdd_WritesVendored` (asserts entry + absence of catalog/detail),
  `TestCatalogAdd_OverwriteRequiresConfirm` (5 sub-cases across interactive and
  `--plain`), and `TestCatalogAdd_DryRun`; preserved cases (SSRF, parse, fetch,
  timeout, namespace precedence) remain green.
- `go vet ./...` clean; `gofmt` clean; `go test ./...` green across all
  packages.
- A live `--plain` run against the public anthropics/skills repo demonstrates
  two adds, a refused overwrite (exit 1), and an overwrite with `-y`.

## Artifact: `cmd` test suite (new + migrated cases)

**What it proves:** The new vendored.json contract behavior and the preserved
resolve/verify/SSRF/timeout behavior both hold.

**Why it matters:** This is the user-facing slice; the migrated test file is the
regression guard that the retarget didn't drop still-valid behavior (FLAG 1).

**Command:**

~~~bash
go test ./cmd/ -run 'TestCatalogAdd|TestRunCatalogAddWithDeps|TestParseAddOpts|TestResolve|TestExtractV2Namespace|TestLoadVendoredFile' -v
~~~

**Result summary:** All pass. Coverage on the new orchestration code:
`runCatalogAddWithDeps` 91.3%, `confirmOverwrite` 100%, `loadVendoredFile`
100%, `vendoredHasEntry`/`extractV2Namespace`/`resolveUpstreamInputs`/
`resolveInternalRef`/`parseAddOpts` 100%. (The thin real `Fetch`/`ResolveRef`/
`runCatalogAdd` wiring shims remain 0% — they require a live network/Cobra and
were untested before this slice too.)

## Artifact: Retargeted `--help` surface

**What it proves:** `--vendored` and `-y`/`--yes` are present; `--catalog` and
`--detail-dir` are gone.

**Why it matters:** Directly demonstrates the FR "expose `--vendored`; remove
`--catalog`/`--detail-dir`."

**Artifact path:** `docs/specs/02-spec-catalog-sync/proofs/catalog-add-help.txt`

**Result summary:** The flags block lists `--vendored string (default
"vendored.json")` and `-y, --yes`, and contains no `--catalog`/`--detail-dir`.

~~~text
Flags:
      --dry-run               Print the would-be entry and exit without writing vendored.json
      --vendored string       Path to vendored.json (default "vendored.json")
  -y, --yes                   Overwrite an existing entry without prompting (required to overwrite in non-interactive/--plain mode)
~~~

## Artifact: Live `--plain` 2-skill run with overwrite

**What it proves:** End-to-end, the command resolves a real upstream, writes
`vendored.json`, refuses a `--plain` overwrite without `-y` (exit 1), and
overwrites with `-y` — producing the exact cross-repo JSON contract shape in
deterministic order.

**Why it matters:** Confirms the scripting/CI contract works against a real
registry-free GitHub resolve+fetch, not just fakes.

**Artifact path:** `docs/specs/02-spec-catalog-sync/proofs/catalog-add-plain-run.txt`

**Result summary:** Two adds (`algorithmic-art`, `pdf`) land sorted in
`vendored.json`; the refused overwrite prints `entry liatrio/algorithmic-art
already exists ... pass -y/--yes` and exits 1; `-y` overwrites in place. `version`
and `commit` both record the resolved SHA for the mutable `main` ref.

## Note on the superseded-draft banner (task 3.7)

`docs/specs/catalog-sync-plan.md` does not exist on this branch, so the
"superseded by" banner sub-item is a no-op — there is no stale draft to annotate.

## Reviewer Conclusion

`catalog add` is now a pure `vendored.json` mutator with a tested
overwrite-confirmation UX and the retargeted flag surface, its resolve/verify
behavior preserved, dead code removed without touching the out-of-scope
`detail.go`/`pkg/catalog` helpers, and the whole suite + `go vet` green.
