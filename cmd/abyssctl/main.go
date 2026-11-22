// Command abyssctl runs several Actual Budget sync servers on one Docker Host.
package main

import (
	"os"
	"syscall"

	"github.com/DocCyblade/abyssctl/internal/cli"
)

func main() {
	syscall.Umask(0o077)
	os.Exit(cli.Main())
}
