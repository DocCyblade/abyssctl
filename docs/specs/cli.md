# Spec: cli

Build step 13 of 16, ticketed together with [foundation.md](foundation.md) (step 1) and grown as each verb lands.
The [overview](README.md) has the module map, the call graph and the Host layout. The start-up checks, `Env`,
`Run`, `Main`, `Root`, `op.Event`, `op.Summary`, `op.Mask` and the typed errors are in
[foundation.md](foundation.md); the Locker is in [docker-and-locks.md](docker-and-locks.md); `abyssctl.env` and
`ReadSettings` are in [host.md](host.md). Each module spec owns its own verb's steps, panel rows and outcome text.
Capitalised terms are defined in [GLOSSARY.md](../../GLOSSARY.md).

## 1. Purpose

Build the shared conventions of the CLI layer (`internal/cli` and its output package `internal/cli/out`), which
every verb follows:

- **the verb table**: every verb with its arguments, flags, locks, confirmation, `--dry-run`, `--json`, activity
  log and exit codes, in one place;
- **the lock table**: which locks each verb takes, in which order, how long it waits, and what the
  [Operator](../../GLOSSARY.md) sees when a lock is busy;
- **the output contract**: the summary panel, the prompt, the timestamped `key=value` log lines, the outcome
  line and the failure panel; streams, colour and times;
- **confirmations**: y/N, the typed name, the pick list, hidden input, `--yes`, no TTY, Ctrl-C;
- **`--dry-run`** and **`--json`**, with the five `--json` schemas;
- **exit codes**, including the verbs whose codes differ;
- **the activity log** `/var/log/abyssctl/abyssctl.log`;
- **wiring**: how the CLI reads the settings and builds each module's `Deps` for a run.

The verbs themselves land with their module specs. This spec is ticketed with foundation so the output package,
the prompts, the lock helper, the `--json` writer and the activity log exist before the first verb uses them.

When this spec is done, every verb in the table behaves as its row says, two runs of the same command look the
same on screen, in the journal and in the activity log, and journey 8 (Output contract) passes.

## 2. Interface

`cli` keeps an Interface section although it is cross-cutting, because it is also a package with test seams:
`cli.Run` is how every `internal/cli` test drives a verb, and the output package is what each verb's code calls.
Module specs name `internal/cli` tests that use these seams.

Signature sketches, not final code.

```go
package cli

// Env is foundation's Env with the fields this spec adds. Main is the only code that fills in production
// values. Run fills nothing in: a nil seam means "not used by this run", and a verb that needs one it was not
// given fails with an internal error (exit 1) instead of reaching the real Host. In tests only
// clitest.Env(t) builds an Env; it always sets Paths (hostfs.Under(t.TempDir())), Term (a fake), Clock,
// Zone, Euid, Getenv and PID, and the test adds the fakes its verb needs in Seams.
type Env struct {
    Args           []string
    Stdin          io.Reader
    Stdout, Stderr io.Writer
    Euid           func() int
    Paths          hostfs.Paths   // hostfs.System(); hostfs.Under(t.TempDir()) in tests
    Clock          op.Clock
    Zone           *time.Location // time.Local; tests pass a fixed zone
    Getenv         func(string) string // os.Getenv: NO_COLOR and SUDO_USER only
    PID            int                 // os.Getpid()
    Term           Terminal
    Seams          Seams
}

// Terminal is what the CLI knows about the terminal. The production one uses golang.org/x/term.
type Terminal interface {
    StdoutTTY() bool
    StderrTTY() bool
    // CanPrompt reports whether a question can be asked and answered: stderr is a terminal and an input
    // terminal is open (stdin when it is a terminal; /dev/tty when stdin carried a token, see Confirmations).
    CanPrompt() bool
    ReadLine(ctx context.Context) (string, error)   // one answer, echo on; io.EOF when input ends
    ReadHidden(ctx context.Context) (string, error) // one answer, echo off
}

// Seams are the adapters tests replace. Main sets every field to the production adapter; a nil field is never
// read as "use production".
type Seams struct {
    Docker      docker.Client         // dockerfake in tests
    System      host.System           // hostfake in tests
    HTTP        *http.Client          // registry and Pushover requests; tests point it at httptest servers
    RegistryURL string                // Main: buildinfo.RegistryURL
    PushoverURL string                // Main: buildinfo.PushoverURL
    Proxy       func(hostfs.Paths) Proxy // a fake Proxy for the cert and proxy verbs' tests
    DiskFree    func(path string) (health.Disk, error)
    Executable  func() (string, error) // os.Executable, resolved; host setup's Options.Executable
}

// Proxy is the part of the Proxy module the CLI calls itself (cert verbs, proxy upgrade, image prune's in-use
// set, logs proxy). *proxy.Proxy satisfies it, and also host.Proxy and lifecycle.Proxy.
type Proxy interface { /* Status, Certificates, CheckCertificate, PlanInstallCertificate, InstallCertificate,
                          PlanRemoveCertificate, RemoveCertificate, PlanUpgrade, Upgrade, as in proxy.md */ }

func Run(ctx context.Context, env Env) int // the whole CLI; returns the exit code
func Main() int                            // Run with the real Env, under signal.NotifyContext
func Root() *cobra.Command                 // the command tree; the reference is generated from it
```

```go
package out // internal/cli/out: everything the Operator sees. Imports only the standard library, internal/op, internal/hostfs and internal/locks.

type Options struct {
    Stdout, Stderr io.Writer
    Color          bool           // stderr is a TTY and NO_COLOR is unset or empty
    StdoutColor    bool           // the same for stdout
    Spinner        bool           // stderr is a TTY
    Zone           *time.Location
    Log            *ActivityLog   // nil for a verb that is not logged
}

func New(o Options) *Printer

func (p *Printer) Sink() func(op.Event)      // for op.NewEmitter: one log line per event, screen and file
func (p *Printer) Panel(s op.Summary)        // stderr
func (p *Printer) Warning(text string)       // stderr "! text", and a warning line in the activity log
func (p *Printer) Outcome(text string)       // stdout "✓ text", and the end line in the activity log
func (p *Printer) Failure(err error) int     // stderr failure panel, end line; returns op.ExitCode(err)
func (p *Printer) Table(header []string, rows [][]string) // stdout
func (p *Printer) JSON(doc any) error        // stdout; doc carries Schema and Warnings (see --json)
func (p *Printer) Waiting(what string, h locks.Holder) (stop func()) // the lock-wait spinner on a TTY

// FormatFields is the one key=value rule, used on screen and in the file.
func FormatFields(fields []op.KV) string

// Confirmations. Each returns nil on yes and an op.Refused error otherwise.
func ConfirmYN(ctx context.Context, t Terminal, question string, yes bool) error
func ConfirmName(ctx context.Context, t Terminal, name string, yes bool) error
func Pick(ctx context.Context, t Terminal, question string, items []string) (int, error)

// The activity log. One per run of a logged verb.
type ActivityLog struct{ /* path, pid, zone, clock, buffered lines */ }
func NewActivityLog(p hostfs.Paths, clock op.Clock, zone *time.Location, pid int) *ActivityLog
func (l *ActivityLog) Begin(command []string, user, version string) // the header line
func (l *ActivityLog) Hold()   // keep lines in memory instead of writing them
func (l *ActivityLog) Flush()  // write the held lines, then write as they come
func (l *ActivityLog) Drop()   // forget the held lines (a quiet health check run)
```

### Invariants and error modes

- **Only `internal/cli` and `internal/cli/out` print.** Modules emit events and return errors
  ([foundation.md](foundation.md)); every byte on stdout, stderr or in the activity log goes through `out`.
