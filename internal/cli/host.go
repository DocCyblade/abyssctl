package cli

import (
	"errors"

	"github.com/spf13/cobra"
)

func hostCmd() *cobra.Command {
	host := &cobra.Command{
		Use:   "host",
		Short: "Set up and manage the Host",
	}
	host.AddCommand(&cobra.Command{
		Use:   "setup",
		Short: "Set up the Host for Abyss",
		Long: `Sets up the Host for Abyss. It needs root.

This build does not have host setup yet: it exits 1 without changing anything.`,
		Example: `  sudo abyssctl host setup`,
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return errors.New("host setup is not built yet")
		},
	})
	return host
}
