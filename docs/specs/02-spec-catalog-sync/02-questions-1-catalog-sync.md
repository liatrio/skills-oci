# 02 Questions Round 1 - Catalog Sync (skills-oci slice)

Please answer each question below (select one or more options, or add your own
notes). Feel free to add additional context under any question.

Scope note: this spec covers **only the skills-oci changes** in the catalog-sync
plan. All `core` / `skills-platform` work (`vendored.json` loader in core,
`skills-discover sync-plan`, `catalog-sync.yml`, discovery's vendored path,
`FetchSourceRevision`, the `core` half of the data-contract change) is handled
by another agent and is out of scope here.

The plan's "Net-new work, by component → skills-oci" line is:
> **skills-oci:** `PushOptions.Annotations` passthrough (small additive change);
> retarget `catalog add` to write `vendored.json`. … add the two fields to
> `SkillVersion`/`buildSkillDetail` (only relevant if it still writes detail
> elsewhere).

---

## 1. Retargeted `catalog add` output target & flags

Today `catalog add` always writes `catalog.json` (via `--catalog`, default
`catalog.json`) and optionally writes a per-skill detail file (via
`--detail-dir`). The plan says it should become "a pure `vendored.json`
mutator" and "stop writing detail files." How should the command's output and
flags change?

- [x] (A) Add a `--vendored` flag (default `vendored.json`); `catalog add`
      writes **only** `vendored.json`. Remove the `--catalog` and `--detail-dir`
      flags and their write paths. Resolution + `SKILL.md` verification stay
      exactly as-is.  ← **SELECTED**
- [ ] (B) Dual-write: keep `--catalog` writing `catalog.json` **and** add
      `--vendored` writing `vendored.json`.
- [ ] (C) Repurpose the existing `--catalog` flag to point at `vendored.json`
      (rename its default + semantics); single flag, no new `--vendored`.
- [ ] (D) Other (describe)

**Recommended answer(s):** [(A)]

**Why these are recommended:**

- `(A)` matches the plan literally: the registry/discovery is the single writer
  of `catalog.json` + detail files, so `catalog add` writing either of those
  would create a second writer the plan explicitly removes. "Pure
  `vendored.json` mutator" + "stop writing detail files" maps 1:1 to dropping
  `--catalog` and `--detail-dir`.
- `(B)` re-introduces the dual-writer the plan's "single writer" principle is
  designed to eliminate, and would leave stale `catalog.json` rows that
  discovery now owns.
- `(C)` is terser but silently changes what an existing flag does, which is a
  confusing breaking change for any current scripting; a new, clearly-named
  `--vendored` flag is less surprising.

---

## 2. `vendored.json` mutation semantics (re-adding an existing skill)

`catalog add` appends an entry. When an entry for the same skill already exists
in `vendored.json`, what should happen? (Identity key recommended:
`(namespace, name)`.)

- [x] (A) **Upsert by `(namespace, name)`:** replace the existing entry's pin in
      place (new `repo`/`subpath`/`version`/`commit`), preserving array
      position; append if new. Keep `skills[]` in a deterministic order (e.g.
      sorted by `namespace` then `name`) so diffs are clean and reviewable.
      ← **SELECTED, with a refinement:** when an entry already exists, *warn the
      user it will be overwritten and prompt `y/n` before proceeding*. In a
      non-interactive context (no TTY / `--plain` / piped input) require a `-y`
      / `--yes` flag to proceed; without it, error out and tell the user to pass
      `-y`.
- [ ] (B) **Append always:** add a new entry even if one already exists (allows
      duplicate `(namespace, name)`).
- [ ] (C) **Error on duplicate:** refuse to add if `(namespace, name)` already
      exists; require the user to edit `vendored.json` by hand for bumps.
- [ ] (D) Other (describe)

**Recommended answer(s):** [(A)]

**Why these are recommended:**

