package config

// Config is the root shape of .skills-oci.yaml. Only the `catalog`
// section is populated in v1.
type Config struct {
	Catalog CatalogConfig `yaml:"catalog"`
}

// CatalogConfig holds the catalog vendoring settings the CLI reads. All
// fields are optional; zero values mean "fall back to the next layer
// of the precedence chain" (flag > yaml > env > error or built-in default).
type CatalogConfig struct {
	// DefaultNamespace is the prefix used to derive an entry's
	// internal_ref when `catalog add` is invoked without --internal-ref.
	// Format: <registry>/<path-prefix>, no tag.
	DefaultNamespace string `yaml:"default_namespace"`

	// AllowMissingLicense, when true, lets `catalog sync` push an entry
	// whose upstream SKILL.md has no license field. Default false.
	AllowMissingLicense bool `yaml:"allow_missing_license"`

	// Concurrency is the bounded-parallel worker count for
	// `catalog sync`. Default (when unset and no flag) is 4.
	Concurrency int `yaml:"concurrency"`
}
