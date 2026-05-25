# Proof Artifacts — Parent Task 3.0

Spec: [`02-spec-skills-catalog-vendoring.md`](../02-spec-skills-catalog-vendoring.md) — Demoable Unit 3
Tasks: [`02-tasks-skills-catalog-vendoring.md`](../02-tasks-skills-catalog-vendoring.md) — Parent 3.0

Built `pkg/config` (YAML loader for `.skills-oci.yaml`), `cmd/catalog.go` (parent command + config bootstrap), and `cmd/catalog_add.go` (the 9-step `catalog add` flow).

## Files created

```
pkg/config/doc.go
pkg/config/types.go
pkg/config/load.go              pkg/config/load_test.go
cmd/catalog.go
cmd/catalog_add.go              cmd/catalog_add_test.go
```

Modified:
```
cmd/root.go    (added newCatalogCmd() to AddCommand chain)
go.mod         (gopkg.in/yaml.v3 was already present as transitive — now direct via pkg/config)
```

## Test Results

### pkg/config (7 tests, all pass)

```
=== RUN   TestLoad_ValidYAML
--- PASS: TestLoad_ValidYAML
=== RUN   TestLoad_EmptyInputReturnsZeroValue
--- PASS: TestLoad_EmptyInputReturnsZeroValue
=== RUN   TestLoad_PartialKeysOK
--- PASS: TestLoad_PartialKeysOK
=== RUN   TestLoad_UnknownTopLevelKeyWarnsButSucceeds
--- PASS: TestLoad_UnknownTopLevelKeyWarnsButSucceeds
=== RUN   TestLoad_TypeMismatchRejects
--- PASS: TestLoad_TypeMismatchRejects
=== RUN   TestLoad_RejectsNegativeConcurrency (2 sub-tests: zero, negative)
--- PASS: TestLoad_RejectsNegativeConcurrency
=== RUN   TestLoad_MalformedYAMLRejects
--- PASS: TestLoad_MalformedYAMLRejects
PASS
ok  	github.com/salaboy/skills-oci/pkg/config	0.333s	coverage: 88.6%
```

### cmd/catalog_add (12 new tests + 5 pre-existing, all pass)

```
=== RUN   TestRunCatalogAddWithDeps_HappyPathURL
--- PASS
=== RUN   TestRunCatalogAddWithDeps_HappyPathFlags
--- PASS
=== RUN   TestParseAddOpts_URLPlusFlagsRejects                       (ambiguous-input guard)
--- PASS
=== RUN   TestParseAddOpts_MissingInputsRejects                      (no URL, no flags)
--- PASS
=== RUN   TestRunCatalogAddWithDeps_RejectsMissingNamespace          (all 4 precedence levels exhausted)
--- PASS
=== RUN   TestRunCatalogAddWithDeps_SubpathWithoutSKILLMD            (catalog.json untouched on fail)
--- PASS
=== RUN   TestRunCatalogAddWithDeps_TagNotFound                      (resolver error propagates)
--- PASS
=== RUN   TestRunCatalogAddWithDeps_DuplicateNameRejected            (Validate's duplicate check; original entry preserved)
--- PASS
=== RUN   TestRunCatalogAddWithDeps_DryRunDoesNotWrite               (file absent after --dry-run; output includes "would add entry")
--- PASS
=== RUN   TestRunCatalogAddWithDeps_OutputMatchesSpecFormat          (all 8 spec'd output lines)
--- PASS
=== RUN   TestResolveInternalRef_PrecedenceChain                     (flag > config > env > error)
--- PASS
=== RUN   TestResolveInternalRef_StripsTrailingSlashOnNamespace
--- PASS
=== RUN   TestResolveUpstreamInputs_FlagFormValidation               (5 sub-tests: missing subpath/version, malformed repo, empty segments)
--- PASS
=== RUN   TestResolveUpstreamInputs_TrimsSubpathSlashes
--- PASS
=== RUN   TestRunCatalogAddWithDeps_MalformedURLRejected             (non-github host)
--- PASS
=== RUN   TestRunCatalogAddWithDeps_FetchFailure
--- PASS
PASS
ok  	github.com/salaboy/skills-oci/cmd
```

## Coverage

`pkg/config/load.go`: **88.6%** statement coverage. Uncovered lines are inside the `warnUnknown` `sort.Strings` path on an empty slice (defensive).

`cmd/catalog_add.go` orchestration functions:

