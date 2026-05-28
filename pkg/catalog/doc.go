// Package catalog implements the skills-oci catalog vendoring data model.
// It owns the v2 catalog.json and catalog-lock.json formats (schema_version
// 2), the validator that enforces SHA-only commit refs, pure append/diff
// helpers, and atomic file writers. IO is confined to narrow write helpers;
// everything else is pure.
package catalog
