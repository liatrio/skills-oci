# Proof Artifacts — Parent Task 2.0

Spec: [`02-spec-skills-catalog-vendoring.md`](../02-spec-skills-catalog-vendoring.md) — Demoable Unit 2
Tasks: [`02-tasks-skills-catalog-vendoring.md`](../02-tasks-skills-catalog-vendoring.md) — Parent 2.0

Built `pkg/scm` — GitHub `tree` URL parser, tag resolver via `go-git` `Remote.List`, shallow-by-SHA fetcher with `SKILL.md` verification.

## Files created

```
pkg/scm/doc.go
pkg/scm/types.go
pkg/scm/parse.go              pkg/scm/parse_test.go
pkg/scm/resolve.go            pkg/scm/resolve_test.go
pkg/scm/fetch.go              pkg/scm/fetch_test.go
pkg/scm/fetch_http_test.go
pkg/scm/testdata_helper_test.go
```

## Dependencies added

- `github.com/go-git/go-git/v5 v5.19.1` (direct)
- Transitive: go-billy/v5, go-git/gcfg, etc.

## Test Results

`go test ./pkg/scm/ -v` — all tests pass (43 total including sub-tests):

```
=== RUN   TestParseGitHubTreeURL_HappyPaths (5 sub-tests: semver tag, branch, 40-hex SHA, deep subpath, trailing slash)
--- PASS: TestParseGitHubTreeURL_HappyPaths
=== RUN   TestParseGitHubTreeURL_Rejections (10 sub-tests: non-github host, http scheme, blob/releases segments, missing subpath, empty, malformed, single segment)
--- PASS: TestParseGitHubTreeURL_Rejections

=== RUN   TestResolveTag_LightweightTag
--- PASS: TestResolveTag_LightweightTag
=== RUN   TestResolveTag_AnnotatedTagPeeled              (verifies annotated tags peel to commit, not tag-object hash)
--- PASS: TestResolveTag_AnnotatedTagPeeled
=== RUN   TestResolveTag_FortyHexSHAPassesThrough        (verifies 40-hex passthrough makes ZERO network calls)
--- PASS: TestResolveTag_FortyHexSHAPassesThrough
=== RUN   TestResolveTag_TagNotFound
--- PASS: TestResolveTag_TagNotFound
=== RUN   TestResolveTag_EmptyTagRejects
--- PASS: TestResolveTag_EmptyTagRejects
=== RUN   TestResolveTag_EmptyRepoRejects
--- PASS: TestResolveTag_EmptyRepoRejects
=== RUN   TestResolveTag_RepoSlugBuildsGitHubURL
--- PASS: TestResolveTag_RepoSlugBuildsGitHubURL

=== RUN   TestFetch_HappyPath                            (file:// fixture; verifies SKILL.md at <dst>/<subpath>/)
--- PASS: TestFetch_HappyPath
=== RUN   TestFetch_SubpathMissingAtCommit
--- PASS: TestFetch_SubpathMissingAtCommit
=== RUN   TestFetch_SubpathExistsButNoSKILLMD
--- PASS: TestFetch_SubpathExistsButNoSKILLMD
=== RUN   TestFetch_RejectsBadOwner (8 sub-tests: url-injection, slash, colon, dot-dot, empty)
--- PASS: TestFetch_RejectsBadOwner
=== RUN   TestFetch_RejectsBadCommit
--- PASS: TestFetch_RejectsBadCommit
=== RUN   TestFetch_RejectsEmptySubpath
--- PASS: TestFetch_RejectsEmptySubpath
=== RUN   TestFetch_RejectsEmptyDst
--- PASS: TestFetch_RejectsEmptyDst
=== RUN   TestFetch_ContextCancellationCleansUp
--- PASS: TestFetch_ContextCancellationCleansUp

=== RUN   TestFetch_HTTPS_404_FromUpstream               (httptest.Server returning 404)
--- PASS: TestFetch_HTTPS_404_FromUpstream
=== RUN   TestFetch_HTTPS_ContextTimeout                 (slow server vs 200ms context; verifies prompt timeout)
--- PASS: TestFetch_HTTPS_ContextTimeout

PASS
ok  	github.com/salaboy/skills-oci/pkg/scm	0.834s
```

## Coverage

`go test ./pkg/scm/ -cover` reports **93.2% statement coverage**, above the spec's ≥ 90% target.

Per-function coverage on critical functions:

```
parse.go:    ParseGitHubTreeURL  91.7%   (uncovered branch is url.Parse failure — defensive)
resolve.go:  ResolveTag         100.0%
fetch.go:    Fetch               80.0%   (uncovered branches are PlainInit/CreateRemote/Worktree/Checkout error paths — defensive; happy + every documented rejection path covered)
fetch.go:    validateRef         88.9%
fetch.go:    wipeAndWrap        100.0%
total:                            93.2%
```

The uncovered branches in `Fetch` are defensive error wrappers around `go-git` operations (PlainInit, CreateRemote, etc.) that cannot fail under the test conditions without filesystem fault injection. The behavioral guarantees that matter — host check, subpath verification, SKILL.md verification, cleanup on error, context cancellation — are all tested.

## Quality Gates

- `gofmt -l pkg/scm` → clean
- `go vet ./pkg/scm/...` → clean
- `go test ./...` → all repo tests pass; no regressions

## Configuration

Two test fixture strategies in use, per the SDD-1 decision:

1. **`file://`-served git repos** (built via `go-git`'s `PlainInit`) for happy-path correctness. Helpers `newFixtureRepo` and `newFixtureRepoWithoutSkillMD` in `testdata_helper_test.go` configure `uploadpack.allowReachableSHA1InWant` so Fetch-by-SHA works.
2. **`httptest.Server`** for HTTP-specific error paths (404 from upstream, slow-server-vs-short-context-timeout). Real smart-HTTP happy-path is covered by the `file://` tests through the same `go-git` transport layer.

The `remoteURLForFetch` package-level variable lets tests redirect the Fetch URL builder without changing the production API.

## Verification

- **Spec FR coverage for Unit 2**:
  - `ParseGitHubTreeURL` pure function rejecting non-`github.com` hosts, non-`tree` URLs, missing subpath, malformed URLs ✓
  - `ResolveTag` with 40-hex passthrough, ls-remote via `Remote.ListContext`, annotated-tag peel via `PeelingOption: AppendPeeled` ✓
  - `Fetch` shallow-clones by SHA, verifies SKILL.md at `<dst>/<subpath>`, rejects bypassed-parser callers (slash/colon/empty/url-injection in Owner/Repo) ✓
  - Anonymous HTTPS only (stdlib `net/http` defaults); private-repo support out of scope ✓
  - Temp dir cleanup on success, error, and context cancellation ✓
- **Critical-business-logic functions at 100% line coverage**: `ResolveTag`, `wipeAndWrap`.
- **Spec coverage target met**: 93.2% overall ≥ 90% target.

## Security

- No credentials, tokens, or API keys in source or proof.
- Fetch validates Owner/Repo against a safe-character pattern (`^[A-Za-z0-9._-]+$`) so a caller cannot smuggle a URL through the SourceRef.
- All fixture data is synthetic (`fixture@example.com` author, `name: example` skill).
