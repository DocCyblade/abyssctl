// Package cli is the command-line layer: the only package that prints, prompts and picks exit codes.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/DocCyblade/abyssctl/internal/hostfs"
	"github.com/DocCyblade/abyssctl/internal/op"
)

// Env is everything the CLI takes from the process.
type Env struct {
	Args           []string
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Euid           func() int   // os.Geteuid in production
	Paths          hostfs.Paths // hostfs.System() in production; the Data root is added after the checks
	Clock          op.Clock     // op.SystemClock() in production
}

// Run runs the whole CLI with env and returns the exit code.
func Run(ctx context.Context, env Env) int {
	root := Root()
	root.SetArgs(env.Args)
	root.SetIn(env.Stdin)
	root.SetOut(env.Stdout)
	root.SetErr(env.Stderr)
	if err := root.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "abyssctl: %v\n", err)
		return 1
	}
	return 0
}

// Main runs the CLI with the process's real Env, under a context cancelled on SIGINT or SIGTERM.
func Main() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return Run(ctx, Env{
		Args:   os.Args[1:],
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Euid:   os.Geteuid,
		Paths:  hostfs.System(),
		Clock:  op.SystemClock(),
	})
}

// Root returns the command tree. It is also what the reference is generated from.
func Root() *cobra.Command {
	root := &cobra.Command{
		Use:           "abyssctl",
		Short:         "Run several Actual Budget sync servers on one Docker Host",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.AddCommand(versionCmd())
	return root
}
