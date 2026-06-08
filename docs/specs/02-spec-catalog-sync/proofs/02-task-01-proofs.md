# Task 01 Proofs — `PushOptions.Annotations` passthrough

## Task Summary

This task proves `pkg/oci.Push` now accepts a caller-supplied
`Annotations map[string]string` and merges it onto the OCI manifest's
annotation set, with **caller keys winning** on collision and a `nil` map
behaving as a no-op. This is the small additive change the catalog-sync plan
needs so provenance (e.g. `org.opencontainers.image.source`/`.revision`) can be
carried on a push and never silently dropped (decision Q4-A).

## What This Task Proves

- A caller annotation absent from the built-in set lands on the pushed manifest.
- A caller annotation that collides with a built-in key (e.g.
  `org.opencontainers.image.title`) overrides the built-in value.
- A `nil`/empty `Annotations` map leaves the built-in annotation set intact and
  introduces no extra keys (additive no-op).

## Evidence Summary

- Three targeted unit tests push `testdata/sample-skill` to an in-process
  read-write OCI registry and read the stored manifest back to assert the
  resulting annotations. All three pass.
- `go vet ./...` is clean and the full `go test ./...` suite is green, so the
  additive field introduces no regression.

## Artifact: Annotation merge / override / no-op unit tests

**What it proves:** All three required behaviors (merge, caller-wins override,
nil no-op) hold against a real push→manifest round-trip.

**Why it matters:** These are the exact behaviors the catalog-sync caller will
rely on to attach provenance; verifying them through an actual push (not just a
unit on the map) proves the annotation survives manifest packing and transport.

**Command:**

~~~bash
go test ./pkg/oci/ -run 'TestPush_MergesCallerAnnotations|TestPush_CallerAnnotationOverridesBuiltin|TestPush_NilAnnotations_UnchangedManifest' -v
~~~

**Result summary:** All three tests pass. The merge test confirms a caller
`org.opencontainers.image.revision` lands on the pulled manifest while the
built-in skill-name annotation remains; the override test confirms a caller
`org.opencontainers.image.title` beats the built-in; the nil test confirms the
built-in set is unchanged and no caller key leaks in.

~~~text
=== RUN   TestPush_MergesCallerAnnotations
--- PASS: TestPush_MergesCallerAnnotations (0.00s)
=== RUN   TestPush_CallerAnnotationOverridesBuiltin
--- PASS: TestPush_CallerAnnotationOverridesBuiltin (0.00s)
=== RUN   TestPush_NilAnnotations_UnchangedManifest
--- PASS: TestPush_NilAnnotations_UnchangedManifest (0.00s)
PASS
ok  	github.com/liatrio/skills-oci/pkg/oci	0.233s
~~~

## Artifact: Full suite + vet (no regression)

**What it proves:** The additive `Annotations` field and merge loop break
nothing else in the codebase.

**Why it matters:** `Push` is on the hot path for every publish; an additive
change must not perturb existing behavior.

**Command:**

~~~bash
go vet ./... && go test ./...
~~~

**Result summary:** `go vet` is clean; every package with tests reports `ok`
(`cmd`, `pkg/catalog`, `pkg/config`, `pkg/oci`, `pkg/scm`, `pkg/telemetry`).

## Reviewer Conclusion

`Push` now merges caller annotations over the built-in set with caller
precedence, and a nil map is a verified no-op — exactly the passthrough the
catalog-sync provenance flow requires, with no regression to the existing push
path.