- `(A)` gives true "mutator" semantics: re-running `catalog add` for a
  version bump produces a minimal, reviewable one-line diff (the load-bearing
  PR-review artifact in the plan). It is idempotent and the natural input for
  Renovate-style bumps later.
- `(B)` lets duplicate rows accumulate; discovery would then have to pick a
  winner, which the plan does not define.
- `(C)` makes the common case (bumping a vendored skill) require hand-editing,
  defeating the point of `catalog add` as the authoring tool.

---

## 3. Is the `digest`/`commit` data-contract change in skills-oci's scope?

The plan adds `digest` + `commit` to each `versions[]` entry of the per-skill
**detail** file, but flags the skills-oci half as "only relevant if it still
writes detail elsewhere." If Q1 = (A), `catalog add` no longer writes detail
files at all, so skills-oci would have no producer of those fields. What should
this spec do?

- [x] (A) **Out of scope; stop using detail, keep the package intact.** Do not
      add `digest`/`commit` to `SkillVersion`. Remove the now-unused
      detail-writing path from `catalog add` (the `--detail-dir` block +
      `buildSkillDetail` helper), but **leave** `pkg/catalog/detail.go`
      (`SkillDetail`, `SkillVersion`, `WriteSkillDetailAtomic`,
      `ValidateSkillDetail`) and its tests intact. The data-contract amendment
      lands in `core` (out of scope).  ← **SELECTED**
- [ ] (B) **Out of scope; delete dead detail code.** Same as (A) but also delete
      `pkg/catalog/detail.go` and its tests entirely, since nothing in
      skills-oci writes detail anymore.
- [ ] (C) **In scope; add the fields anyway.** Add `Digest` and `Commit` to
      `SkillVersion` and keep `buildSkillDetail`/detail-writing in `catalog add`
      (i.e. answer Q1 so detail is still written), populating `commit` from the
      resolved SHA. `digest` would be left empty (no push happens in
      `catalog add`).
- [ ] (D) Other (describe)

**Recommended answer(s):** [(A)]

**Why these are recommended:**

- `(A)` honors the plan's conditional ("only relevant if it still writes detail
  elsewhere") — once `catalog add` stops writing detail, the field change has no
  skills-oci producer, so adding it here would be speculative. Keeping the
  package intact avoids deleting code that other commands or future work may
  use, and avoids an unrelated, larger deletion in this spec.
- `(B)` is cleaner-on-paper but risks removing types still referenced elsewhere;
  a deletion of that size deserves its own change, not a rider on this one.
- `(C)` contradicts Q1=(A) by keeping `catalog add` a detail-writer, and would
  emit a `digest`-less, `commit`-only detail row that duplicates what discovery
  (the single writer) now produces authoritatively — exactly the dual-writer the
  plan removes.

---

## 4. `PushOptions.Annotations` merge precedence

The passthrough adds `Annotations map[string]string` to `PushOptions`. When a
caller-supplied key collides with a built-in key the pusher already sets (e.g.
`org.opencontainers.image.*`, `AnnotationSkillName`), which wins?

- [x] (A) **Caller wins:** caller-supplied keys override built-ins, so
      provenance (`org.opencontainers.image.source`/`.revision`) supplied by the
      caller can never be silently dropped. Built-ins fill only keys the caller
      didn't set.  ← **SELECTED**
- [ ] (B) **Built-ins win:** caller may only add keys the pusher doesn't already
      set; collisions keep the built-in value.
- [ ] (C) **Error on collision:** reject the push if a caller key collides with a
      built-in.
- [ ] (D) Other (describe)

**Recommended answer(s):** [(A)]

**Why these are recommended:**

- `(A)` makes the passthrough actually useful for its stated purpose
  (provenance), and is the least surprising for a caller that explicitly set a
  value. It is also the behavior the earlier skills-oci draft assumed.
- `(B)` could silently drop caller provenance, which is the one thing this
  passthrough exists to carry.
- `(C)` is the safest against accidents but turns a convenience feature into a
  foot-gun for the CI workflow that will set these keys on every push.
