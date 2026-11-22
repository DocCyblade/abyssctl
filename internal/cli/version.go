package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/DocCyblade/abyssctl/internal/buildinfo"
)

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show the version, commit, Go version, platform and Caddy pin of this binary",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return writeVersion(cmd.OutOrStdout(), buildinfo.Current())
		},
	}
}

// writeVersion writes foundation.md's text layout for `abyssctl version`.
func writeVersion(w io.Writer, info buildinfo.Info) error {
	_, err := fmt.Fprintf(w,
		"abyssctl  %s\ncommit    %s\ngo        %s\nplatform  %s/%s\ncaddy     %s %s\n",
		info.Version, info.Commit, info.GoVersion, info.OS, info.Arch, info.Caddy.Version, info.Caddy.Digest)
	return err
}
