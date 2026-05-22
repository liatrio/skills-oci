package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/salaboy/skills-oci/pkg/config"
	"github.com/spf13/cobra"
)

// catalogConfigKey is the context key used to stash the .skills-oci.yaml
// config loaded by the `catalog` parent command. Subcommands fetch it
// from cmd.Context() rather than re-reading the file.
type catalogConfigKey struct{}

// configFromContext returns the resolved .skills-oci.yaml config attached
// to ctx, or a zero-value Config if none was attached. Callers tolerate
// the zero value as "no project-level defaults".
func configFromContext(ctx context.Context) config.Config {
	if v, ok := ctx.Value(catalogConfigKey{}).(config.Config); ok {
		return v
	}
	return config.Config{}
}

// newCatalogCmd builds the `catalog` parent command. It hosts the `add`
// and `sync` subcommands and loads .skills-oci.yaml (if present) into
// the command context so subcommands have a uniform place to read
// project-level defaults from.
func newCatalogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "catalog",
		Short: "Vendor third-party skills into an internal OCI registry",
		Long:  "Manage a declarative catalog (catalog.json) of third-party skills vendored from upstream Git repositories. See docs/skills-catalog-data-contract.md for the on-disk contract.",
		PersistentPreRunE: func(c *cobra.Command, _ []string) error {
			cfg, err := loadProjectConfig()
			if err != nil {
				return err
			}
			c.SetContext(context.WithValue(c.Context(), catalogConfigKey{}, cfg))
			return nil
		},
	}

	cmd.AddCommand(newCatalogAddCmd())
	cmd.AddCommand(newCatalogSyncCmd())
	return cmd
}

// loadProjectConfig reads .skills-oci.yaml from the current working
// directory. Absent file returns a zero-value Config (not an error) so
// the precedence chain can fall through to env vars and command-line
// flags. Read errors other than not-found are surfaced so a malformed
// config doesn't silently get ignored.
func loadProjectConfig() (config.Config, error) {
	const path = ".skills-oci.yaml"
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return config.Config{}, nil
		}
		return config.Config{}, fmt.Errorf("reading %s: %w", path, err)
	}
	return config.Load(data)
}