- **One error, one panel, one exit code.** A verb's function returns an error to `Run`, which shows it once with
  `Printer.Failure` and returns `op.ExitCode(err)`. Nothing else decides the exit code, except the verbs listed
  under [Exit codes](#exit-codes).
- **No Secret reaches the screen, `--json` or the activity log.** Pushover values appear only as `op.Mask(value)`,
  in the activity log's command line too. A Certificate's key and anything inside `data/` are never read by the
  CLI. `logs`, `exec` and `reset-password` pass the container's I/O through unfiltered and never log it; `exec`
  logs only its command line and exit code ([The activity log](#the-activity-log)).
- **A prompt never reads from a pipe.** With no terminal to ask on and no `--yes`, a prompt is refused (exit 2)
  and nothing changes.
- **Plans before locks, locks before acting.** The panel comes from a `Plan…` with no lock held; the run takes its
  locks from the lock table and then calls the module, which checks again.

## 3. Behaviour

### Start-up and wiring

1. `Main` builds the real `Env` under `signal.NotifyContext(SIGINT, SIGTERM, SIGHUP)` and calls `Run`.
2. `Run` builds the tree, parses, runs the exempt commands, then the stamped-build and root checks, as
   [foundation.md](foundation.md) says. Cobra's parse errors are refused (exit 2) with one line and the hint
   `abyssctl <command> --help`. A verb group given an unknown subcommand (`abyssctl host bogus`) is refused the
   same way, with the hint `abyssctl <group> --help`; a bare group (`abyssctl host`) shows its help and exits 0.
3. **Settings.** Every verb except `version`, help and `host setup` first calls `host.ReadSettings(env.Paths)`.
   `ErrNotSetUp` is refused (exit 2, hint `abyssctl host setup`); a `*SettingsError` exits 4 with its problems and
   the hint "fix /etc/abyssctl/abyssctl.env by hand".
   Each of `Settings.Warnings` is printed once as a warning. The run's Paths are then
   `env.Paths.WithDataRoot(s.DataRoot)`.
4. **Modules.** The CLI builds only the modules the verb uses, each once per run, with the shared Docker client,
   one `op.Emitter` (`op.NewEmitter(env.Clock, printer.Sink())`), `env.Clock` and the run's Paths:

   | Module | Built with |
   |---|---|
   | Store | `store.New(p, Clock, buildinfo.Version)` |
   | Locker | `locks.New(p, Clock, Emit, purpose)` ([purpose](#the-lock-table)) |
   | Proxy | `proxy.New` with Docker, Emit, Clock and `buildinfo.CompiledPins()` |
   | Host | `host.New(Deps{Paths: env.Paths, Docker, NewProxy, OS, Emit, Clock, Pins, Version})`; also the source of `Account` |
   | Backup | `Deps` with `Keep: s.BackupKeep` and `Account: host.Account` |
   | Versions | `Deps{RegistryURL, HTTP, Docker, ActualRepo, CaddyRepo, Emit}` from the Seams |
   | Lifecycle | `Deps{Paths, Store, Docker, Proxy, Backup, Locker, Health, Account, Emit, Clock, Version, ActualRepo, BaseDomain: s.BaseDomain}` |
   | Upgrade | as [upgrade.md](upgrade.md) lists, with the same Store, Proxy, Backup and Versions |
   | Health | `Deps` with `Settings{DiskMinFree, CertWarnDays, BaseDomain}`, `Sender`, `SenderNote`, `Zone: env.Zone`, `CaddyPin`, the repos |
   | Alerts | `alerts.New(Config{s.PushoverUser, s.PushoverToken, PushoverURL, HTTP})` |

   - `Lifecycle.Health` is an adapter over `health.Read`: `health.ErrNoResults` → `lifecycle.ErrNoHealthResults`;
     each `health.Problem` → `lifecycle.HealthProblem{Label, Text, Hint, Since}`; the version fields as they are.
   - For `health check`, `alerts.ErrNotConfigured` gives no `Sender` and no note; any other `alerts.New` refusal
     (a lone key) gives no `Sender`, one warning line, and `SenderNote` set to the refusal's sentence.
   - Versions are resolved by the CLI before any lock: `create` ([versions.md](versions.md)), `proxy upgrade`
     ([versions.md](versions.md)) and `upgrade` (`Target`, [upgrade.md](upgrade.md)).
   - The CLI passes the Hostnames from `store.List` to the `cert` verbs, and the in-use images to `image prune`
     ([versions.md](versions.md)). For `cert install` and `cert remove`, the panel and `--dry-run` use a
     `store.List` read with no lock; the real run reads `store.List` again under the Host lock and passes those
     Hostnames to `InstallCertificate` or `RemoveCertificate`, so an Instance created or removed meanwhile is
     counted. `image prune` follows the same pattern: its panel and `--dry-run` use the in-use images from an
     unlocked `store.List`, and the real run builds them again under its locks and passes both sets to `Prune`,
     which skips an image that came into use meanwhile.
5. **Newer records.** An Instance whose `TOOL_VERSION` is newer than this binary stops every changing verb on it
   with exit 4 and the hint `install abyssctl <v> or newer`; the module's guard raises it
   ([lifecycle.md](lifecycle.md)). The check reads the record's `TOOL_VERSION=` line before full validation, so a
   record a newer Abyss wrote gets this exit 4 even when the rest of it would not parse. Read verbs still work and
   show it as a warning.
6. **Signals.** SIGINT, SIGTERM and SIGHUP cancel the run's context once. At a prompt, that is a declined prompt
   (exit 2, nothing changed). During `logs -f` it ends the follow (exit 0). Otherwise the module stops at its next
   safe point as its spec says, and its error decides the exit code; every lock is released when the process
   ends.

### The verb table

Columns: **Lock** from the [lock table](#the-lock-table); **Confirm** is the prompt after the panel ("panel" means
a panel is shown with no prompt); **Log** is the [activity log](#the-activity-log). `--yes` exists exactly on the
verbs with a prompt, and `--dry-run` exactly on the verbs with a panel. No verb takes `--quiet` or `--verbose`.
A `host setup` flag left out keeps the current value; the first-run defaults are in [host.md](host.md).

| Verb | Arguments and flags | Lock | Confirm | `--dry-run` | `--json` | Log | Exit | Spec |
|---|---|---|---|---|---|---|---|---|
| `version` | `--json` | none | none | no | yes | no | 0, 2 | [foundation](foundation.md) |
| `host setup` | `--base-domain D`, `--data-root P`, `--cert F`, `--key F`, `--pushover-user U`, `--pushover-token -`, `--health-interval <d>`, `--disk-min-free <p>`, `--cert-warn-days N`, `--backup-time HH:MM`, `--backup-keep N`, `--unsupported`, `--dry-run`, `--yes` | Host, then every Instance in name order | y/N (none when there is nothing to fix) | yes | no | yes | 0, 1, 2, 4 | [host](host.md) |
| `cert install <cert.pem> <key.pem>` | `--dry-run` | Host | panel | yes | no | yes | 0, 1, 2, 4 | [proxy](proxy.md) |
| `cert list` | | none | none | no | no | no | 0, 1, 2 | [proxy](proxy.md) |
| `cert remove <id>` | `--dry-run`, `--yes` | Host | y/N | yes | no | yes | 0, 1, 2, 4 | [proxy](proxy.md) |
| `proxy upgrade [<version>]` | `--dry-run`, `--yes` | Host | y/N | yes | no | yes | 0, 1, 2, 4 | [versions](versions.md), [proxy](proxy.md) |
| `create <name>` | `--hostname H`, `--version X.Y.Z`, `--internet`, `--dry-run` | Host | panel | yes | no | yes | 0, 1, 2, 4 | [lifecycle](lifecycle.md) |
| `remove <name>` | `--dry-run`, `--yes` | Host, then Instance | typed name | yes | no | yes | 0, 1, 2, 4 | [lifecycle](lifecycle.md) |
| `remove --orphan <name>` | `--dry-run`, `--yes` | Host | typed name | yes | no | yes | 0, 1, 2 | [lifecycle](lifecycle.md) |
| `list` | `--json` | none | none | no | yes | no | 0, 1, 2 | [lifecycle](lifecycle.md) |
| `status` | `--json` | none | none | no | yes | no | 0, 1, 2 | [lifecycle](lifecycle.md) |
| `start <name>` | | Host, then Instance; Host released after the site step | none | no | no | yes | 0, 1, 2, 4 | [lifecycle](lifecycle.md) |
| `stop <name>` | `--dry-run`, `--yes` | Instance | y/N | yes | no | yes | 0, 1, 2, 4 | [lifecycle](lifecycle.md) |
| `restart <name>` | `--dry-run`, `--yes` | Host, then Instance; Host released after the site step | y/N | yes | no | yes | 0, 1, 2, 4 | [lifecycle](lifecycle.md) |
| `set <name>` | `--internet=on\|off` (required), `--dry-run`, `--yes` | Instance | y/N | yes | no | yes | 0, 1, 2, 4 | [lifecycle](lifecycle.md) |
| `logs <name>`, `logs proxy` | `-f`/`--follow`, `--tail N` (default all) | none | none | no | no | no | 0, 1, 2 | [lifecycle](lifecycle.md) |
| `exec <name> [-- <cmd…>]` | | none | none | no | no | command line and exit code, never I/O | the command's own; 1, 2 | [lifecycle](lifecycle.md) |
| `reset-password <name>` | | Instance | the script's own, on a terminal | no | no | header and one event | 0, 1, 2, 4 | [lifecycle](lifecycle.md) |
| `upgrade <name> [<version>]` | `--timeout 5m`, `--dry-run`, `--yes` | Instance | y/N | yes | no | yes | 0, 1, 2, 3, 4 | [upgrade](upgrade.md) |
| `image prune` | `--dry-run`, `--yes` | Host, then every Instance in name order | y/N | yes | no | yes | 0, 1, 2, 4 | [versions](versions.md) |
| `backup <name>` | | Instance | none | no | no | yes | 0, 1, 2, 4 | [backup](backup.md) |
| `backup --all` | `--scheduled` (only with `--all`) | each Instance in turn | none | no | no | yes | 0, 1, 2 | [backup](backup.md) |
| `backup list [<name>]` | `--json` | none | none | no | yes | no | 0, 1, 2 | [backup](backup.md) |
| `backup prune <name>` | `--keep N` (required), `--kind K`, `--dry-run`, `--yes` | Instance | y/N | yes | no | yes | 0, 1, 2, 4 | [backup](backup.md) |
| `restore <name> [<backup-id>]` | `--latest`, `--dry-run`, `--yes` | Instance | pick list (no id, on a TTY), then typed name | yes | no | yes | 0, 1, 2, 4 | [backup](backup.md) |
| `health check` | `--scheduled`, `--json` | health lock | none | no | yes | only a run that changed something | 0, 1, 2 | [health](health.md) |
| `health clear <name>` | | health lock | none | no | no | yes | 0, 1, 2 | [health](health.md) |
| `alerts setup` | `--pushover-user U` (required), `--pushover-token -` | Host, only to save | the token, hidden, on a TTY | no | no | yes | 0, 1, 2 | [alerts](alerts.md) |
| `alerts test` | | none | none | no | no | yes | 0, 1, 2 | [alerts](alerts.md) |

Help (`--help`, `-h`, `help`, a bare `abyssctl`) is Cobra's, needs no root, and is not logged. Cobra's
`completion` command is turned off. Each verb's `Short` and `Long` help text says what its row says about locks,
prompts and flags, so the generated reference ([docs.md](docs.md)) carries the table. The other help-text rules
(no full stop after `Short`, a required `Example`, the example domain and the Secret placeholders) are in
docs.md's [Help text rules](docs.md#help-text-rules).

### The lock table

The CLI takes every lock; modules state "caller holds X" and never lock, with the two exceptions in the overview
(Health's Instance `TryAcquire` inside `Check`, and Lifecycle's `Create` locking the staged Instance).

| Verb | Locks, in order | Wait |
|---|---|---|
| `host setup` | Host, then every Instance in name order; on a first run, through `Options.Lock` after Host creates `state/` ([host.md](host.md)) | 30 s each |
| `image prune` | Host, then every Instance in name order, after the prompt; the in-use images are read again under them | 30 s each |
| `create` | Host, until `Create` returns (`Create` takes the new Instance's own lock) | 30 s |
| `start`, `restart` | Host, then Instance; the Host lock is released after the site step | 30 s each |
| `remove` | Host, then Instance | 30 s each |
| `remove --orphan`, `cert install`, `cert remove`, `proxy upgrade` | Host | 30 s |
| `alerts setup` | Host, after the test message, only to save | 30 s |
| `stop`, `set`, `reset-password`, `upgrade`, `backup <name>`, `backup prune`, `restore` | Instance | 30 s |
| `backup --all` | each Instance in name order, one at a time, released before the next | 30 s each |
| `backup --all --scheduled` | each Instance in name order, one at a time | none: `TryAcquire`, a busy one is skipped and logged |
| `health check` | the health lock, holding nothing else; Instance locks by `TryAcquire` inside `Check` | 30 s by hand; `TryAcquire` with `--scheduled` |
| `health clear` | the health lock | 30 s |
| `version`, help, `list`, `status`, `logs`, `exec`, `cert list`, `backup list`, `alerts test`, every `--dry-run` | none | |

- **Why these locks.** The Host lock guards every Proxy file and Host-wide state, so every verb that can write a
  site file, a Certificate, `proxy.env` or `abyssctl.env` holds it. `upgrade` and `restore` never touch a site
  file, so they take only the Instance lock. A process never waits for the Host lock while holding an Instance
  lock, and only `host setup` and `image prune` hold more than one Instance lock, taken in name order, so two
  `abyssctl` processes can't deadlock ([docker-and-locks.md](docker-and-locks.md)).
- **The site step.** `start` and `restart` need the Host lock only while the site file is written. Lifecycle
  calls `Options.AfterSite` once, right after `Proxy.AddSite` succeeds and before compose up, and the CLI passes
  the Host lock's release as that hook. The Instance lock alone covers compose up and the wait for healthy (up to
  5 minutes), so other Host-lock verbs are not held up. `create` holds the Host lock until `Create` returns,
  because its teardown removes the site file ([lifecycle.md](lifecycle.md)).
- **Wait.** Commands run by hand use `Acquire` with `locks.DefaultWait` (30 s). Only timer runs (`--scheduled`)
  use `TryAcquire`.
- **Purpose.** The Locker's purpose is the command path, then `--all`, `--scheduled` or `--orphan` when given,
  then the Instance name when there is one: `upgrade kids`, `backup --all --scheduled`, `remove --orphan old`,
  `host setup`. It is what other runs see as the holder.
- **Waiting.** `Acquire`'s `lock_wait` event is printed as a log line like any other. On a TTY a spinner line on
  stderr says what it waits for, and goes away when the lock is taken: `⠋ waiting for kids's lock (held by
  abyssctl upgrade kids, pid 4242) … 12s`.
- **Busy.** A lock not obtained in 30 s is exit 1, with nothing changed. The message names the holder from the
  `*BusyError`:

  ```
  ✗ kids is busy: abyssctl upgrade kids (pid 4242) has held its lock since 09:14:02; waited 30s
    → Try again when it finishes
  ```

  The subject is "kids" for an Instance lock, "the Host" for the Host lock and "another Health check" for the
  health lock. A zero `Holder` (an unreadable holder line) is named "another abyssctl command". `since` is Host
  local time, with the date and zone when it is not today.
- **No such Instance.** Two errors, and only these, are refused (exit 2) as `✗ no such Instance: kids` with the
  hint `abyssctl list`: any error for which `errors.Is(err, store.ErrNoInstance)` holds (a name that passes
  `store.ValidName` but has no folder, from any module), and `fs.ErrNotExist` returned by the CLI's own `Acquire`
  of that Instance's lock (it was removed while this run waited). Any other `fs.ErrNotExist`, such as a missing
  `compose.yaml` inside an Instance folder, is not "no such Instance": it keeps its own message and `op.ExitCode`
  (1 unless typed).
- **Plans see unlocked state.** A `--dry-run`, and the panel before a prompt, run with no lock. One that runs while
  another process restores the same Instance can see that Restore's `data.pre-restore/` and refuse with exit 4 as
  if a Restore were interrupted. The real run takes the Instance lock first, so it waits for the Restore and then
  sees the finished state.

### The output contract

**Start-up refusals are plain text.** The stamped-build refusal, the root refusal and Cobra's parse errors are
printed as [foundation.md](foundation.md) gives them, one or two plain lines on stderr with no `✗` panel, no `Log`
line and nothing in the activity log. "Not set up" (exit 2) and a broken `abyssctl.env` (exit 4) are reported the same
way, in plain text, with the hint line `abyssctl host setup` or the `SettingsError`'s problems and its hint. Everything after start-up follows
the contract below.

**Streams.** Results go to stdout: tables, the `--json` document and the outcome line. Everything else goes to
stderr: panels, prompts, progress lines, warnings, spinners and the failure panel. Under a timer both reach the
journal.

**Colour and decoration.** Colour on a stream only when that stream is a terminal and `NO_COLOR` is unset or
empty. Spinners and the pick list only on a TTY. Without a TTY the output is plain text, the same bytes every run
for the same events.

**A changing verb's run** has four parts, in order; each verb's spec gives its rows, steps and outcome text:

1. **The summary panel** (stderr), from the `Plan…`'s `op.Summary`:

   ```
     Upgrade kids
     ──────────────────────────────────────────────
     From        26.10.1
     To          26.11.0 (latest)
     Hostname    kids.budget.example.com
     Downtime    kids is unreachable until it passes its checks (about a minute)
     On failure  automatic Rollback to 26.10.1

     1. pull actualbudget/actual-server:26.11.0
     2. stop kids and take a pre-upgrade Backup
     3. start 26.11.0 and verify it
     ! the old image stays until abyssctl image prune
   ```

   Two-space indent; the title (bold on a TTY); a rule of 46 `─`; one row per `Row`, the label with its first
   letter capitalised and padded to the longest label plus two spaces; a blank line; the steps, numbered; each
   warning as `! text` (yellow on a TTY). A `NoOp` summary shows its title and rows only.
2. **The prompt** (stderr), if the verb has one ([Confirmations](#confirmations)).
3. **Log lines** (stderr), one per event, as the module emits them:

   ```
   09:15:15 backup   instance=kids kind=pre-upgrade id=kids-20261116T141502Z-pre-upgrade size=48MB
   ```

   - The time is `Event.At` in Host local time, `HH:MM:SS`.
   - The step is padded to 8 characters and followed by one space (a longer step is followed by one space).
   - Then the fields, in the order emitted, joined by one space, by `FormatFields`: `key=value`, the value bare
     when it is non-empty and holds only printable characters other than space, `"` and `\`; otherwise
     double-quoted, with `\"`, `\\`, `\n`, `\t` and `\xNN` for other control characters. `held_by="upgrade kids"`,
     `error=""`.
4. **The outcome line** (stdout): `✓ <text>`, two-space indent, green on a TTY: `✓ kids is on 26.11.0 · 41s ·
   Backup kids-20261116T141502Z-pre-upgrade kept`. Verbs that count say so: `✗ 2 of 3 Backups made; bob failed`
   is also an outcome line, on stdout, when the run finished but a part failed. A part skipped because its lock
   was busy is always named as `<name> skipped (busy)`: `✓ 1 Backup made; bob skipped (busy)`.

**The failure panel** (stderr) replaces the outcome line when the verb returns an error:

```
  ✗ Upgrade failed. kids is back on 26.10.1.
    Why     container exited with code 1 during migrations
    Log     /var/log/abyssctl/abyssctl.log
    → less /srv/abyssctl/instances/kids/upgrade-failed-20261116T141544Z.log
```

- Line 1 is the error's message (`op.Error.Msg`, or the error's text).
- `Why` is the cause (`op.Error.Cause`) when there is one and its text is not already in line 1.
- `Log` appears only for a logged verb whose lines reached the activity log.
- `→ <hint>` is `op.Hint(err)` when there is one.
- The exit code is `op.ExitCode(err)`.

**Warnings** (stderr) are `! <text>`, yellow on a TTY: `Settings.Warnings`, a record's warnings, `Result.Warnings`,
"kids was last changed by Abyss 0.4.0; install abyssctl 0.4.0 or newer" on read verbs. They never change the exit
code.

**Tables** (stdout): an uppercase header row, columns separated by at least two spaces, no borders, sorted as the
verb's spec says. A table that is empty prints its header and one line saying so ("no Instances yet").

**`status` and `health check`** use the same pieces: `status` prints its Host line, one entry per Instance (`●`
wanted and running, `○` stopped, `↑` an update or out-of-date files, `!` an invalid record or a disagreement), its
Orphans, then its Problems as `! <label>  <text>` with `→ <hint>` under each, all on stdout, as
[lifecycle.md](lifecycle.md) lists. `health check` prints its events as log lines on stderr, then its warnings, its
Problems and its outcome line ([health.md](health.md)).

**Times.** Times people read are Host local time (`env.Zone`): log lines `HH:MM:SS`, and wherever a date is shown,
the zone abbreviation too (`2026-11-17 03:00 EST`). Machine-facing times are UTC: `--json`, file names, manifests,
`outcomes.json`, `health.json`. The activity log uses RFC 3339 local time with its offset
(`2026-11-17T03:00:04-05:00`). The tool never changes the Host's time zone.

### Confirmations

| Kind | Verbs | What the Operator does |
|---|---|---|
| y/N | `host setup`, `cert remove`, `proxy upgrade`, `stop`, `restart`, `set`, `upgrade`, `image prune`, `backup prune` | `? <Title>? (y/N)`: `y` or `yes`, any case, is yes; anything else, an empty line or end of input is no |
| typed name | `remove`, `remove --orphan`, `restore`, including `restore <name>` finishing an interrupted Restore | `? Type the Instance name to confirm (kids):` the exact name, nothing else |
| pick list | `restore <name>` with no id and no `--latest`, on a TTY, when no Restore is interrupted | a numbered list of the Instance's Backups, newest first, then the panel and the typed name |
| hidden input | `host setup --pushover-user` and `alerts setup` without `--pushover-token`, on a TTY | the Pushover token, echo off |
| input | `host setup` with no `abyssctl.env` and no `--base-domain`, on a TTY | the Base domain |
| none | `create`, `cert install` (panel only); `start`, `backup`, `health clear`, `alerts test` (no panel) | |

- **`--yes`** answers every y/N and typed-name prompt. It does not pick a Backup and does not supply a token or a
  Base domain: each of those has its own flag or argument.
- **No terminal.** When `Terminal.CanPrompt` is false and the prompt is needed, the run is refused (exit 2) before
  any lock, with nothing changed:

  ```
  ✗ upgrade needs a confirmation
    → Run it in a terminal, or pass --yes
  ```

  For the pick list the hint names `abyssctl restore <name> <backup-id>` and `--latest`; for the token,
  `--pushover-token -`; for the Base domain, `--base-domain`.
- **The input terminal** is stdin when stdin is a terminal. When stdin carried the token (`--pushover-token -`),
  it is `/dev/tty` if it opens and stderr is a terminal; otherwise there is none, and the y/N needs `--yes`. A
  prompt never reads its answer from a pipe.
- **No, or Ctrl-C at a prompt**, is refused (exit 2): `✗ Not confirmed; nothing changed`. A typed name that does
  not match is refused the same way: `✗ The name did not match; nothing changed`. No lock was taken and the module
  was not called.
- **`NoOp`.** When the plan has nothing to do, the CLI prints the panel and the outcome line and asks nothing
  (exit 0).
- `reset-password` runs Actual's own prompt on a terminal and is refused without one ([lifecycle.md](lifecycle.md)).

### `--dry-run`

`--dry-run` is on every verb that shows a panel: `host setup`, `cert install`, `cert remove`, `proxy upgrade`,
`create`, `remove`, `remove --orphan`, `stop`, `restart`, `set`, `upgrade`, `image prune`, `backup prune` and
`restore`.

- It calls the `Plan…`, prints the panel and the outcome line `✓ Dry run: nothing changed`, and exits 0. It takes
  no lock, asks nothing, writes nothing, pulls nothing and is not logged.
- If the plan refuses, `--dry-run` refuses with the same error and exit code as the real run would.
- Lookups the plan needs still happen: the CLI resolves versions before the plan ([versions.md](versions.md)), and
  a `Plan…` may query the registry. Until Versions lands, `create` needs `--version` and `create --dry-run` shows
  the digest as resolved at run time ([lifecycle.md](lifecycle.md)).

### `--json`

`--json` is on `version`, `list`, `status`, `backup list` and `health check`. Changing verbs have no JSON form.

- One JSON document on stdout, indented two spaces, ending in a newline. Panels, progress lines and warnings still
  go to stderr, without colour.
- Every document is an object with `"schema": 1` first and `"warnings"` last: the warnings the run printed, as
  strings, `[]` when there are none.
- Keys are snake_case. Times are RFC 3339 UTC with whole seconds (`2026-11-18T14:15:01Z`); a time that may be
  absent is `null`. Lists are always arrays, never `null`. Strings that may be absent are `""` unless a schema
  says the key is omitted.
- **Stability.** Field names and meanings are fixed from v1. A later version may add fields under `"schema": 1`;
  renaming or removing one raises `schema`. Readers ignore fields they don't know.
- **On failure** no document is written: the failure panel goes to stderr and the exit code says what happened.
  `health check` is the exception: Problems are a result, not a failure, so its document is written with exit 1.
- The schemas are in [Files and formats](#--json-schemas).

### Exit codes

| Code | Meaning |
|---|---|
| 0 | done, nothing to do, or a dry run |
| 1 | failed: Docker, disk, network, a lock not obtained in 30 s, or an error a module could not classify |
| 2 | refused before acting: bad arguments, not root, an unstamped build, not set up, no such Instance, a declined or unconfirmed prompt, a target the rules don't allow |
| 3 | an Upgrade failed and was rolled back: the Instance is fine on its old version |
| 4 | needs the Operator: a failed Rollback, an interrupted Upgrade or Restore, an Instance written by a newer Abyss, an invalid Instance record, `proxy.env` or `abyssctl.env` (fix by hand) |

An invalid Instance record or `proxy.env` is `op.NeedsOperator`: the message names the file, line and problem,
and the hint is "fix <file> by hand". A name with no Instance folder is still "no such Instance" (exit 2).

`op.ExitCode` decides, except for these verbs:

- **`health check`** by hand: 1 when any Problem is current, else 0; a `Check` error is 1; 2 for a refusal before
  `Check`. With
  `--scheduled`: 0 whatever it found, a skipped run included; 1 only when `Check` fails or a lock error other than
  busy; 2 for a refusal before `Check`. A Problem is not a failed timer run.
- **`status`** exits 0 whatever it shows, 1 only when Store or Docker can't be read at all, and 2 for a refusal
  before anything is read (not root, not set up).
- **`backup --all`** exits 0 when every Backup was made (busy skips under `--scheduled` included), 1 when any
  failed.
- **`exec`** exits with the command's own exit code when it is non-zero (`*docker.ExitError`); its own refusals
  and failures are 2 and 1.
- **`logs -f`** ended by Ctrl-C exits 0.
- **`alerts`**: Pushover rejecting the credentials (`ErrRejected`) is a refusal (exit 2) in `alerts setup`, which
  saves nothing, and a failure (exit 1) in `alerts test` ([alerts.md](alerts.md)).

### The activity log

`/var/log/abyssctl/abyssctl.log` (`Paths.LogFile()`) records every changing run, by hand or by a timer, including
refused and failed attempts, so the Operator can see what happened on the Host and when.

- **Which runs.** Every verb with "yes" in the Log column, from the moment both start-up checks pass. Not logged:
  `version`, help, Cobra parse errors, the stamped-build and root refusals ([foundation.md](foundation.md)), every
  `--dry-run`, and the read verbs (`list`, `status`, `logs`, `cert list`, `backup list`). `reset-password`
  logs its header, its one `reset_password` event and its end line, never its input or output. `exec` logs its
  `begin` line, whose `command` holds the command line with the command it runs, masked by the rule below
  (`command="abyssctl exec kids -- node /app/scripts/x.js"`), and its `end` line with the exit code (the
  command's own when it ran), never the container's input or output. `logs` is not logged. `alerts test` is
  logged because it reaches outside the Host.
- **`health check`** holds its lines in memory and writes them, with its outcome line, only when the run emitted a
  `problem`, `alert` or `versions` event (a Problem started, ended or was dropped; an Alert was sent or failed; the
  versions were fetched, or the fetch failed) or failed. A quiet run, and a skipped `--scheduled` run, log nothing.
- **`host setup` on a first run** holds its lines in memory until Host has created `/var/log/abyssctl/`, then
  writes them and goes on appending. A run refused or failed before that is shown on screen only.
- **The lines** are the screen's lines without colour, with the time as RFC 3339 local time with its offset and
  the pid after it. A run has a `begin` line, then a `confirm` line for a prompt, each log line, each warning, and
  an `end` line. Panels, prompts and tables are not written. See [the format](#the-activity-log-file).
- **The command line** in `begin` is `abyssctl` followed by the arguments as typed, each shell-quoted when it
  needs it. The value of `--pushover-user`, in either the `--flag value` or `--flag=value` form, is written as
  `op.Mask(value)`. `--pushover-token` takes only `-`; any other value is refused (exit 2), and the `begin` line
  of that refused run shows it as `op.Mask(value)`, so the log never holds a token.
- **The user** is `SUDO_USER` when it is set and non-empty, else the name of the effective user (`root`).
- **Writing.** Each line is one `write(2)` to the file opened with `O_APPEND|O_CREATE`, then the file is closed
  again, so lines from two runs never interleave inside a line and log rotation needs no `copytruncate` or
  signal ([render.md](render.md)). The CLI creates a missing file as root:adm 0640, setting the mode and owner
  itself because the process runs with `umask 077`. Host creates the folder, root:adm 0750
  ([host.md](host.md)).
- **When the log can't be written** (no folder, a full disk), the run goes on: one warning, "activity log not
  written: <reason>", on stderr, and the exit code is unchanged.
- **No Secret** ever reaches the file, by the same rules as the screen.

## 4. Files and formats it owns

| Path | Mode | Written |
|---|---|---|
| `/var/log/abyssctl/abyssctl.log` | root:adm 0640 | appended by every logged run; created by the CLI when missing |

### The activity log file

```
2026-11-18T09:15:01-05:00 4242 begin    command="abyssctl upgrade kids" user=ken version=0.3.0
2026-11-18T09:15:04-05:00 4242 confirm  answer=yes by=prompt
2026-11-18T09:15:04-05:00 4242 pull     image=actualbudget/actual-server:26.11.0
2026-11-18T09:15:16-05:00 4242 pulled   digest=sha256:3f9a0c… took=12s
2026-11-18T09:15:16-05:00 4242 stop     instance=kids
2026-11-18T09:15:45-05:00 4242 end      exit=0 outcome="✓ kids is on 26.11.0 · 41s · Backup kids-20261118T141518Z-pre-upgrade kept"
2026-11-18T03:00:02-05:00 5120 begin    command="abyssctl backup --all --scheduled" user=root version=0.3.0
2026-11-18T03:00:02-05:00 5120 skipped  instance=bob reason=busy
2026-11-18T03:00:02-05:00 5120 stop     instance=kids
2026-11-18T03:00:09-05:00 5120 backup   instance=kids kind=scheduled id=kids-20261118T080002Z-scheduled size=48MB
2026-11-18T03:00:09-05:00 5120 end      exit=0 outcome="✓ 1 Backup made; bob skipped (busy)"
```

- One line per record, `\n` endings, UTF-8: `<RFC 3339 local time> <pid> <step padded to 8> <fields>`, with the
  fields by `FormatFields`.
- `begin`: `command`, `user`, `version` (`buildinfo.Version`).
- `confirm`: `answer=yes`, and `by=prompt` or `by=--yes`. A declined prompt ends the run with its `end` line.
- `warning`: `text`.
- `end`: `exit`, then `outcome` (the outcome line's text) whenever an outcome line was printed, whatever the
  exit code (`end exit=1 outcome="✗ 1 of 2 Backups made; kids failed"`); otherwise `error` (the failure panel's
  first line) and, when there is one, `hint`.
- Times come from `Event.At` for log lines and from `env.Clock` for the others.

### `--json` schemas

Every key below is always present unless marked "omitted when empty". `…` stands for the full value.

**`version`** (from `buildinfo.Current()`; `host setup` reads `version` from it):

```json
{
  "schema": 1,
  "version": "0.3.0",
  "commit": "9b1c4e2d7f3a…",
  "go": "go1.27.1",
  "os": "linux",
  "arch": "amd64",
  "caddy": { "version": "2.10.2", "digest": "sha256:…" },
  "warnings": []
}
```

**`list`** (one entry per `lifecycle.Entry`, sorted by name):

```json
{
  "schema": 1,
  "instances": [
    {
      "name": "kids",
      "record": {
        "hostname": "kids.budget.example.com",
        "actual_version": "26.10.1",
        "actual_image_digest": "sha256:…",
        "state": "running",
        "internet": false,
        "created_at": "2026-11-01T15:20:00Z",
        "tool_version": "0.3.0",
        "upgrade_backup": "",
        "upgrade_healthy": false
      },
      "record_error": "",
      "live": {
        "exists": true,
        "state": "running",
        "health": "healthy",
        "started_at": "2026-11-18T08:00:03Z",
        "container_id": "4f1c…",
        "networks": ["abyssctl"]
      }
    }
  ],
  "warnings": []
}
```

`record` is `null` and `record_error` holds the problems, one sentence each joined by `; `, for an invalid record.
`record.state` is what the Operator wants (`STATE`); `live.state` is Docker's, `""` with `exists: false`, and
`started_at` is `null` and `networks` is `[]` when there is no container. `record.internet` is the record's
[Internet access](../../GLOSSARY.md) (`INTERNET=1`); `live.networks` is the networks the container is attached
to, sorted.

**`status`** (the `lifecycle.Report`):

```json
{
  "schema": 1,
  "tool_version": "0.3.0",
  "proxy": { "set_up": true, "running": true, "version": "2.10.2", "image": "…", "behind": false },
  "instances": [
    {
      "name": "kids", "record": { … }, "record_error": "", "live": { … },
      "agrees": true,
      "files_out_of_date": "",
      "written_by_newer": "",
      "interrupted": "",
      "networks": "",
      "update_available": "26.11.0"
    }
  ],
  "orphans": [ { "name": "old", "containers": ["abyssctl-old"] } ],
  "health": {
    "checked_at": "2026-11-18T14:10:01Z",
    "note": "",
    "problems": [ { "label": "kids", "text": "…", "hint": "abyssctl start kids", "since": "2026-11-18T14:05:01Z" } ],
    "caddy_update": "",
    "versions_stale": false
  },
  "warnings": []
}
```

`tool_version` is the running binary's `buildinfo.Version`, not the record's `TOOL_VERSION` (that is
`record.tool_version`). `instances[]` carries the same `name`, `record`, `record_error` and `live` as `list`. `interrupted` is `""`,
`"upgrade"` or `"restore"`. `health.checked_at` is `null` with the note "no Health check results yet".

**`backup list`** (one entry per `backup.Info`, newest first per Instance, Instances by name):

```json
{
  "schema": 1,
  "backups": [
    {
      "id": "kids-20261117T080004Z-scheduled",
      "instance": "kids",
      "kind": "scheduled",
      "created_at": "2026-11-17T08:00:04Z",
      "actual_version": "26.10.1",
      "actual_image_digest": "sha256:…",
      "tool_version": "0.3.0",
      "size": 50331648,
      "path": "/srv/abyssctl/backups/kids/kids-20261117T080004Z-scheduled.tar.gz",
      "problem": "the manifest can't be read"
    }
  ],
  "warnings": []
}
```

`size` is bytes. `problem` is omitted when empty.

**`health check`** (the `health.Result`):

```json
{
  "schema": 1,
  "checked_at": "2026-11-18T19:10:01Z",
  "skipped": false,
  "instances": 3,
  "busy": [],
  "problems": [
    {
      "kind": "instance_down", "subject": "kids", "label": "kids", "text": "…",
      "hint": "abyssctl start kids", "event": false, "since": "2026-11-18T19:05:01Z"
    }
  ],
  "started": [],
  "ended": [],
  "alerts": { "sent": 1, "unsent": 0, "error": "" },
  "warnings": []
}
```

`problems` are the current Problems in display order; `started` and `ended` hold the Problems that started or
ended in this run, in the same shape; `busy` names the Instances skipped because a command held their lock;
`alerts.error` is the last send error of this run, `""` when none failed. A skipped `--scheduled` run (the health
lock was busy) gives `"skipped": true`, `checked_at` `null`, `instances` 0 and the lists empty.

### Package layout

```
cmd/abyssctl/main.go          umask, then cli.Main
internal/cli/
  root.go        Env, Seams, Terminal, Run, Main, Root, the checks, the exempt list, parse errors
  wire.go        settings, Paths, building each module's Deps, the Health and Proxy adapters
  locking.go     the lock table, purposes, busy and no-such-Instance errors
  term.go        the production Terminal (golang.org/x/term, /dev/tty)
  version.go host.go cert.go proxy.go instance.go upgrade.go backup.go health.go alerts.go   one file per verb group
  clitest/       a fake Terminal and helpers to call Run with temp Paths, a ManualClock and the Seams
  gendocs/       the reference generator
internal/cli/out/
  printer.go     panels, log lines, outcome, failure panel, tables, JSON
  fields.go      FormatFields
  confirm.go     ConfirmYN, ConfirmName, Pick
  activitylog.go ActivityLog
  testdata/      golden files
```

## 5. Depends on

- [foundation](foundation.md): `Env`, `Run`, `Main`, `Root` and the start-up checks; `internal/op` (`Event`,
  `Emitter`, `Summary`, `Mask`, the typed errors, `ExitCode`, `Hint`, `Clock`); `internal/hostfs` (`Paths`,
  `LogFile`, the lock paths); `internal/buildinfo`.
- [docker-and-locks](docker-and-locks.md): `docker.Client` and its CLI adapter; `locks.New`, `Acquire`,
  `TryAcquire`, `DefaultWait`, `BusyError`, `Holder`.
- Every module spec, for its verb: [store](store.md) (`List`, `ValidName`, `ErrNoInstance`), [host](host.md)
  (`ReadSettings`, `Setup`, `SetPushover`, `Account`), [proxy](proxy.md), [lifecycle](lifecycle.md),
  [backup](backup.md), [versions](versions.md), [upgrade](upgrade.md), [alerts](alerts.md), [health](health.md).
- Dependencies: Cobra and `cobra/doc` (only `internal/cli` and `internal/cli/gendocs`); `golang.org/x/term` (only
  `internal/cli`).

Import rules this spec adds to `internal/archtest`: `internal/cli/out` imports only the standard library,
`internal/op`, `internal/hostfs` (for `Paths`) and `internal/locks` (for `Holder`), and only `internal/cli`
imports it; `golang.org/x/term` is
imported only by `internal/cli`.

## 6. Decisions

- **One output style for every verb** (summary panel → prompt → timestamped `key=value` lines → outcome line, or
  a failure panel): the panel lets the Operator see the plan before acting, and the log lines are greppable and
  identical on screen, in the journal and in the activity log.
- **Results on stdout, everything else on stderr**, so `abyssctl status --json | jq` and `abyssctl list > file`
  carry only the result, and a timer's stderr still reaches the journal.
- **Confirmation fits the loss.** Typing the name for `remove` and `restore`, which delete or replace data; y/N
  for verbs that stop a service or delete something that can be made again; no prompt for `start`, `backup` and
  `create`, which lose nothing, or for `cert install`, which deletes only Certificates the new one fully replaces
  and lists them in its panel. `--yes` skips every prompt, and with no terminal and no `--yes`
  the tool refuses instead of guessing.
- **A prompt is answered only on a terminal.** Reading answers from a pipe would let `yes |` confirm a `remove`
  by accident; `--yes` is the one way to confirm without a terminal.
- **`--dry-run` wherever there is a panel**, built from the same `Plan…` as the panel, so a dry run refuses
  exactly when the run would.
- **`--json` only on read verbs in v1**, with a top-level `schema` and fields fixed from v1, so scripts can rely on
  them. Changing verbs have the outcome line and the exit code.
- **Exit codes 0–4** say what state the Host is in: done, failed, refused with nothing changed, rolled back, or
  waiting for the Operator. `health check --scheduled` exits 0 for Problems because a Problem is the check working.
- **The activity log is a plain append-only file** written by the CLI, not a journal-only record, so it survives
  journal rotation, is readable by the `adm` group, and holds the same lines the Operator saw. Each line is opened,
  appended and closed so rotation needs no signal. A log that can't be written never fails a run.
- **A missing Instance is a refusal (exit 2)**: nothing was changed and the argument names nothing, the same
  whether the name was wrong or the Instance was removed while this run waited.
- **`golang.org/x/term`** is the third dependency: hidden input for the Pushover token and TTY detection, which the
  standard library does not offer without `syscall` code per platform. It is imported only by `internal/cli`.
- **CLI tests run through `cli.Run`** with temp Paths, fakes in `Seams` and a fake `Terminal`, unprivileged.
  Journey 8 checks the same contract on the real binary as root.

## 7. Later

- Shell completion: Cobra's `completion` command, which stays off in v1.
- `abyssctl log`: a verb that shows the activity log.

## 8. Docs it changes

- Reference: the root `abyssctl` page, from the root command's `Long` help: exit codes 0–4, `--yes`, `--dry-run`,
  `--json` and `schema`, results on stdout and the rest on stderr, `NO_COLOR`, the activity log's path, and that a
  prompt needs a terminal or `--yes`. Each verb's page, from its help text, states its lock wait, prompt and flags
  as in the verb table.
- `docs/guides/security.md`: the activity log is readable by the `adm` group and holds no Secrets; the Pushover
  user is masked in it; `--pushover-token -` keeps the token out of shell history.
- `docs/guides/upgrades.md`: exit code 3 and the failure panel's log pointer (with [upgrade.md](upgrade.md)).
- `.github/ISSUE_TEMPLATE/bug.yml`: asks for the outcome line or failure panel and the run's activity log lines
  ([docs.md](docs.md)).

## 9. Acceptance list

Module tests are in `internal/cli` and `internal/cli/out`. CLI tests call `cli.Run` with
`hostfs.Under(t.TempDir())`, an `op.ManualClock`, a fixed `Zone`, `Euid` 0, a fake `Terminal` and fakes in `Seams`
(`dockerfake`, `hostfake`, `httptest` registry and Pushover), without root. Each module spec's `internal/cli`
tests check its own verb's rows; the tests below check the shared contract. Journey 8 runs the real binary as root
on an Ubuntu runner.

### Root, Env and exit codes

1. For every verb in `Root()`: `--yes` exists exactly on the verbs with a y/N or typed-name prompt, `--dry-run`
   exactly on the verbs with a panel, `--json` exactly on `version`, `list`, `status`, `backup list` and
   `health check`, and `completion` does not exist. → `TestVerbTable` (walks `Root()` against a table copied from
   this spec)
2. `clitest.Env(t)` always sets `Paths` under `t.TempDir()` and a fake `Term`; a verb run with a seam it needs left
   nil fails with exit 1 and touches nothing outside the temp root. → `TestEnv_NilSeam`
3. Every verb except `version`, help and `host setup` is refused (exit 2) in plain text with the hint
   `abyssctl host setup` when `abyssctl.env` is missing, and exits 4 with the `SettingsError` problems when it is
   broken;
   settings warnings are shown once. → `TestNotSetUp` (table over `Root()`), `TestSettingsWarnings`
4. Cobra parse errors (an unknown verb, an unknown subcommand of a group, an unknown flag, a missing argument)
   exit 2 with one plain line and the help hint, and are not logged; a bare group shows its help and exits 0. → `TestParseErrors` (table)
5. Exit codes: `health check` by hand 0/1/2 and with `--scheduled` 0 for Problems and for a skipped run, 1 for a
   `Check` error, 2 for a Host that is not set up; `status` 0 with Problems, 1 when Docker can't be read and 2 when
   not set up; `list` and `cert list` 2 when not set up; `backup --all` 1 when one Instance fails and 0 when one is
   skipped as busy; `exec` returns the command's own code; `logs -f` ended by cancellation exits 0. →
   `TestExitCodes` (table)
6. `errors.Is(err, store.ErrNoInstance)` from any module, and `fs.ErrNotExist` from the CLI's own `Acquire` of an
   Instance's lock, are refused (exit 2) with "no such Instance: <name>" and the hint `abyssctl list`; a wrapped
   `fs.ErrNotExist` for a missing `compose.yaml` is not, and keeps its own message and exit code. →
   `TestNoSuchInstance` (table); an invalid Instance record and an invalid `proxy.env` exit 4 with the file, line and
   problem and the hint "fix <file> by hand", not as "no such Instance". → `TestInvalidFiles` (table)
7. `internal/cli/out` imports only what the import rule allows and only `internal/cli` imports it;
   `golang.org/x/term` is imported only by `internal/cli`. → `TestImportRules` in `internal/archtest`

### Output: lines, panels and streams

8. A log line is `HH:MM:SS` local time from `Event.At`, the step padded to 8, and the fields in emitted order; a
   value with a space, a quote, a backslash, a control character or nothing is quoted and escaped, and any other
   value is bare. → `TestFormatFields` (table), `TestLogLine`
9. A panel renders the title, the rule, padded rows with capitalised labels, numbered steps and `!` warnings; a
   `NoOp` panel shows title and rows only. → `TestPanel` (golden)
10. Colour appears on a stream only when it is a TTY and `NO_COLOR` is unset or empty; spinners and the pick list
    only on a TTY. → `TestColour` (table)
11. Tables, the `--json` document and the outcome line go to stdout; panels, prompts, log lines, warnings and the
    failure panel go to stderr. → `TestStreams` (table over one verb of each kind)
12. A returned error shows one failure panel with its message, `Why` only when the cause adds something, `Log`
    only when the run's lines reached the activity log, and `→ <hint>`, and the exit code is `op.ExitCode(err)` for
    each error class; the start-up refusals print foundation's plain text and no panel. → `TestFailurePanel`
    (table)
13. A part skipped because its lock was busy is named "<name> skipped (busy)" in the outcome line. →
    `TestOutcome_BusySkip`

### Prompts and `--dry-run`

14. y/N accepts `y` and `yes` in any case; `n`, other text, an empty line and end of input decline with exit 2,
    "Not confirmed; nothing changed", no lock taken and the module not called. → `TestConfirmYN` (table)
15. The typed name accepts only the exact name; anything else is refused (exit 2) with nothing changed. →
    `TestConfirmName` (table)
16. With `CanPrompt` false and no `--yes`, each prompting verb is refused (exit 2) with "<verb> needs a
    confirmation" and the hint, before any lock; with `--yes` it goes on without reading input. →
    `TestConfirm_NoTerminal` (table over the prompting verbs)
17. A cancelled context at a prompt is refused (exit 2) with nothing changed. → `TestConfirm_Cancelled`
18. A `NoOp` plan prints the panel and the outcome line, asks nothing and exits 0. → `TestNoOp_SkipsPrompt`
    (table)
19. `--dry-run` on every verb that takes it prints the panel and "✓ Dry run: nothing changed", exits 0, holds no
    lock, makes no changing `dockerfake` call, writes no file under the temp root and writes nothing to the
    activity log; when the plan refuses, it exits with the same code as the run. → `TestDryRun` (table over the
    13 verbs)

### Locks

20. For each verb not covered by another spec's lock test, the module is called holding exactly the locks in the
    lock table, in order (a fake module probes every lock path with `TryAcquire` from a second Locker), and
    `--scheduled` uses `TryAcquire`. → `TestLockTable` (table; Lifecycle's rows are
    [lifecycle.md](lifecycle.md)'s `TestLifecycleCmds_Locks`, and the Host lock released after the site step in
    `start` and `restart` is its `TestStartCmd_ReleasesHostLockAfterSite`)
21. The Locker's purpose for each verb is as the lock table says (`upgrade kids`, `backup --all --scheduled`,
    `remove --orphan old`, `host setup`). → `TestLockPurpose` (table)
22. A lock held by another Locker beyond a short wait gives exit 1 with nothing changed and a message naming the
    holder's purpose, pid and since, and "another abyssctl command" for a zero `Holder`; the `lock_wait` event is
    printed as a log line, and the spinner appears only on a TTY. → `TestLockBusy` (table)

### Activity log

23. A logged run writes `begin` (command, user from `SUDO_USER` or the effective user, version), `confirm`, each
    log line, each warning and `end` with RFC 3339 local times with offset and the pid, and no panel, prompt or
    table; `end` carries `outcome=` whenever an outcome line was printed, whatever the exit code, and otherwise
    `error=` and `hint=`. → `TestActivityLog_Lines` (golden, including a `backup --all` with a failed part)
24. The verbs marked logged write the activity log, refused and failed runs included; the others, `--dry-run` and
    the start-up refusals don't; `alerts test` is logged, `reset-password` logs only its header, its event and its
    end line, and `exec` logs only its `begin` line with the command line and its `end` line with the command's exit
    code, never the container's I/O. → `TestActivityLog_WhichVerbs` (table over `Root()`); the `exec` lines
    themselves are [lifecycle.md](lifecycle.md)'s `TestExecCmd_Log`
25. A `--pushover-user` value, and a `--pushover-token` value other than `-` (which is refused), are masked to
    `…abcd` in the `begin` line, in both flag forms, for `host setup` and `alerts setup`. → `TestActivityLog_MasksPushover` (table)
26. A quiet `health check` run and a skipped one log nothing; a run with a `problem`, `alert` or `versions` event,
    or a `Check` error, logs its lines and outcome. → `TestActivityLog_HealthCheck` (table)
27. A first `host setup` holds its lines until the log folder exists and then writes all of them from its `begin`;
    a run refused before that writes no file. → `TestActivityLog_FirstRunHeld`
28. A missing log file is created with mode 0640; each line is appended with the file opened and closed again, so
    a file renamed during a run gets no later line; a log that can't be written gives one warning and leaves the
    exit code unchanged. → `TestActivityLog_File`

### `--json`

29. Each `--json` document is one object on stdout with `"schema": 1` first and `"warnings"` last, snake_case keys
    only, RFC 3339 UTC times with whole seconds, `null` only for absent times and records, and arrays never `null`.
    → `TestJSON_Envelope` (table over the five verbs, walking the decoded document)
30. `version`, `list`, `status`, `backup list` and `health check` (a normal and a skipped run) match their schemas;
    `list` and `status` carry `record.internet` and `live.networks`, with one Instance with Internet access and one
    without. → `TestJSON_Schemas` (golden, one file each)
31. A verb with `--json` that fails writes nothing to stdout; `health check --json` with a current Problem writes
    its document and exits 1. → `TestJSON_OnFailure`

### Verb groups

32. `host setup` with `--pushover-token -` reads the token from stdin and asks the y/N on the input terminal when
    `CanPrompt` is true, and is refused (exit 2) without `--yes` when it is not; no prompt ever reads from a pipe.
    → `TestHostSetupCmd_TokenFromStdin`
33. `cert install` and `cert remove` build the panel and `--dry-run` from an unlocked `store.List`, and the real
    run passes the Hostnames of a `store.List` read under the Host lock, so an Instance created between the two is
    counted. → `TestCertVerbs_HostnamesUnderLock`
34. `restore <name>` with no id on a terminal shows the pick list newest first, then the panel and the typed name;
    with no terminal it is refused (exit 2) naming `restore <name> <backup-id>` and `--latest`; `restore <name>`
    finishing an interrupted Restore shows no pick list and asks the typed name (`--yes` skips it). →
    [backup.md](backup.md)'s `TestRestoreCmd_Selection` (its finish row) and `TestBackupCmds_Prompts` (its
    finish row)

### Journeys, Release check and docs

35. Journey 8: as root, the panels and outcome lines of `stop --dry-run`, `image prune --dry-run` and `remove
    --dry-run`, the `status` output, and the `--json` documents of `version`, `list`, `status`, `backup list` and
    `health check` match golden files (times, ids and digests normalised). → journey 8
36. Journey 8: without a TTY and without `--yes`, `stop`, `remove` and `restore` exit 2 and change nothing; the
    same with `--yes` exit 0; a non-root run of `status` exits 2 with "needs root. Run it with sudo." and leaves the
    activity log unchanged. → journey 8
37. Journey 8: the activity log is root:adm 0640 and holds a `begin` and an `end` line for each changing run of the
    journey, with the outcome text the screen showed. → journey 8
38. Prompts on a real terminal (y/N, the typed name, the pick list, the hidden token prompt, Ctrl-C at a prompt)
    behave as above. → the Release check's manual steps (checked by release.md's Release check)
39. Docs: the root reference page, each verb's help text, `security.md`, `upgrades.md` and the bug template say
    what [Docs it changes](#8-docs-it-changes) lists. → (review)