```
newCatalogAddCmd            100.0%
parseAddOpts                 94.1%
runCatalogAddWithDeps        91.5%
resolveUpstreamInputs       100.0%
resolveInternalRef          100.0%
loadCatalogFile              83.3%   (uncovered: arbitrary read errors other than IsNotExist)
GetDefaultNamespace         100.0%
```

The Cobra-glue functions (`runCatalogAdd`, `newCatalogCmd`, `loadProjectConfig`, `configFromContextAccessor`, `realFetcher.Fetch`, `realResolver.ResolveTag`) show 0% — they are exercised by the real-world CLI proof below, not by unit tests.

The cmd-package "total coverage" of 13.5% reflects the pre-existing untested commands (push, install, etc.) and is not a regression introduced by this change.

## Configuration

`.skills-oci.yaml` schema (project-level):

```yaml
catalog:
  default_namespace: ghcr.io/liatrio/skills
  allow_missing_license: false
  concurrency: 4
```

Precedence for `default_namespace` (verified in `TestResolveInternalRef_PrecedenceChain`):

1. `--namespace` flag
2. `.skills-oci.yaml` `catalog.default_namespace`
3. `SKILLS_OCI_DEFAULT_NAMESPACE` env var
4. Error: `"no default namespace configured; pass --namespace, set catalog.default_namespace in .skills-oci.yaml, or export SKILLS_OCI_DEFAULT_NAMESPACE"`

Plus `--internal-ref` short-circuits all of the above when explicitly set.

## CLI Output (real-world capture)

Captured from a real `catalog add` invocation against the public `anthropics/skills` repo at commit `690f15cac7f7b4c055c5ab109c79ed9259934081`:

`docs/specs/02-spec-skills-catalog-vendoring/02-proofs/catalog-add-plain.txt`:

```
resolving anthropics/skills@690f15cac7f7b4c055c5ab109c79ed9259934081
  → commit 690f15cac7f7b4c055c5ab109c79ed9259934081
fetching subpath skills/algorithmic-art
verifying SKILL.md
  upstream name: algorithmic-art
  upstream license: Complete terms in LICENSE.txt
catalog add: appended entry "algorithmic-art" to catalog.json
```

(Note: `upstream version:` is absent because Anthropic's `skills/algorithmic-art/SKILL.md` has no `version` field in its frontmatter. The CLI omits the line gracefully.)

Resulting `catalog.json` (`docs/specs/02-spec-skills-catalog-vendoring/02-proofs/catalog-add-result.json`):

```json
{
  "schemaVersion": 1,
  "skills": [
    {
      "name": "algorithmic-art",
      "repo": "anthropics/skills",
      "subpath": "skills/algorithmic-art",
      "version": "690f15cac7f7b4c055c5ab109c79ed9259934081",
      "commit": "690f15cac7f7b4c055c5ab109c79ed9259934081",
      "internal_ref": "ghcr.io/liatrio/skills/algorithmic-art"
    }
  ]
}
```

When the operator pins to a SHA directly (no upstream tag exists), `version` and `commit` carry the same value. This is a documented v1 capability (see `pkg/scm.ResolveTag` 40-hex passthrough).

## Quality Gates

- `gofmt -l pkg/config cmd/catalog_add.go cmd/catalog.go` → clean
- `go vet ./pkg/config/... ./cmd/...` → clean
- `go test ./...` → all repo tests pass; no regressions in `pkg/oci`, `pkg/telemetry`, `pkg/catalog`, `pkg/scm`, or pre-existing `cmd/` tests

## Verification

- **Spec FR coverage for Unit 3**:
  - URL form + flag form, mutually exclusive ✓
  - Cheap-and-decisive checks first, network second, file write last ✓
  - Namespace precedence chain ✓
  - Upstream SKILL.md frontmatter surfaced ✓
  - `--dry-run` short-circuit ✓
  - Atomic catalog write ✓
  - No registry contact ✓
  - `--plain` output format matches spec verbatim ✓
- **Real-world end-to-end demonstration** against the public `anthropics/skills` repo proves the production code path (no test seams) works against a real GitHub HTTPS fetch.

## Security

- No credentials in source or proof artifacts.
- The captured proof uses public repo content from `anthropics/skills` — no proprietary or customer data.
- `configAccessor` adapter keeps `pkg/config` types out of `cmd`'s public-facing API surface, so future config changes don't leak into the orchestrator's signature.
