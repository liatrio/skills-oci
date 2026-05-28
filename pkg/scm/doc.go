// Package scm is the IO-edge layer that handles every interaction with
// upstream source-control hosts (GitHub only in v1). It parses tree URLs,
// resolves tags to commit SHAs via ls-remote, and shallow-fetches a single
// commit into a temp directory. Callers (catalog add, catalog sync) never
// need to know about Git internals.
//
// Implementation note: uses github.com/go-git/go-git/v5 over shelling out
// to a system git binary so skills-oci preserves its single-static-binary
// property and CI runners do not need git installed.
package scm
