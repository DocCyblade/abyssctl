// Package cli is the command-line layer: the only package that prints, prompts and picks exit codes.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/DocCyblade/abyssctl/internal/buildinfo"
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

// exempt lists the commands that run without the stamped-build and root checks, by command path. Each reads
// only what is compiled in. Help flags (--help, -h) never reach the checks, because Cobra answers them first.
var exempt = map[string]bool{
	"abyssctl":         true, // a bare abyssctl shows the help
	"abyssctl help":    true,
	"abyssctl version": true,
}

// Run runs the whole CLI with env and returns the exit code.
func Run(ctx context.Context, env Env) int {
	root := newRoot(env)
	root.SetArgs(env.Args)
	root.SetIn(env.Stdin)
	root.SetOut(env.Stdout)
	root.SetErr(env.Stderr)

	err := checkCommand(root, env.Args)
	if err == nil {
		err = root.ExecuteContext(ctx)
	}
	if err != nil {
		showError(env.Stderr, err)
	}
	return op.ExitCode(err)
}

// checkCommand refuses args that name no command. Cobra reports an unknown command from its own lookup, before
// any hook or validator runs, so the CLI looks the command up first.
func checkCommand(root *cobra.Command, args []string) error {
	cmd, _, err := root.Find(args)
	if err != nil {
		return parseError(cmd, err)
	}
	return nil
}

// showError writes err to stderr in plain text: the message of a typed error and its hint, or the error itself.
// cli.md's failure panel replaces this for errors after start-up.
func showError(w io.Writer, err error) {
	var e *op.Error
	if errors.As(err, &e) {
		_, _ = fmt.Fprintln(w, e.Msg)
		if e.Hint != "" {
			_, _ = fmt.Fprintln(w, e.Hint)
		}
		return
	}
	_, _ = fmt.Fprintf(w, "abyssctl: %v\n", err)
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
func Root() *cobra.Command { return newRoot(Env{Euid: os.Geteuid}) }

// newRoot builds the command tree for env.
func newRoot(env Env) *cobra.Command {
	root := &cobra.Command{
		Use:           "abyssctl",
		Short:         "Run several Actual Budget sync servers on one Docker Host",
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return startupChecks(cmd, env)
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetFlagErrorFunc(parseError)
	root.AddCommand(versionCmd(), hostCmd())
	root.InitDefaultHelpCmd() // Cobra adds `help` only inside Execute; checkCommand must find it first
	refuseBadArgs(root)
	return root
}

// startupChecks runs the stamped-build check and then the root check, unless cmd is exempt. Neither reads or
// writes anything on the Host.
func startupChecks(cmd *cobra.Command, env Env) error {
	if exempt[cmd.CommandPath()] {
		return nil
	}
	if err := buildinfo.CheckStamped(); err != nil {
		return op.Refused(fmt.Sprintf(
			"abyssctl is an unstamped development build (%s) and won't act on a Host.\n"+
				"Build it with: make build VERSION=X.Y.Z", buildinfo.Version)).WithCause(err)
	}
	if env.Euid() != 0 {
		return op.Refused(cmd.CommandPath() + " needs root. Run it with sudo.")
	}
	return nil
}

// parseError wraps one of Cobra's parse errors as a refusal, with the command's help as the hint.
func parseError(cmd *cobra.Command, err error) error {
	return op.Refused(err.Error()).WithHint(cmd.CommandPath() + " --help")
}

// refuseBadArgs wraps every Args validator in the tree, so a failed one is refused.
func refuseBadArgs(cmd *cobra.Command) {
	if validate := cmd.Args; validate != nil {
		cmd.Args = func(c *cobra.Command, args []string) error {
			if err := validate(c, args); err != nil {
				return parseError(c, err)
			}
			return nil
		}
	}
	for _, sub := range cmd.Commands() {
		refuseBadArgs(sub)
	}
}
