# How skills-oci vendors third-party skills, and what the on-disk contract looks like

**Source of truth**: two files in the consumer's catalog repository — `catalog.json` (declared inputs) and `catalog-lock.json` (concrete push state). The CLI's `catalog add` and `catalog sync` subcommands are the only supported way to vendor an upstream skill into the internal registry; there is no manual `push --from-git` path.

**Who writes**: humans and Renovate Bot write `catalog.json`. CI writes `catalog-lock.json`. The split is intentional — see [Writer/reader matrix](#writer-reader-matrix).

**Who reads**: the `skills-oci catalog sync` command (every entry), Renovate Bot (`catalog.json` only, via the snippet in [Renovate integration](#renovate-integration)), security reviewers and auditors (both files, as the audit trail).

**What's in scope today**: GitHub-hosted upstream skills, anonymous-only fetch, SHA-1 commit refs only. Other SCM hosts, private-repo auth, and SHA-256 git refs are out of scope for v1 but the schema is forward-compatible. See [Out of scope](#out-of-scope).

## SHA-only refs: the load-bearing security property

`catalog.json` entries carry both a human-readable `version` (the upstream tag) and an immutable `commit` (the 40-hex Git SHA). The CLI validates that `commit` matches `^[a-f0-9]{40}$` at load time. Branches, mutable tags (`latest`, `main`, `master`, `HEAD`), and empty strings are rejected — accepting any of them defeats the audit story, because the `org.opencontainers.image.source` annotation the registry artifact carries would point at something that could be moved out from under us.

The human review at PR time is the trust checkpoint: the reviewer clicks through the SHA on GitHub, reads the upstream content at exactly that commit, and merges. CI then has no discretion — it fetches the SHA the human approved.

## `catalog.json` — declared inputs

```json
{
  "schemaVersion": 1,
  "skills": [
    {
      "name": "create-skill",
      "repo": "anthropics/skills",
      "subpath": "skills/create-skill",
      "version": "v1.0.0",
      "commit": "bc6708cbbc37adb919157f04d31e601e68f4b9c2",
      "internal_ref": "ghcr.io/liatrio/skills/create-skill"
    }
  ]
}
```

### Field rules

All fields are required.

- **`schemaVersion`** (integer) — currently `1`. Additive changes (e.g. new optional fields on entries) do not bump this; breaking changes do. Future readers MUST reject `schemaVersion` they do not recognize.
- **`name`** (string) — local catalog identifier. Need not match the upstream skill name. Two entries with the same `name` are rejected at validation time. Used as the lookup key for `--only`, telemetry, and lockfile correlation.
- **`repo`** (string) — bare `<owner>/<repo>` slug. **Renovate's `github-tags` datasource consumes this field.** Must not contain `https://`, `/tree/`, `/blob/`, or any path component beyond the owner and repo. Pattern: `^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`.
- **`subpath`** (string) — path within the upstream repo to the skill directory. Forward slashes only, no leading slash. The fetcher requires `<subpath>/SKILL.md` to exist at the resolved commit.
- **`version`** (string) — human-readable upstream tag or semver. **Renovate manages this field.** Used as the tag in `<internal_ref>:<version>` at push time. Rejected if it equals `latest`, `main`, `master`, `HEAD`, or the empty string — those are mutable in practice and defeat the audit guarantee.
- **`commit`** (string) — full 40-character lowercase hexadecimal Git SHA-1 commit. **Renovate manages this field via the `pinDigests` pattern, alongside `version`.** Rejected if not exactly 40 lowercase hex chars. SHA-256 git refs (64 hex) are out of scope for v1 but accepting them is an additive validator change when SHA-256 git becomes broadly supported.
- **`internal_ref`** (string) — destination registry/repository, no tag. The tag at push time is derived from `version` — so a successful sync of the example above pushes to `ghcr.io/liatrio/skills/create-skill:v1.0.0`.

## `catalog-lock.json` — concrete push state

```json
{
  "lockfileVersion": 1,
  "generatedAt": "2026-05-22T18:30:00Z",
  "skills": [
    {
      "name": "create-skill",
      "internal_ref": "ghcr.io/liatrio/skills/create-skill",
      "tag": "v1.0.0",
      "commit": "bc6708cbbc37adb919157f04d31e601e68f4b9c2",
      "digest": "sha256:1234567890abcdef...",
      "ref": "ghcr.io/liatrio/skills/create-skill:v1.0.0@sha256:1234...",
      "syncedAt": "2026-05-22T18:30:14Z"
    }
  ]
}
```

### Field rules

- **`lockfileVersion`** (integer) — currently `1`.
- **`generatedAt`** (string) — RFC 3339 UTC timestamp, second precision, `Z` suffix — when the sync run started.
- **`skills[].commit`** is duplicated from `catalog.json` into the lock file so each lock entry is self-contained: the lock records what commit was synced, not just what registry digest came out.
- **`skills[].digest`** is the OCI manifest digest returned by the registry on push.
- **`skills[].ref`** is the fully-qualified pinned reference (`<internal_ref>:<tag>@<digest>`), included for consumers that want a single string to copy.
- **`skills[].syncedAt`** is per-entry; `generatedAt` is per-file.

Failed and skipped entries do NOT overwrite their prior lock entries — the lock preserves the last-known-good state for entries that did not push successfully in the current run, so a transient registry outage does not regress the lockfile.

## Writer/reader matrix

| File | Written by | Read by |
| --- | --- | --- |
| `catalog.json` | humans (via `catalog add` or hand-edit), Renovate Bot (via `pinDigests`) | `catalog sync`, Renovate Bot, PR reviewers |
| `catalog-lock.json` | `catalog sync` (CI) | security reviewers, drift detectors, future `catalog verify` |

Both files MUST be committed to version control. The lockfile committed back to `main` by the CI bot is the source of truth for "what is in the registry now."

## Exit codes for `catalog sync`

The CLI distinguishes three terminal states so CI consumers can react appropriately:

- `0` — every entry was either pushed (`synced`) or skipped (`skipped`); the lockfile was written successfully.
- `1` — at least one entry failed (`failed`). The lockfile was still written, and failed entries' prior lock state was preserved (no regression).
- `2` — the lockfile write itself failed *after* one or more registry pushes succeeded. This is the only state where the registry can be ahead of the lockfile; manual reconciliation is required (re-run `catalog sync`, or hand-edit the lockfile from registry state).

CI workflows MAY distinguish exit `1` from exit `2` to alert humans on the latter and auto-retry the former.

## Renovate integration

The `repo`, `version`, and `commit` fields are shaped so Renovate's stock `github-tags` datasource plus the `pinDigests` pattern bump them atomically — no custom Renovate plugin required.

Drop the following into `renovate.json` (or your shared config) in the same repo as `catalog.json`:

```json
{
  "customManagers": [
    {
      "customType": "regex",
      "fileMatch": ["^catalog\\.json$"],
      "matchStrings": [
        "\"repo\":\\s*\"(?<depName>[^\"]+)\"[^}]*?\"version\":\\s*\"(?<currentValue>[^\"]+)\"[^}]*?\"commit\":\\s*\"(?<currentDigest>[a-f0-9]{40})\""
      ],
      "datasourceTemplate": "github-tags",
      "currentDigestTemplate": "{{currentDigest}}"
    }
  ],
  "packageRules": [
    {
      "matchManagers": ["custom.regex"],
      "matchFileNames": ["catalog.json"],
      "pinDigests": true
    }
  ]
}
```

`pinDigests` is the load-bearing trick: without it, Renovate would update only `version` (the tag) and leave `commit` stale. With it, Renovate resolves the new tag's commit SHA at PR-creation time and updates both fields atomically.

## CI workflow

The canonical adoption pattern for GitHub Actions + GHCR + OIDC:

```yaml
# .github/workflows/catalog.yml
name: catalog

on:
  pull_request:
    paths: [catalog.json]
  push:
    branches: [main]
    paths: [catalog.json]

jobs:
  validate:
    if: github.event_name == 'pull_request'
    runs-on: ubuntu-latest
    permissions:
      contents: read
    steps:
      - uses: actions/checkout@v4
      - uses: liatrio/skills-oci/.github/actions/setup@v1
        with:
          version: v1.4.0
      - run: skills-oci catalog sync --dry-run --plain

  sync:
    if: github.event_name == 'push' && github.ref == 'refs/heads/main'
    runs-on: ubuntu-latest
    permissions:
      contents: write    # to commit catalog-lock.json
      packages: write    # to push to ghcr.io
      id-token: write    # OIDC for registry auth
    steps:
      - uses: actions/checkout@v4
      - uses: liatrio/skills-oci/.github/actions/setup@v1
        with:
          version: v1.4.0
      - run: skills-oci catalog sync --plain
      - name: Commit lockfile
        run: |
          if ! git diff --quiet catalog-lock.json; then
            git config user.name  "skills-oci-bot"
            git config user.email "skills-oci-bot@users.noreply.github.com"
            git add catalog-lock.json
            git commit -m "chore(catalog): update lockfile"
            git push
          fi
```

Things to know:

- **The `paths` filter is `catalog.json` only, not the lockfile.** The bot's lockfile commit on `main` does not retrigger CI, which avoids an infinite loop.
- **OIDC for GHCR auth.** No long-lived personal access token; the `id-token: write` permission lets the runner mint a short-lived token GHCR trusts. Configure the trust relationship in your registry's settings.
- **Bot identity for lockfile commits.** The runner's `GITHUB_TOKEN` (scoped to `contents: write`) commits as `skills-oci-bot`. Adjust the name/email to match your org's convention.
- **The `setup` composite action** referenced above downloads a pinned release binary onto the runner. It is published from this repository; substitute an equivalent install step if you mirror skills-oci internally.

## Trust and governance

The CLI does not implement its own access control or vetting machinery. Trust flows through the catalog repository's branch protection and CODEOWNERS:

- **Access control** — whoever can merge to `main` of the catalog repo is the trust root. Configure CODEOWNERS so a platform-team group owns `catalog.json` and `catalog-lock.json`; require one review from that group; protect `main` against direct pushes.
- **Vetting model** — per-skill at PR review time. v1 does not implement a pre-approved vendor allow-list. Renovate-generated PRs are reviewed identically to author-generated PRs.
- **License compliance** — pushed artifacts carry `org.opencontainers.image.licenses` derived from upstream `SKILL.md` frontmatter. License *compatibility* evaluation is the job of downstream compliance tooling (FOSSA, ScanCode, manual review). The CLI fails a sync when the upstream `SKILL.md` declares no license at all unless `--allow-missing-license` is set; deciding whether a *declared* license is *allowed* is downstream.

### Threat model gap, acknowledged

SHA-pinning anchors *what* was pulled to an immutable Git object. It does not anchor *who* the upstream is — a compromised upstream GitHub account can force-push a malicious commit under a new tag, and Renovate will dutifully open a PR. The human PR reviewer is the only check. Sigstore/cosign attestations would close this gap and are out of scope for v1.

## Out of scope

- **SCM hosts other than `github.com`** — `pkg/scm` is host-agnostic at the boundary but v1 validation rejects non-GitHub hosts.
- **Authentication for private upstream repos** — v1 fetches anonymously.
- **SHA-256 git refs** — schema accepts 40-hex only in v1; forward-compatible for 64-hex.
- **`catalog remove`, `catalog init`, `catalog verify`** — remove is a hand-edit, init is `echo '{"schemaVersion":1,"skills":[]}'`, verify is a separate periodic check.
- **Registry-side artifact deletion** — `catalog sync` only pushes; un-vendoring includes a manual registry-side step by design.
- **Sigstore / cosign attestations** — separate compliance initiative.
- **A pre-approved vendor allow-list** — vetting is per-skill at PR review time.
- **`push --from-git` shortcut** — bypasses the catalog and therefore the audit story.
