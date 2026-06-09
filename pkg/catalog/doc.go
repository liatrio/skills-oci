// Package catalog implements the skills-oci vendoring data model. It owns
// the vendored.json format (schemaVersion 1) — the desired-state list of
// third-party skills vendored into a repo — along with its validator
// (SHA-only commit pins), pure upsert/sort helpers, and an atomic file
// writer. IO is confined to narrow write helpers; everything else is pure.
package catalog
