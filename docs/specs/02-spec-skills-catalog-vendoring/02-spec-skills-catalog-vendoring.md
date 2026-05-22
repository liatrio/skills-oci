# 02-spec-skills-catalog-vendoring.md

## Introduction/Overview

The `skills-oci` CLI cannot today vendor a 3rd-party skill (e.g. an Anthropic-published skill distributed as a directory inside a Git repo) into an internal OCI registry without a manual `git clone && skills-oci push` sequence. That manual flow has no audit trail, no Renovate-friendly version anchor, and no enforcement that what was pushed matches what was reviewed. This spec adds two new subcommands — `skills-oci catalog add` and `skills-oci catalog sync` — plus a documented two-file catalog format (`catalog.json` + `catalog-lock.json`) that mirrors the existing `skills.json` / `skills.lock.json` pattern. The PRD that drives this spec is [`02-prd-skills-catalog-vendoring.md`](../../how-to/) (kept in this folder until merge) and traces to GitHub issue [`liatrio/skills-platform#62`](https://github.com/liatrio/skills-platform/issues/62).

**Primary goal:** make a platform engineer's full vendoring workflow — author a catalog entry from an upstream GitHub `tree` URL, get the entry reviewed in a PR, and reconcile every entry in the catalog to the internal registry on merge — a declarative, SHA-pinned, Renovate-compatible operation with zero manual `git clone && push` steps.

## Goals

- Ship `catalog add` that resolves a GitHub `tree` URL (or component flags) to a 40-hex commit SHA, verifies the upstream subpath contains `SKILL.md`, and appends the entry to `catalog.json` atomically, without ever contacting the destination registry.
- Ship `catalog sync` that reads `catalog.json`, clones each entry's upstream at its declared SHA into a temp directory, pushes to the internal registry with `org.opencontainers.image.source` and `org.opencontainers.image.licenses` annotations, and writes `catalog-lock.json` atomically — running entries with bounded parallelism (default 4) and skipping entries whose lockfile state already matches.
- Freeze the catalog data contract at `schemaVersion: 1` in a new `docs/skills-catalog-data-contract.md` document, modeled on `docs/telemetry-data-contract.md`, including a worked Renovate config snippet and a worked GitHub Actions workflow snippet.
- Enforce SHA-only refs at validation time: branches, mutable tags (`latest`, `main`, `master`, `HEAD`, empty), and non-40-hex commits are rejected. The audit story depends on this; it is the load-bearing security property.
- Extend the existing `pkg/telemetry/` pipeline additively with a new `catalog.synced` event type emitted once per per-entry outcome (`synced` / `skipped` / `failed`).
- Surface a clean failure mode for upstream SKILL.md without a license field: fail by default, with an `--allow-missing-license` flag (also settable in `.skills-oci.yaml`) for explicit opt-in.

## User Stories

- **As a platform engineer onboarding a 3rd-party skill**, I want to run `skills-oci catalog add <github-tree-url>` and have the entry resolved, verified, and appended to `catalog.json` in one command, so that the most common authoring task takes seconds and the diff a reviewer sees is minimal.
- **As a platform engineer reviewing a Renovate version-bump PR**, I want both the `version` tag and the `commit` SHA to update together via the `pinDigests` pattern, so that one click into the new SHA on GitHub is the entire human checkpoint and CI has no discretion over what content flows in.
- **As a CI engineer**, I want `catalog sync` to be one command that replaces every step of the manual `git clone && push` flow — reading the declared catalog, cloning at the pinned SHA, pushing with provenance annotations, and updating the lockfile atomically — so that the GitHub Actions workflow is short, readable, and fast.
- **As a security reviewer**, I want every vendored OCI artifact to carry `org.opencontainers.image.source` pointing at the immutable upstream SHA and `org.opencontainers.image.licenses` reflecting the upstream's declared license, so that downstream license-compliance tools can audit the registry without chasing source files.
- **As a developer consuming a vendored skill**, I want the experience to be identical to consuming an internally-authored skill — `skills-oci add ghcr.io/<org>/skills/<name>:<version>` — so that 3rd-party origin is a platform-team concern, not mine.

## Demoable Units of Work

### Unit 1: Catalog data contract and `pkg/catalog/`

**Purpose:** Build the catalog data model — types, validation, atomic file IO — and publish the frozen `schemaVersion: 1` contract that all consumers (humans, Renovate, CI) write against. Pure code; no network, no Cobra. This is the foundation everything else depends on; it must be correct in isolation before any command code is written.

**Functional Requirements:**

- The system shall provide `pkg/catalog.Catalog`, `Entry`, `Lock`, `LockEntry` Go types whose JSON marshaling produces files conforming exactly to the field tables in `docs/skills-catalog-data-contract.md` (`schemaVersion: 1` / `lockfileVersion: 1`).
- The system shall provide a pure `Load([]byte) (Catalog, error)` and `LoadLock([]byte) (Lock, error)` for parsing on-disk files.
- The system shall provide a pure `Validate(Catalog) error` that enforces, at minimum: `commit` matches `^[a-f0-9]{40}$`; `version` is not `latest`, `main`, `master`, `HEAD`, or empty; `repo` does not contain `https://` or `/tree/`; `subpath` has no leading slash; no two entries share the same `name`. Each violation returns a field-named error.
- The system shall provide a pure `AddEntry(c Catalog, e Entry) (Catalog, error)` that appends, re-validates, and returns a new `Catalog` value without mutating the input. Duplicate-name rejection is delegated to `Validate`.
- The system shall provide `WriteCatalogAtomic(path string, c Catalog) error` and `WriteLockAtomic(path string, l Lock) error` that write to a temp file in the same directory and `os.Rename` into place, preserving stable JSON key order so diffs are minimal across `catalog add` invocations and Renovate updates.
- The system shall publish `docs/skills-catalog-data-contract.md` containing: the v1 field table for both files, the worked Renovate `customManagers` snippet from the PRD, the worked GitHub Actions workflow snippet, the writer/reader matrix (`humans+Renovate write catalog.json`; `CI writes catalog-lock.json`), and the exit-code semantics (`1` = some entries failed; `2` = lockfile write failed, registry diverged).

**Proof Artifacts:**

- Test: `pkg/catalog/validate_test.go` table-driven tests cover every rejection path (each bad value of `commit`, each forbidden `version`, malformed `repo`, malformed `subpath`, duplicate `name`) and the all-fields-valid happy path — demonstrating 100% branch coverage of the validator.
- Test: `pkg/catalog/write_test.go` asserts that two `WriteCatalogAtomic` calls with the same `Catalog` value produce byte-identical files (stable key order), and that a simulated rename failure leaves no partial file behind — demonstrating durability and diff-stability.
- Test: `pkg/catalog/add_test.go` asserts `AddEntry` appends at the tail without mutating the input, and that appending a duplicate-name entry returns the validator's error verbatim.
- Doc: `docs/skills-catalog-data-contract.md` exists and is reviewed in the same PR as the implementation — demonstrating the contract is the source of truth, not the code.

### Unit 2: Upstream SCM fetch and resolve (`pkg/scm/`)

**Purpose:** Build the IO-edge package that talks to upstream GitHub: parse `tree` URLs into components, resolve a tag to a commit SHA via `git ls-remote`, and shallow-fetch a single SHA into a temp directory for the caller to push. Reusable by both `catalog add` (verify-at-author-time) and `catalog sync` (clone-before-push). No catalog awareness; no Cobra.

**Functional Requirements:**

- The system shall provide `pkg/scm.ParseGitHubTreeURL(string) (owner, repo, refOrCommit, subpath string, err error)` as a pure function accepting `https://github.com/<owner>/<repo>/tree/<ref>/<subpath>` and rejecting non-`github.com` hosts, non-`tree` URLs (e.g. `blob`, `releases`), missing subpath, and malformed URLs with field-named errors.
- The system shall provide `pkg/scm.ResolveTag(ctx, repo, tag string) (commit string, err error)` that returns the input unchanged when it is already 40-hex, and otherwise uses `github.com/go-git/go-git/v5`'s `Remote.List` over HTTPS to ls-remote the upstream, returning the peeled SHA for annotated tags (`refs/tags/<tag>^{}`) or the SHA for lightweight tags (`refs/tags/<tag>`), and `fmt.Errorf("tag %q not found on %s", tag, repo)` on miss.
- The system shall provide `pkg/scm.Fetch(ctx, ref SourceRef, dst string) error` that initializes an empty repo at `dst`, adds `origin = https://github.com/<owner>/<repo>.git`, runs `git fetch --depth=1 origin <commit>`, checks out `FETCH_HEAD`, verifies `<dst>/<subpath>/SKILL.md` exists, and returns the absolute path to `<dst>/<subpath>` for the caller to push.
- The system shall reject non-`github.com` hosts at the `Fetch` boundary, mirroring `ParseGitHubTreeURL`'s host check so a caller that constructed `SourceRef` by hand cannot bypass it.
- The system shall use stdlib `net/http` defaults (no auth) for v1; private-repo support is explicitly out of scope.
- The system shall clean up the destination temp directory on error, on context cancellation, and on success — the caller passes a temp dir and the package owns its lifecycle within the `Fetch` call.

**Proof Artifacts:**

- Test: `pkg/scm/parse_test.go` is table-driven and covers happy paths (semver tag, branch name, 40-hex SHA, multi-segment subpath, trailing slash), and every rejection path (`gitlab.com` host, `blob` segment, empty subpath, malformed URL) — demonstrating 100% branch coverage of the URL parser.
- Test: `pkg/scm/resolve_test.go` uses a `file://`-served temp repo to verify the lightweight-tag and annotated-tag paths, plus a tag-not-found case, plus the 40-hex-passthrough fast path that performs no network call — demonstrating contract conformance with `git`'s ref-peeling rules.
- Test: `pkg/scm/fetch_test.go` covers the happy path against a `file://` fixture repo (subpath fetched, SKILL.md verified, temp dir contents asserted) and the failure paths (subpath missing in upstream, SKILL.md missing in subpath, non-`github.com` host rejected). A separate `httptest.Server`-backed test exercises the HTTP code path so auth-header and redirect bugs surface — demonstrating that both fetch transports are exercised.
- Test: a context-cancellation test asserts that `Fetch` returns promptly when the caller's context is cancelled mid-fetch and that the destination temp dir is removed.

### Unit 3: `catalog add` command + project configuration

**Purpose:** First user-visible surface. Compose `pkg/catalog` and `pkg/scm` into a Cobra subcommand that authors a new catalog entry from a GitHub URL (or component flags), with cheap-and-decisive checks before network calls and the file write only at the end. Introduces `pkg/config/` for `.skills-oci.yaml` reading.

**Functional Requirements:**

- The system shall provide a Cobra subcommand `skills-oci catalog add [URL]` with flags `--repo`, `--subpath`, `--version`, `--name`, `--internal-ref`, `--namespace`, `--catalog` (default `catalog.json`), `--dry-run`, inheriting global `--plain` and `--plain-http`.
- The system shall accept the URL form (positional argument fills `--repo`, `--subpath`, `--version`) or the flag form (at minimum `--repo` + `--subpath` + `--version`) but never both — passing both rejects with an ambiguous-input error.
- The system shall derive `name` from the last segment of `subpath` unless `--name` is set, and derive `internal_ref` as `<namespace>/<name>` unless `--internal-ref` is set, where `namespace` resolves in precedence order: `--namespace` flag > `.skills-oci.yaml` `catalog.default_namespace` > `SKILLS_OCI_DEFAULT_NAMESPACE` env var > error `"no default namespace configured; pass --namespace, set in .skills-oci.yaml, or export SKILLS_OCI_DEFAULT_NAMESPACE"`.
- The system shall perform the operations in this order, exiting non-zero on any failure with no file write: (1) parse positional/flags, (2) derive defaults, (3) `pkg/scm.ResolveTag` (passes through 40-hex SHAs without network), (4) `pkg/scm.Fetch` to a temp dir + verify `SKILL.md`, (5) read upstream SKILL.md frontmatter and surface upstream `name`/`version`/`license` to stdout, (6) load existing `catalog.json` (or initialize empty `Catalog{SchemaVersion: 1}`), (7) `pkg/catalog.AddEntry`, (8) `--dry-run` short-circuit prints the would-be entry as JSON and exits 0, (9) `pkg/catalog.WriteCatalogAtomic`, (10) print one-line summary.
- The system shall never contact the destination registry from `catalog add` and never modify `catalog-lock.json` — authoring and reconciliation are strictly separated.
- The system shall provide a new `pkg/config/` package with `Load([]byte) (Config, error)` that parses `.skills-oci.yaml`, tolerates absent file (caller passes empty bytes → zero-value config), logs unknown top-level keys to stderr without erroring (forward-compat), and rejects type mismatches with field-named errors. v1 keys: `catalog.default_namespace` (string), `catalog.allow_missing_license` (bool), `catalog.concurrency` (positive int).
- The `--plain` output of `catalog add` shall match this exact format (one line per step, two-space indent for nested context):

  ```
  resolving anthropics/skills@v1.0.0
    → commit bc6708cb…30f86abc12340
  fetching subpath skills/create-skill
  verifying SKILL.md
    upstream name: create-skill
    upstream version: 1.0.0
    upstream license: Apache-2.0
  catalog add: appended entry "create-skill" to catalog.json
  ```

**Proof Artifacts:**

- Test: `cmd/catalog_add_test.go` integration tests (Cobra-level) drive `catalog add` against a `file://`-served fixture upstream repo and assert that `catalog.json` contains the expected entry with resolved SHA, derived name, and namespace-derived `internal_ref` — demonstrating end-to-end authoring on the happy path.
- Test: a namespace-precedence table-driven test asserts `--namespace` wins over `.skills-oci.yaml`, which wins over `SKILLS_OCI_DEFAULT_NAMESPACE`, which wins over absent (which rejects) — demonstrating the precedence chain in the spec.
- Test: failure-path tests assert that an upstream subpath without `SKILL.md`, a tag not found upstream, and a duplicate `name` each reject with the documented error and leave `catalog.json` unchanged — demonstrating the no-partial-state guarantee.
- Test: `pkg/config/load_test.go` covers valid YAML, empty input (returns zero-value), unknown key (warns but succeeds), type mismatch (rejects with field-named error), and invalid `concurrency` (zero / negative rejects).
- CLI: running `skills-oci catalog add https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill` against a real upstream (network-permitting) prints the documented `--plain` output and writes a clean diff to `catalog.json` — demonstrating end-to-end real-world authoring.

### Unit 4: `catalog sync` command, telemetry, CI integration

**Purpose:** Second user-visible surface and the operational payoff. Reconcile every entry in `catalog.json` to the destination registry with bounded parallelism, push provenance annotations, write `catalog-lock.json` atomically, emit one `catalog.synced` telemetry event per per-entry outcome, and ship the worked GitHub Actions workflow snippet that consumers will copy.

**Functional Requirements:**

- The system shall provide a Cobra subcommand `skills-oci catalog sync` with flags `--dry-run`, `--only <name>[,<name>…]`, `--catalog` (default `catalog.json`), `--concurrency int` (default from `.skills-oci.yaml`, else `4`), `--allow-missing-license` (default from `.skills-oci.yaml`, else `false`), inheriting global `--plain` and `--plain-http`.
- The system shall provide `pkg/catalog.Sync(ctx, opts) (Result, error)` as the orchestrator, with bounded parallelism via `golang.org/x/sync/errgroup`'s `SetLimit(opts.Concurrency)`. Per-entry the orchestrator: creates a temp dir under `os.TempDir()`, defers cleanup, calls `pkg/scm.Fetch`, parses the upstream SKILL.md frontmatter, runs the license-missing check (see below), calls `pkg/oci.Push` with annotations `org.opencontainers.image.source = https://github.com/<owner>/<repo>/tree/<commit>/<subpath>` and `org.opencontainers.image.licenses = <upstream license>` (the licenses annotation omitted only when `AllowMissingLicense` is true and the upstream license is empty), and records a per-entry `{Name, Outcome, Commit, Digest, Err}` result with `Outcome` in `{synced, skipped, failed}`.
- The system shall, by default, fail any entry whose upstream SKILL.md frontmatter has no `license` field with the error `entry %q: upstream SKILL.md missing required 'license' field`. With `--allow-missing-license` (or config equivalent), the entry succeeds and the artifact is pushed with the `licenses` annotation omitted.
- The system shall skip entries whose declared `commit` exactly matches the existing `catalog-lock.json` entry for that `name` (recorded outcome `synced`); skipped entries do not contact upstream or the registry.
- The system shall write `catalog-lock.json` atomically after all entries complete, merging successful pushes from this run with prior lock entries for failed/skipped entries (failed entries do not overwrite prior good lock state with stale data).
- The system shall exit `0` if all entries succeeded or were skipped, `1` if any entry failed, and `2` if the lockfile write itself failed (worst state — registry diverged from lockfile, manual reconciliation needed).
- The system shall emit one `telemetry.Event` of type `catalog.synced` per per-entry outcome (including `failed` and `skipped`), with payload fields `name`, `internal_ref`, `tag`, `commit`, `digest` (empty for non-`synced`), `upstream_repo`, `outcome`. Emissions fire concurrently as entries complete (matching the existing best-effort philosophy); the `pkg/telemetry/` opt-out and 2-second timeout behaviors apply unchanged.
- The system shall update `docs/telemetry-data-contract.md` additively with a new `catalog.synced` event-type section; `skill.downloaded` semantics are unchanged.
- The system shall publish the canonical GitHub Actions workflow file as a code block inside `docs/skills-catalog-data-contract.md`, targeting GitHub Actions + GHCR + OIDC: a `pull_request` job runs `catalog sync --dry-run --plain`, and a `push: branches: [main]` job runs `catalog sync --plain` and commits the updated `catalog-lock.json` back via a bot identity (`skills-oci-bot`).
- The `--plain` output of `catalog sync` shall match this exact format:

  ```
  catalog sync starting (N entries)
  [1/N] <name> cloning <repo>@<commit-short>
  [1/N] <name> pushing <internal_ref>:<version>
  [1/N] <name> ok <digest-short>
  [2/N] <name> skipped (already at <commit-short>)
  [3/N] <name> failed: <error>
  catalog sync done: synced=X skipped=Y failed=Z
  ```

- The `catalog sync` interactive (non-`--plain`) output shall be minimum-viable: one status line per entry, updated in place as the entry transitions through `queued → cloning → pushing → done/failed/skipped`. No spinners, no color blocks; plain text is the canonical UX per `CLAUDE.md`.

**Proof Artifacts:**

- Test: `pkg/catalog/sync_test.go` against test-double `Fetcher` and `Pusher` covers: all-succeed (lockfile contains all entries), one-fail (failed entry preserved-not-overwritten in lockfile, other entries complete), skip-when-matches-lock (pusher never called for skipped entry), `--only` filtering (unnamed entries absent from result), `--dry-run` (pusher never called, lockfile never written), concurrency limit honored (channel-based fakes confirm at most N in flight) — demonstrating orchestrator correctness without registry or git network involvement.
- Test: license-missing test pair — default behavior fails the entry with the documented error and leaves lockfile unchanged for that entry; with `AllowMissingLicense: true`, the same fixture succeeds, the pushed annotation map does not contain `org.opencontainers.image.licenses`, and the lockfile records `synced`.
- Test: `cmd/catalog_sync_test.go` end-to-end integration tests use the in-process registry from `cmd/testregistry_test.go` plus a `file://`-fixture upstream and assert: 2-entry catalog all-success (lockfile written, output matches the documented `--plain` format, exit `0`); 2-entry catalog one-failure (exit `1`, lockfile written with one entry, other preserved or absent); lockfile-write-failure simulated (exit `2`).
- Test: telemetry assertion — successful entries emit `catalog.synced` with `outcome=synced`; failed entries emit `outcome=failed`; skipped entries emit `outcome=skipped`. Assertion is against an in-process collector (`httptest.Server`) mirroring the pattern from `pkg/oci/pull_telemetry_test.go`.
- Doc: `docs/skills-catalog-data-contract.md` includes the workflow snippet and the snippet's properties are documented inline (path filter on `catalog.json` only so the bot's lockfile commit does not retrigger CI; OIDC for registry auth; bot identity).
- Doc: `docs/telemetry-data-contract.md` has a new `catalog.synced` section describing the event payload.
- CLI: running `skills-oci catalog sync --plain` end-to-end (locally against a registry like `localhost:5000`) produces the documented output, pushes annotated artifacts, and writes `catalog-lock.json` with manifest digests — demonstrating the full reconciliation loop.

