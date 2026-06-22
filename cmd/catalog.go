package cmd

import (
	"github.com/spf13/cobra"
)

// newCatalogCmd builds the `catalog` parent command. It hosts the `add`
// subcommand for vendoring third-party skills into vendored.json.
func newCatalogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "catalog",
		Short: "Vendor third-party skills into an internal OCI registry",
		Long:  "Manage a declarative catalog (vendored.json) of third-party skills vendored from upstream Git repositories.",
	}

	cmd.AddCommand(newCatalogAddCmd())
	return cmd
}
