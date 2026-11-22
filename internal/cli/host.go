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
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return errors.New("host setup is not built yet")
		},
	})
	return host
}
