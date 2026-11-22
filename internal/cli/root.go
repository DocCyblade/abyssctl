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

// rootLong is the root command's Long help, and so the synopsis of the reference's index page. cli.md owns what
// it says.
const rootLong = `Abyss runs several Actual Budget sync servers on one Docker Host. Each one is an Instance, with its
own data, behind the one Proxy. Every command except version and help needs root: run it with sudo.

Results go to stdout: tables, the --json document and the outcome line. Everything else goes to
stderr: panels, prompts, progress lines, warnings and errors. Colour is used only on a terminal, and
never when NO_COLOR is set to a non-empty value.

A prompt is asked only on a terminal and never reads its answer from a pipe. Without a terminal, a
command that would prompt needs --yes, or it is refused (exit 2) with nothing changed.

Flags shared by verbs:

    --dry-run  on every verb that shows a panel: show the plan, change nothing and exit 0
    --yes      on every verb with a prompt: answer every y/N and typed-name prompt
    --json     on version, list, status, backup list and health check: print one JSON document
               on stdout; its "schema" field is 1, its fields are fixed from v1, and renaming or
               removing one raises schema

Exit codes (a few verbs differ, and their own help says how):

    0  done, nothing to do, or a dry run
    1  failed: Docker, disk, network, a lock not obtained in 30 s, or an unclassified error
    2  refused before acting: nothing changed
    3  an Upgrade failed and was rolled back: the Instance is fine on its old version
    4  needs the Operator: fix what the message names by hand

Every changing run, refused and failed attempts included, is recorded in the activity log,
/var/log/abyssctl/abyssctl.log.`

// newRoot builds the command tree for env.
func newRoot(env Env) *cobra.Command {
	root := &cobra.Command{
		Use:           "abyssctl",
		Short:         "Run several Actual Budget sync servers on one Docker Host",
		Long:          rootLong,
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
