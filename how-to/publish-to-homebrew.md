# How Homebrew Publishing Works

`skills-oci` ships as a Homebrew formula in [`liatrio/homebrew-taproom`](https://github.com/liatrio/homebrew-taproom). Every release publishes (or updates) the formula automatically — there is nothing to do by hand.

End-users install with:

```bash
brew install liatrio/taproom/skills-oci
```

(Or `brew tap liatrio/taproom` once, then `brew install skills-oci`.)

## How a release flows to the tap

```
conventional commit on main
        │
        ▼
  semantic-release        ── creates tag + GitHub Release (notes)
        │
        ▼
   GoReleaser             ── cross-compiles, builds archives,
        │                    appends them to the release,
        │                    commits Casks/skills-oci.rb
        │                    to liatrio/homebrew-taproom
        ▼
brew install / brew upgrade picks up the new version
```

GoReleaser is configured in `.goreleaser.yaml`. The `homebrew_casks:` block points at the tap repo, names the cask file (`Casks/skills-oci.rb`), and sets the cask metadata (homepage, description, binary name). A `postflight` hook removes the macOS Gatekeeper quarantine attribute so the unsigned binary launches on first use. The workflow that runs it is `.github/workflows/_goreleaser.yml`.

> **Note:** GoReleaser v2 generates a Homebrew **cask** (not a formula). Casks ship the prebuilt binary as-is, which is the right fit for a Go CLI. They work on both macOS and Linux Homebrew; the generated cask has separate `on_macos`/`on_linux` URL stanzas.

## One-time setup (already done)

These items only need to be (re-)done if the secret rotates or the tap is moved:

1. **Tap repo**: `liatrio/homebrew-taproom` must exist and contain a `Formula/` directory. The Homebrew `homebrew-` prefix is what lets users tap it as `liatrio/taproom`.
2. **Tap token**: a fine-grained PAT (or GitHub App token) scoped to `liatrio/homebrew-taproom` with `contents: read & write`, stored as the repo secret `HOMEBREW_TAP_TOKEN` on `liatrio/skills-oci`. Prefer a machine user (e.g. `liatrio-bot`) over a personal PAT so the integration survives team changes.

If the token is missing or expired, `_goreleaser.yml` will fail the `brews` step but the rest of the release (archives + checksums on the GitHub Release) will still succeed.

## Local verification

To dry-run the full GoReleaser flow without touching GitHub or the tap:

```bash
go install github.com/goreleaser/goreleaser/v2@latest

goreleaser check                                       # config sanity
goreleaser release --snapshot --clean --skip=publish   # builds everything into ./dist
ls dist/                                               # archives + checksums.txt
cat dist/homebrew/Casks/skills-oci.rb                # the formula GoReleaser would push
```

`--snapshot` skips git-tag validation; `--skip=publish` skips GitHub and tap pushes.

## Promoting to Homebrew Core (someday)

`homebrew-core` has stricter [acceptance criteria](https://docs.brew.sh/Acceptable-Formulae) (notability, age, stars). The custom tap is the right home until those bars are met.
