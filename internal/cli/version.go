package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/DocCyblade/abyssctl/internal/buildinfo"
)

func versionCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Show the version, commit, Go version, platform and Caddy pin of this binary",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if asJSON {
				return writeVersionJSON(cmd.OutOrStdout(), buildinfo.Current())
			}
			return writeVersion(cmd.OutOrStdout(), buildinfo.Current())
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the version document as JSON")
	return cmd
}

// writeVersion writes foundation.md's text layout for `abyssctl version`.
func writeVersion(w io.Writer, info buildinfo.Info) error {
	_, err := fmt.Fprintf(w,
		"abyssctl  %s\ncommit    %s\ngo        %s\nplatform  %s/%s\ncaddy     %s %s\n",
		info.Version, info.Commit, info.GoVersion, info.OS, info.Arch, info.Caddy.Version, info.Caddy.Digest)
	return err
}

// versionDoc is cli.md's `version` --json schema; the field order is the document's key order.
type versionDoc struct {
	Schema   int      `json:"schema"`
	Version  string   `json:"version"`
	Commit   string   `json:"commit"`
	Go       string   `json:"go"`
	OS       string   `json:"os"`
	Arch     string   `json:"arch"`
	Caddy    imagePin `json:"caddy"`
	Warnings []string `json:"warnings"`
}

type imagePin struct {
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

// writeVersionJSON writes the `version` document, indented two spaces and ending in a newline.
func writeVersionJSON(w io.Writer, info buildinfo.Info) error {
	doc := versionDoc{
		Schema:   1,
		Version:  info.Version,
		Commit:   info.Commit,
		Go:       info.GoVersion,
		OS:       info.OS,
		Arch:     info.Arch,
		Caddy:    imagePin{Version: info.Caddy.Version, Digest: info.Caddy.Digest},
		Warnings: []string{},
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}
