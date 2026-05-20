# Task 05 Proofs — Release pipeline, schema lockstep CI gate, and README

## Task Summary

This task productionizes the telemetry feature: it injects
`pkg/telemetry.DefaultEndpoint` and `pkg/telemetry.DefaultToken` at release
time via `-ldflags`, vendors the collector's canonical JSON Schema as
`pkg/telemetry/testdata/event-v1.json` and validates the golden body
against it in CI on every PR, and documents what is collected, what is
never sent, and how to opt out in the project README.

Resolves Open Questions #1 (empty `-ldflags` defaults keep stock builds
effectively off until the collector is stood up), #2 (vendored schema +
lockstep CI step), and #3 (4xx drops write to `last-error.log`, already
landed in Task 02).

## What This Task Proves

- `pkg/telemetry/testdata/event-v1.json` exists and is a draft 2020-12 JSON
  Schema covering every required field with documented format/regex/enum
  constraints.
- `TestGolden_ValidatesAgainstSchema` confirms the canonical golden body
  validates against the schema (lockstep guarantee).
- `TestSchema_RejectsBadEvent` confirms the schema actually constrains the
  body — a missing `schema_version`, wrong version, or malformed ULID is
  rejected — so a permissively-written schema cannot silently pass.
- `.github/workflows/ci.yml` runs the schema-lockstep test as an
  explicitly-named step so drift is readable in PR checks.
- `.github/workflows/release.yml` injects telemetry defaults via
  `-X github.com/salaboy/skills-oci/pkg/telemetry.DefaultEndpoint=...` and
  `-X .../DefaultToken=...`, reading `TELEMETRY_ENDPOINT` and
  `TELEMETRY_TOKEN` from GitHub Actions secrets, falling back to empty
  strings when unset.
- `README.md` has a new Telemetry section describing what's collected,
  what's never sent, the three env vars, and the opt-out one-liner.

## Evidence Summary

- `go test ./pkg/telemetry/... -run "TestGolden|TestSchema" -v` → 2 passes.
- `go test ./...` and `go vet ./...` and `go build ./...` are all clean.
- New dep: `github.com/santhosh-tekuri/jsonschema/v5 v5.3.1`.

## Artifact: Schema lockstep test passes

**What it proves:** The golden body and the vendored schema agree.

**Why it matters:** This is the producer-side mirror of the collector's
"CI fails on drift" rule. A change to either file that does not preserve
the other will be caught at PR time.

**Command:**

```bash
go test ./pkg/telemetry/... -run "TestGolden_ValidatesAgainstSchema|TestSchema_RejectsBadEvent" -v
```

**Result summary:**

```text
=== RUN   TestGolden_ValidatesAgainstSchema
--- PASS: TestGolden_ValidatesAgainstSchema (0.00s)
=== RUN   TestSchema_RejectsBadEvent
--- PASS: TestSchema_RejectsBadEvent (0.00s)
PASS
ok  	github.com/salaboy/skills-oci/pkg/telemetry  0.324s
```

## Artifact: CI workflow runs the lockstep check explicitly

**What it proves:** A PR that drifts the producer body away from the
schema will fail a named CI step, not just an opaque "Test" line.

**Diff:** new step in `.github/workflows/ci.yml`:

```yaml
- name: Validate event schema lockstep
  run: go test ./pkg/telemetry/... -run TestGolden_ValidatesAgainstSchema -v
```

## Artifact: Release workflow injects defaults via -ldflags

**What it proves:** Release-time builds receive the collector endpoint and
shared-anti-abuse token via `-ldflags -X`, with secrets sourced from
`secrets.TELEMETRY_ENDPOINT` and `secrets.TELEMETRY_TOKEN`. Stock builds
(forks, or before the collector exists) get empty defaults, which the
transport short-circuits on — keeping telemetry effectively off.

**Diff:** `.github/workflows/release.yml` Build step:

```yaml
env:
  TELEMETRY_ENDPOINT: ${{ secrets.TELEMETRY_ENDPOINT }}
  TELEMETRY_TOKEN: ${{ secrets.TELEMETRY_TOKEN }}
run: |
  LDFLAGS="-s -w \
    -X main.version=${VERSION} \
    -X github.com/salaboy/skills-oci/pkg/telemetry.DefaultEndpoint=${TELEMETRY_ENDPOINT:-} \
    -X github.com/salaboy/skills-oci/pkg/telemetry.DefaultToken=${TELEMETRY_TOKEN:-}"
```

## Artifact: README documents telemetry and opt-out

**What it proves:** Users have a single visible documentation surface with
the contract summary, the never-sent list, env-var reference, and the
opt-out one-liner.

**Diff:** new `## Telemetry` section in `README.md` (between
`Authentication` and `License`).

## Artifact: Whole-project tests + vet + build

**Command:**

```bash
go vet ./...  && go build ./...  && go test ./...
```

**Result summary:** all exit 0.

```text
ok  	github.com/salaboy/skills-oci/cmd
ok  	github.com/salaboy/skills-oci/pkg/oci
ok  	github.com/salaboy/skills-oci/pkg/telemetry
```

## Reviewer Conclusion

The feature is production-ready: release builds will carry the right
defaults, CI catches schema drift by name, and the README documents both
the value users get and the single env var that opts them out. The wire
contract, the producer code, and the CI gate are now all in lockstep.