## Non-Goals (Out of Scope)

1. **`catalog remove` and `catalog init` subcommands.** Removing an entry is a hand-edit and `catalog init` is `echo '{"schemaVersion":1,"skills":[]}'` — the CLI value-add is too thin. Adopt either if real friction emerges.
2. **A "review inflated skills" flow.** Reviewers approve a catalog PR by clicking through the SHA on GitHub at PR time. A future `catalog review` subcommand (or a GitHub Action that posts the rendered SKILL.md to the PR) is deferred until the review-burden cost actually shows up.
3. **SCM hosts other than `github.com`.** `pkg/scm` is host-agnostic at the boundary but v1 validation rejects non-GitHub hosts. GitLab/Bitbucket are additive when first asked for.
4. **Authentication for private upstream repos.** v1 fetches anonymously. SSH agent, HTTPS basic auth, GitHub token plumbing are all out of scope; private-repo support is its own design.
5. **SHA-256 git refs.** Validation accepts 40-hex only in v1; the schema is forward-compatible for 64-hex when SHA-256 git becomes broadly supported.
6. **Sparse-checkout optimization.** `--depth=1` is cheap on GitHub; sparse-checkout is a v2 perf option if monorepo fetches become painful.
7. **`catalog verify` subcommand.** A periodic check that every `internal_ref:tag` still resolves to the recorded digest is useful for detecting registry drift but separable; ship in a follow-up.
8. **Sigstore / cosign signatures or in-toto attestations.** SHA-pinning + the OCI source annotation is the v1 supply-chain story. Signed attestations are a separate compliance initiative; the threat-model gap is acknowledged.
9. **License compatibility enforcement.** v1 surfaces `org.opencontainers.image.licenses` on the pushed artifact and fails when the upstream declares no license at all; whether a *declared* license is *allowed* is a downstream compliance tool's job.
10. **A pre-approved vendor allow-list.** Vetting is per-skill at PR review time; an allow-list creates a soft trust boundary that's easy to widen accidentally.
11. **Registry-side artifact deletion.** `catalog sync` pushes only; it never deletes tags or manifests. Un-vendoring includes a manual registry-side step by design (see the PRD's *Un-vendoring a skill* section).
12. **Implicit version-bumping or `HEAD`-chasing.** All `version`+`commit` changes flow through Renovate PRs reviewed by humans.
13. **Renovate config generator and GHA workflow generator.** The data contract publishes both snippets; the CLI does not scaffold them.
14. **User-level `.skills-oci.yaml` (`~/.config/skills-oci/config.yaml`) fallback.** v1 reads project-level config only. Add user-level if real demand surfaces.
15. **Full-feature TUI for `catalog sync`.** v1 is minimum-viable: one line per entry, plain-text updates, no spinners. Polish later if usage data justifies it.
16. **`push --from-git` shortcut.** A one-shot vendoring path that bypasses the catalog also bypasses the audit story; intentionally not provided.

## Design Considerations

No UI/UX mockups. The visible surface is plain-text command output (`--plain` format committed verbatim in Units 3 and 4) and a thin in-place status TUI for `catalog sync` (one line per entry, no spinners). The data-contract document (`docs/skills-catalog-data-contract.md`) is a documentation artifact, not a UI surface; readability and copy-pasteability of the Renovate and GHA snippets are the only design requirements there.

## Repository Standards

Implementation must follow the patterns already established in this repository, per `CLAUDE.md`:

- **Strict TDD**: every functional requirement above is implemented red-green-refactor. No production code without a failing test first.
- **Module layout**: new code lives under `pkg/catalog/`, `pkg/scm/`, `pkg/config/`, `pkg/tui/catalog/`, mirroring the existing `pkg/skill/`, `pkg/oci/`, `pkg/tui/` package style. No Cobra dependencies in `pkg/`.
- **Cobra commands stay in `cmd/`**: command-level wiring (flag parsing, env-var read, plain-mode dispatch) belongs in `cmd/catalog.go`, with `pkg/catalog/`, `pkg/scm/`, `pkg/config/` receiving structured arguments — same separation as `cmd/add.go` ↔ `pkg/oci/pull.go`.
- **TUI vs `--plain` parity**: both `catalog add` and `catalog sync` must work correctly under `--plain` (CI/scripting mode); the TUI is a presentation layer over the same underlying operations.
- **Pure core, IO at edges**: `pkg/catalog/validate.go`, `pkg/catalog/add.go`, `pkg/scm/parse.go` are pure functions; all filesystem and HTTP IO is in `pkg/catalog/write.go`, `pkg/catalog/lock.go`, `pkg/scm/resolve.go`, `pkg/scm/fetch.go`.
- **One concern per package**: `pkg/catalog/` does not talk to upstream Git; `pkg/scm/` does not parse SKILL.md; `pkg/tui/catalog/` does not perform IO directly — it dispatches.
- **Error handling**: wrap errors with context (`fmt.Errorf("syncing %q: %w", entry.Name, err)`), matching the rest of the codebase. Never swallow.
- **Atomic file writes**: temp file in the same directory + `os.Rename`, matching the deterministic-artifact discipline applied to tar.gz layers elsewhere.
- **Coverage**: ≥ 90% line coverage on new code; 100% branch coverage on `pkg/catalog/validate.go`, `pkg/catalog/add.go`, `pkg/catalog/write.go::WriteCatalogAtomic`, `pkg/catalog/lock.go::WriteLockAtomic`, `pkg/scm/parse.go::ParseGitHubTreeURL`, `pkg/scm/resolve.go::ResolveTag`, `pkg/scm/fetch.go::Fetch` (host check + SKILL.md check), `pkg/config/load.go`.
- **Conventional commits**: `feat(catalog):`, `feat(scm):`, `feat(config):`, `docs(catalog):`, `test(catalog):` etc.
- **Quality gates**: `go test ./...`, `go vet ./...`, `gofmt` clean before commit.

## Technical Considerations

- **`go-git` dependency**: `github.com/go-git/go-git/v5` is the chosen upstream-fetch library (per the PRD). It carries ~30 transitive deps and is heavier than shelling out to `git`, but it preserves the single-static-binary property and avoids `git` being required on CI runners. Use `Remote.List` for ls-remote, an empty in-memory repo + `Fetch` for shallow-by-SHA. Document the choice in an ADR if one doesn't yet exist; do not relitigate.
- **YAML loader for `.skills-oci.yaml`**: prefer `gopkg.in/yaml.v3` (standard Go YAML library, well-maintained). Unknown-key handling needs `yaml.Node`-level parsing or an explicit decoder option since `yaml.v3`'s default tolerates unknown keys silently — Unit 3 requires they be logged to stderr.
- **OCI push annotations**: the existing `pkg/oci.PushOptions.Annotations` map (already used for `org.opencontainers.image.licenses` from SKILL.md frontmatter) is reused. No new fields on `PushOptions`. The orchestrator constructs the annotation map and passes it through; nothing in `pkg/oci/push.go` changes.
- **Telemetry pipeline**: the existing `pkg/telemetry/` is reused additively. A new `catalog.synced` `event_type` is registered; the envelope shape, opt-out env var (`SKILLS_OCI_TELEMETRY=off`), 2s timeout, and `pending.ndjson` buffer behaviors all apply unchanged. Per-entry events emit concurrently (the chosen design — see `02-questions-1-...` answer #1).
- **Concurrency model for `catalog sync`**: `golang.org/x/sync/errgroup` with `SetLimit(N)` is the canonical pattern. Default N=4 is conservative for GitHub clone bandwidth and OCI registry push concurrency; CI consumers tune via `--concurrency` or `.skills-oci.yaml`. A failure in one entry does not abort the errgroup (use `errgroup` only for the limit, not for fail-fast — workers swallow per-entry errors into the `Result`).
- **Temp directory hygiene**: each entry's clone lives under `os.TempDir()/skills-oci-catalog-<random>/`; `defer os.RemoveAll` cleans on completion. A top-of-run sweep removes leftover `skills-oci-catalog-*` directories older than 24h to handle prior crashes.
- **Lockfile atomicity**: `WriteLockAtomic` writes to `catalog-lock.json.tmp.<pid>` in the same directory and renames; on rename failure the temp file is removed. Exit code `2` is reserved for "lockfile write failed after registry changed" — the only condition where registry and lockfile can diverge.
- **Stable JSON output**: `encoding/json` does not guarantee key order on a `map[string]any`, but it does for structs. All catalog/lock types are typed structs with explicit `json:"..."` tags — stable order is a property of the type definitions, not separate sort code.
- **Test infrastructure**:
  - `pkg/catalog/` and `pkg/config/` tests use `t.TempDir()` and table-driven inputs; no external dependencies.
  - `pkg/scm/` tests use both strategies: `file://`-served temp repos (initialized with `go-git` itself, fastest, faithful to ref resolution) for happy-path correctness; `httptest.Server` serving the smart-HTTP protocol for HTTP-edge cases (auth headers, 404, timeout, redirects).
  - `cmd/catalog_*_test.go` reuses the in-process registry from `cmd/testregistry_test.go` plus a `file://` upstream fixture. End-to-end without real GitHub or real GHCR.
- **No new direct dependencies beyond**: `github.com/go-git/go-git/v5` (upstream fetch), `gopkg.in/yaml.v3` (config), `golang.org/x/sync/errgroup` (already in transitive set, but pin explicitly if not). Everything else is stdlib (`encoding/json`, `os`, `path/filepath`, `context`, `net/http`).

## Security Considerations

- **SHA-only refs are load-bearing**: branches, mutable tags, and `HEAD` are rejected by `pkg/catalog/Validate`. The audit story (`org.opencontainers.image.source` annotation pointing at upstream) only works if the ref is immutable. Tests must cover every rejection path — relaxing the validator silently is the single highest-impact regression risk.
- **Trust checkpoint at PR review**: the CLI does not implement its own access control. Trust flows through the catalog repository's branch protection + CODEOWNERS; whoever can merge to `main` is the trust root. The data contract document must say this in plain language so adopters configure their repos correctly.
- **No long-lived credentials**: the canonical GHA workflow uses OIDC (`id-token: write` + GHCR OIDC trust) for registry auth. No `GITHUB_TOKEN`-as-registry-password pattern, no PAT in GitHub Secrets. Bot identity for committing the lockfile back uses `GITHUB_TOKEN` scoped to `contents: write` only.
- **License surfacing, not enforcement**: `org.opencontainers.image.licenses` is set from upstream SKILL.md frontmatter. Failing on missing-license is a correctness guard, not a policy enforcement; per-license allow/deny is downstream tooling.
- **Threat model gap, acknowledged**: SHA-pinning anchors *what* was pulled to an immutable Git object. It does not anchor *who* the upstream is — a compromised upstream GitHub account can force-push a malicious commit under a new tag, and Renovate will dutifully open a PR. The human PR reviewer is the only check. Sigstore/cosign attestations would close this gap and are explicitly out of scope (Non-Goal 8).
- **Proof artifact hygiene**: test fixtures use synthetic upstream repos with fabricated SKILL.md content; no real customer or registry data lands in `testdata/`. The `--plain` output examples in this spec use sample values that never reference real proprietary repos.
- **Temp dir contents**: cloned upstream repos contain code, not secrets. They live under `os.TempDir()` with default perms; `defer os.RemoveAll` cleans on exit. No special handling needed.
- **Config file contents**: `.skills-oci.yaml` contains no secrets — `default_namespace` is public registry info, `allow_missing_license` is a bool, `concurrency` is an int. Safe to commit.

## Success Metrics

1. **Vendoring time**: a platform engineer can take a fresh upstream skill URL through `catalog add` → PR → merge → CI sync → consumable in registry in under 10 minutes wall-clock on the happy path. Target: P95 ≤ 15 minutes including human review time, measured against the first 5 real vendoring operations after launch.
2. **Sync throughput**: `catalog sync` reconciles a 20-entry catalog (all `skipped`, the steady-state case) in under 10 seconds, and a 20-entry catalog with 4 fresh pushes (`--concurrency=4`) in under 60 seconds end-to-end on a standard GitHub Actions ubuntu-latest runner.
3. **Audit completeness**: 100% of artifacts pushed by `catalog sync` carry both `org.opencontainers.image.source` (pointing at the immutable upstream SHA) and `org.opencontainers.image.licenses` (or are explicitly opted-out via `--allow-missing-license`). Verified by a post-launch audit pass over the first 50 vendored artifacts.
4. **Renovate integration**: the published Renovate snippet from `docs/skills-catalog-data-contract.md` works against a real Renovate Bot run on a real catalog repo, atomically bumping `version` and `commit` together via `pinDigests`. Target: zero manual SHA edits in the first 10 Renovate PRs after launch.
5. **Telemetry conformance**: 100% of `catalog.synced` events emitted in CI pass the same envelope-shape validation as existing `skill.downloaded` events. Zero `4xx` rejections attributable to producer bugs in the first 30 days post-launch.

## Open Questions

1. **Setup composite action — same release or prerequisite?** The CI workflow snippet in `docs/skills-catalog-data-contract.md` references `liatrio/skills-oci/.github/actions/setup@v1`, a composite action that downloads a pinned release binary onto the runner. The PRD says the action "must be authored as part of the same release" but does not say "as part of this spec." Confirm whether the setup action is in-scope for this spec's deliverables (one extra small unit) or shipped as a parallel workstream coordinated by the release.
