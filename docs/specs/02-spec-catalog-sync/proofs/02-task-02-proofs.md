# Task 02 Proofs — `vendored.json` types + IO (`pkg/catalog`)

## Task Summary

This task proves `pkg/catalog` now defines the `vendored.json` desired-state
schema (`Vendored` / `VendoredEntry`) and the pure load / validate / upsert +
atomic-write primitives that `catalog add` (Task 03) and the out-of-scope
`core` reader both depend on. All logic is implemented as pure functions with
IO confined to the existing `writeAtomic` rename helper.

## What This Task Proves

- `LoadVendored` parses the plan's exact JSON shape (camelCase `schemaVersion`,
  snake_case `internal_ref`, the 7 string entry fields) and survives a
  write → reload round-trip with an equal struct.
- `ValidateVendored` rejects an unsupported `schemaVersion`, any entry missing
  a required field, and any `commit` that is not a 40-char lowercase hex SHA —
  the load-bearing immutability guard.
- `UpsertVendored` replaces an existing entry by `(namespace, name)` in place
  (reporting `replaced == true`), appends new entries, returns a deterministic
  order (namespace then name), and does not mutate its input.
- `WriteVendoredAtomic` validates before writing, writes via temp-file +
  rename, bootstraps a zero `schemaVersion` to 1, and leaves no partial file on
  invalid content.

## Evidence Summary

- The `pkg/catalog` Vendored test set passes (`go test ./pkg/catalog/ -run
  Vendored`).
- Per-function coverage: `LoadVendored`, `ValidateVendored`, and
  `UpsertVendored` are at **100%**; `WriteVendoredAtomic` is at 88.9% with the
  sole gap being the unreachable `json.MarshalIndent` error return on an
  already-validated struct (defensive code, no reachable branch).

## Artifact: Vendored schema + IO unit tests

**What it proves:** Every required behavior — round-trip fidelity, each
validation rejection branch, upsert append/replace/order/no-mutate, and durable
atomic write — holds.

**Why it matters:** This is a cross-repo JSON contract (`core` reads exactly
this shape); a producer-side validation + deterministic write is what keeps
`vendored.json` diffs minimal and machine-consumable.

**Command:**

~~~bash
go test ./pkg/catalog/ -run Vendored -v
~~~

**Result summary:** All cases pass, including the 14-case table for
`ValidateVendored` (bad schema, each missing field, short/uppercase/non-hex
commit, plus valid and empty-skills cases).

~~~text
--- PASS: TestLoadVendored_RoundTrip
--- PASS: TestLoadVendored_Errors
--- PASS: TestValidateVendored (14 subtests)
--- PASS: TestUpsertVendored (5 subtests)
--- PASS: TestWriteVendoredAtomic
ok  	github.com/liatrio/skills-oci/pkg/catalog
~~~

## Artifact: Branch coverage on the critical pure logic

**What it proves:** The contract-critical functions meet the repo's 100% branch
target for critical pure logic (CLAUDE.md).

**Why it matters:** Validation and upsert are the load-bearing correctness
guarantees of the schema; full coverage is the repo's quality gate for them.

**Command:**

~~~bash
go test -coverprofile=cov.out ./pkg/catalog/ && go tool cover -func=cov.out | grep vendored.go
~~~

**Result summary:** `LoadVendored`, `ValidateVendored`, `UpsertVendored` =
100%. `WriteVendoredAtomic` = 88.9% (only the unreachable marshal-error line is
uncovered).

~~~text
vendored.go:41:	LoadVendored		100.0%
vendored.go:56:	ValidateVendored	100.0%
vendored.go:87:	UpsertVendored		100.0%
vendored.go:121:	WriteVendoredAtomic	88.9%
~~~

## Reviewer Conclusion

`pkg/catalog` now provides a fully-tested, pure `vendored.json` schema with
load, contract validation, idempotent deterministic upsert, and durable atomic
write — ready for `catalog add` to wire in at Task 03, with the
contract-critical functions at full branch coverage.
