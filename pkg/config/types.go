package config

// Config is the root shape of .skills-oci.yaml. Only the `catalog`
// section is populated in v1.
type Config struct {
	Catalog CatalogConfig `yaml:"catalog"`
}

// CatalogConfig holds the catalog vendoring settings the CLI reads. All
// fields are optional; zero values mean "fall back to the next layer of
// the precedence chain". For namespace resolution that chain is, highest
// first: --internal-ref (which skips namespace/config entirely) > the
// --namespace flag (which overrides this config but not --internal-ref) >
// this yaml config > the SKILLS_OCI_DEFAULT_NAMESPACE env var > error.
type CatalogConfig struct {
	// DefaultNamespace is the base OCI namespace used to derive an entry's
	// internal_ref when `catalog add` is invoked without --internal-ref. It is
	// source-qualified per skill to <base>/<owner>/<repo>/<name>, so
	// this value is just the registry-qualified prefix.
	// Format: <registry>/<path-prefix>, no tag (e.g. ghcr.io/liatrio/skills).
	DefaultNamespace string `yaml:"default_namespace"`
}
