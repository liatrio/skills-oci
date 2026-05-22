// Package catalog implements the skills-oci catalog vendoring data model
// defined in docs/skills-catalog-data-contract.md. It owns the v1
// catalog.json and catalog-lock.json formats, the validator that enforces
// SHA-only commit refs, pure append/diff helpers, and atomic file writers.
// IO is confined to narrow write helpers; everything else is pure.
package catalog
