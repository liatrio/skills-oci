# How to Create a New Release

`skills-oci` releases are fully automated. You do not tag the repo by hand — [semantic-release](https://semantic-release.gitbook.io/) cuts the next version from your conventional commits when they land on `main`. GoReleaser then builds artifacts, attaches them to the GitHub Release, and updates the Homebrew formula in [`liatrio/homebrew-taproom`](https://github.com/liatrio/homebrew-taproom).

## The release flow

```
PR merged to main (conventional commit)
        │
        ▼
  _test.yml            (always)
        │
        ▼
  _semantic-release.yml ── tags + creates GitHub Release with notes
        │
   ┌────┴─────────┐
   ▼              ▼
_build-push.yml   _goreleaser.yml
  │                │
  │                ├── archives + checksums attached to the release
  │                └── Homebrew formula committed to liatrio/homebrew-taproom
  ▼
GHCR image at ghcr.io/liatrio/skills-oci:<version>
```

All of this is gated on `_test.yml` going green.

## Cutting a release

There is no command to run. To release, merge a PR whose commit message follows [Conventional Commits](https://www.conventionalcommits.org/):

| Commit type | Bumps |
|-------------|-------|
| `fix:` | patch (e.g. `v1.0.0` → `v1.0.1`) |
| `feat:` | minor (e.g. `v1.0.1` → `v1.1.0`) |
| `feat!:` or `BREAKING CHANGE:` footer | major (e.g. `v1.1.0` → `v2.0.0`) |
| `chore:`, `docs:`, `refactor:`, `test:`, `ci:` | no release |

PR titles are validated by `.github/workflows/pr-title.yml`, so a merged PR title that doesn't bump nothing will not produce a release.

If no release-bumping commits have landed since the last tag, `_semantic-release.yml` is a no-op and the `goreleaser` / `build-push` jobs are skipped.

## Verifying a release

After merge, watch the `goreleaser` job in [the Actions tab](https://github.com/liatrio/skills-oci/actions). When it completes:

1. The [Releases page](https://github.com/liatrio/skills-oci/releases) shows the new tag with:
   - Auto-generated release notes (from semantic-release)
   - `skills-oci_<version>_{linux,darwin}_{amd64,arm64}.tar.gz`
   - `skills-oci_<version>_windows_amd64.zip`
   - `checksums.txt`
2. [`liatrio/homebrew-taproom`](https://github.com/liatrio/homebrew-taproom) has a new commit on `main` updating `Casks/skills-oci.rb` to the new version + SHA256s.
3. The image is published to `ghcr.io/liatrio/skills-oci:<version>`.

Smoke-test the brew formula on a Mac:

```bash
brew update
brew upgrade skills-oci    # or `brew install liatrio/taproom/skills-oci` on first install
skills-oci --version       # should print the new version, not "dev"
```

## Manual / backport releases

Sometimes you need to release off a non-`main` branch (e.g. patching an old major). Push a `v*` tag and the same workflows run, skipping semantic-release:

```bash
git checkout -b release/v1.0.x v1.0.3
# apply the fix, commit
git tag v1.0.4
git push origin release/v1.0.x v1.0.4
```

The `goreleaser` workflow falls back to `github.ref_name` for the version when called from a tag push, so binaries and the formula update normally.

## Troubleshooting

**Release didn't happen.** Check whether the merged commits actually bump anything (see the table above). `chore:` and `docs:` commits intentionally do not release.

**`goreleaser` job failed at the `brews` step.** The `HOMEBREW_TAP_TOKEN` secret on `liatrio/skills-oci` is missing, expired, or no longer authorized on `liatrio/homebrew-taproom`. The rest of the release still succeeded — re-running the job after refreshing the secret will only update the tap. See [`publish-to-homebrew.md`](./publish-to-homebrew.md).

**Tests fail.** Fix on `main` and merge another commit; semantic-release will run again on the next push.
