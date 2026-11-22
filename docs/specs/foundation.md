# Spec: foundation

Build step 1 of 16, ticketed together with [cli.md](cli.md) and [testing.md](testing.md). The
[overview](README.md) has the module map, the call graph and the Host layout. Capitalised terms are defined in
[GLOSSARY.md](../../GLOSSARY.md).

## 1. Purpose

Lay down the repo that every later spec builds in, and the small shared packages every module imports:

- the Go module, its toolchain pin, and the `cmd/` + `internal/` layout;
- `internal/buildinfo`: the stamped `-X` variables, the compiled-in pins (`pins.json`) and version comparison;
- `internal/hostfs`: the paths value (every path the tool touches) and the one way to write a file;
- `internal/op`: the contract between modules and the CLI: `Event` / `Emitter`, the typed errors and their exit
  codes, `Summary` (what every `Plan…` returns) and the `Clock`;
- the start of `internal/cli`: `umask 077`, the root command, the stamped-build check, the root check, error →
  exit code, and `abyssctl version`;
- the Makefile's `tools`, `lint`, `test` and `build` targets, `.golangci.yml`, an import-rules test, and the CI
  workflow that runs them on every push;
- from [docs.md](docs.md), ticketed with this step: the reference generator `internal/cli/gendocs` with the help tests, `make docs` and `make docs-check`, the `ci.yml` step, `internal/docscheck` with `TestLinks` and the section tests, `LICENSE`, and H2 skeletons of the six guides and `CONTRIBUTING.md`. docs.md owns
  their content.

When this spec is done, a stamped binary prints its version without root, refuses every other command without
root, an unstamped binary refuses to act on a [Host](../../GLOSSARY.md), and CI is green.

The verb table, output contract, confirmations, lock table and `--json` schemas belong to [cli.md](cli.md). The
journey harness, the journeys and the test image cache belong to [testing.md](testing.md).

## 2. Interface

Signature sketches, not final code. The three shared packages are leaves: each imports only the standard library
(and `internal/buildinfo` imports nothing from `internal/`). Any module may import them.

### `internal/buildinfo`: what is stamped or compiled in

```go
// Set with -ldflags "-X github.com/DocCyblade/abyssctl/internal/buildinfo.<Name>=<value>".
// The defaults are the release values, except Version and Commit, which every release build stamps.
var (
    Version     = "0.0.0-dev"                            // SemVer without a leading "v", e.g. "0.1.0"
    Commit      = "unknown"                              // full git commit hash
    ActualRepo  = "docker.io/actualbudget/actual-server" // image name, registry host included
    CaddyRepo   = "docker.io/library/caddy"
    RegistryURL = "https://registry-1.docker.io"         // base URL of the registry API for both repos
    PushoverURL = "https://api.pushover.net"             // base URL of the Pushover API (alerts.md)
)

const Unstamped = "0.0.0-dev"

type ImagePin struct{ Version, Digest string } // Digest is "sha256:<64 hex>"

type Pins struct {
    Caddy            ImagePin
    DockerEngineMin  string // "25.0.5"
    DockerComposeMin string // "2.24"
}

func CompiledPins() Pins      // parsed once from the embedded pins.json
func CheckStamped() error     // an error if Version is Unstamped or not valid SemVer

type Info struct {
    Version, Commit, GoVersion, OS, Arch string
    Caddy                                ImagePin
}

func Current() Info // the variables above plus runtime.Version(), runtime.GOOS, runtime.GOARCH

// SemVer 2.0 without a leading "v": MAJOR.MINOR.PATCH[-prerelease][+build].
func ValidVersion(v string) bool
func CompareVersions(a, b string) (int, error) // -1, 0, +1; an error if either is not valid

// PlainVersion reports whether s is a plain stable version: MAJOR.MINOR.PATCH, three decimal numbers with no
// leading zeros, no "v", no pre-release and no build part.
func PlainVersion(s string) bool
```

- **No runtime override.** Nothing in the tool reads an image name, the registry URL or the Pushover URL from
  anywhere but these variables: there is no env var, flag or settings key for them. Journey builds stamp them to
  point at a local registry and an `httptest` Pushover ([testing.md](testing.md)); the release binary keeps the
  defaults.
- **Image names carry their registry host.** A pull uses `<repo>@<digest>` or `<repo>:<version>`. The name used
  with `RegistryURL` (`/v2/<name>/tags/list`) is the repo with its first path element, the registry host,
  removed; [versions.md](versions.md) owns those lookups.
- **Docker shortens Docker Hub names.** `image inspect` (RepoDigests) and `image ls` show a Docker Hub image
  without `docker.io/` (and without `library/` for official images), so code that matches image names
  normalises both sides first. [docker-and-locks.md](docker-and-locks.md) owns that normalisation;
  [versions.md](versions.md) relies on it for `image prune`.
- **`Version` is what the tool writes as `TOOL_VERSION`** in an [Instance record](../../GLOSSARY.md)
  ([store.md](store.md) owns the record). `CompareVersions` follows SemVer 2.0 precedence: a pre-release sorts
  before its release, and build metadata is ignored. It is the one comparison behind the downgrade refusal
  ([host.md](host.md)) and the exit 4 for an Instance whose `TOOL_VERSION` is newer than the binary
  ([cli.md](cli.md)).
- **`CheckStamped`** rejects the unstamped default and any `Version` that is not valid SemVer; the CLI wraps its
  error in `op.Refused`. A pre-release such as
  `0.2.0-dev.3` is stamped and runs, so a developer can try a build on a test Host.
- **`PlainVersion` is the one plain `X.Y.Z` rule** for Actual versions, the Caddy pin and `PROXY_VERSION`:
  `0.1.0` and `26.11.0` pass; `v26.11.0`, `26.11`, `26.011.0`, `26.11.0-rc1` and `26.11.0+b1` don't. Every
  module that checks a plain version calls it, except [render.md](render.md), which can't import `buildinfo` and
  keeps its own unexported copy. The cases live in one table, `internal/buildinfo/testdata/plain_versions.txt`
  (one `<version> <true|false>` per line), and both packages' tests run it, so the two copies can't drift.
- `CompiledPins` panics if the embedded file does not parse. That can only be a broken commit, and
  `TestCompiledPins_Valid` catches it before any build ships.

### `internal/hostfs`: every path, and the one way to write a file

```go
const DefaultDataRoot = "/srv/abyssctl"

type Paths struct{ /* unexported: a prefix for tests and the Data root */ }

func System() Paths                                     // the real fixed paths; no Data root yet
func Under(prefix string) Paths                         // tests only: every fixed path moved under prefix
func (p Paths) WithDataRoot(dir string) (Paths, error)  // dir must be absolute and clean
func (p Paths) HasDataRoot() bool

// Fixed paths (not under the Data root).
func (p Paths) Binary() string                // /usr/local/bin/abyssctl
func (p Paths) SettingsDir() string           // /etc/abyssctl
func (p Paths) SettingsFile() string          // /etc/abyssctl/abyssctl.env
func (p Paths) LogDir() string                // /var/log/abyssctl
func (p Paths) LogFile() string               // /var/log/abyssctl/abyssctl.log
func (p Paths) SystemdDir() string            // /etc/systemd/system
func (p Paths) SystemdUnit(file string) string // /etc/systemd/system/<file>
func (p Paths) MotdScript() string            // /etc/update-motd.d/90-abyssctl
func (p Paths) LogrotateRule() string         // /etc/logrotate.d/abyssctl

// Under the Data root.
func (p Paths) DataRoot() string
func (p Paths) InstancesDir() string                        // instances/
func (p Paths) InstanceDir(name string) string              // instances/<name>/
func (p Paths) InstanceRecord(name string) string           // instances/<name>/instance.env
func (p Paths) InstanceCompose(name string) string          // instances/<name>/compose.yaml
func (p Paths) InstanceOutcomes(name string) string         // instances/<name>/outcomes.json
func (p Paths) InstanceLock(name string) string             // instances/<name>/.lock
func (p Paths) InstanceData(name string) string             // instances/<name>/data/
func (p Paths) InstancePreRestore(name string) string       // instances/<name>/data.pre-restore/
func (p Paths) UpgradeFailedLog(name string, at time.Time) string // instances/<name>/upgrade-failed-<stamp>.log
func (p Paths) CreatingDir(name string) string              // instances/.<name>.creating/
func (p Paths) CreatingLock(name string) string             // instances/.<name>.creating/.lock
func (p Paths) RemovingDir(name string) string              // instances/.<name>.removing/
func (p Paths) BackupsDir() string                          // backups/
func (p Paths) InstanceBackups(name string) string          // backups/<name>/
func (p Paths) RemovedBackupsDir() string                   // backups/removed/
func (p Paths) RemovedBackups(name string, at time.Time) string // backups/removed/<name>-<stamp>/
func (p Paths) ProxyDir() string                            // proxy/
func (p Paths) Caddyfile() string                           // proxy/Caddyfile
func (p Paths) ProxyCompose() string                        // proxy/compose.yaml
func (p Paths) ProxyEnv() string                            // proxy/proxy.env
func (p Paths) SitesDir() string                            // proxy/sites/
func (p Paths) SiteFile(name string) string                 // proxy/sites/<name>.caddy
func (p Paths) CertsDir() string                            // proxy/certs/
func (p Paths) CertFile(id string) string                   // proxy/certs/<id>.crt
func (p Paths) KeyFile(id string) string                    // proxy/certs/<id>.key
func (p Paths) CaddyData() string                           // proxy/caddy-data/
func (p Paths) CaddyConfig() string                         // proxy/caddy-config/
func (p Paths) StateDir() string                            // state/
func (p Paths) HealthState() string                         // state/health.json
func (p Paths) MotdText() string                            // state/motd.txt
func (p Paths) HostLock() string                            // state/host.lock
func (p Paths) HealthLock() string                          // state/health.lock

// File-name timestamps: UTC, ISO 8601 basic format, e.g. "20261117T080004Z".
func Stamp(t time.Time) string
func ParseStamp(s string) (time.Time, error)

// The one way to write a file.
func WriteFile(path string, data []byte, perm fs.FileMode) error
func Create(path string, perm fs.FileMode) (*Pending, error) // for large or streamed files
func (f *Pending) Write(b []byte) (int, error)
func (f *Pending) Path() string  // the temp file's path, so the writer can read it back before Commit
func (f *Pending) Commit() error // fsync, chmod, rename over path, fsync the folder
func (f *Pending) Abort()        // removes the temp file; safe after Commit, safe to call twice

// The one way to make a folder.
func EnsureDir(path string, perm fs.FileMode) error
```

- **One value, built only by the CLI.** `internal/cli` calls `System()`, gets the settings from
  [Host](host.md) (which reads `SettingsFile()`; [host.md](host.md) owns its format), and calls
  `WithDataRoot(DATA_ROOT)`. Each module that needs paths takes the `Paths` in its constructor and never calls
  `System()` or `Under()` itself. Module tests use `Under(t.TempDir())` and a [Data root](../../GLOSSARY.md)
  inside that temp folder, so they run
  unprivileged and never touch the real Host.
- **The layout is the overview's.** Every path in the [Host layout](README.md#host-layout) table except Docker's
  apt files has exactly one method here, and no module builds a path under the Data root, `/etc` or `/var/log` any
  other way, except Host's `System` adapter, which reads `/etc/os-release`, the apt sources and `/proc/net/tcp{,6}`
  itself and writes Docker's two apt files ([host.md](host.md)). Inside an
  Instance's `data/` and a [Backup](../../GLOSSARY.md) archive, names belong to [backup.md](backup.md). A Backup's file names in
  `backups/<name>/` belong to [backup.md](backup.md) too, built on `InstanceBackups` and `Stamp`.
- **No disk access, no name checks.** `Paths` methods only join strings. Callers pass an Instance name the
  [Instance store](store.md) has already validated and a [Certificate](../../GLOSSARY.md) id from [proxy.md](proxy.md).
- **A Data root path before `WithDataRoot` is a bug** and panics. The CLI never hands a module a `Paths` without a
  Data root, except to [Host](host.md) for `host setup` on a Host that is not set up yet.
- **Machine-facing times in file names are UTC** and use `Stamp`, which sorts as text in time order and holds no
  `-` or `:`. A Backup id (`<name>-<stamp>-<kind>`, [backup.md](backup.md)) uses the same stamp.
- **`WriteFile` and `Create`** write to a temp file in `path`'s own folder named `<base>.<8 random hex>.partial`,
  created with `O_EXCL`, then fsync, chmod to exactly `perm` (whatever the umask), rename over `path` and fsync
  the folder. A reader sees the old file or the new one, never part of one, and never a file with a looser mode.
  On any error the temp file is removed and `path` is untouched. Only its writer reads a `.partial` file (Backup
  verifies an archive through `Path()` before `Commit`). A temp file left by a crash keeps its `.partial` name.
  Backup deletes leftovers in `backups/<name>/*.partial` under the Instance lock ([backup.md](backup.md)), and
  Proxy's `Ensure` deletes `proxy/certs/*.partial` under the Host lock ([proxy.md](proxy.md)); no other module
  sweeps `.partial` files, and a small one left elsewhere is harmless because nothing reads it.
- **Neither creates folders nor changes owners.** A missing folder is an error. `EnsureDir` creates a missing
  folder (and missing parents at 0700) and then sets the folder itself to exactly `perm`. Owners are
  [Host](host.md)'s job (and [Backup](backup.md)'s after a Restore, and [Store](store.md)'s for a new
  Instance's `data/`).
- Every file the tool writes on the Host goes through `WriteFile` or `Create`, with these exceptions, each owned
  by the spec named:
  - the activity log, which is appended to ([cli.md](cli.md));
  - lock files, opened in place and held with `flock` ([docker-and-locks.md](docker-and-locks.md));
  - files a Restore extracts into `data/` and the Regroup's in-place SQLite edits ([backup.md](backup.md));
  - whatever Caddy writes in `proxy/caddy-data/` and `proxy/caddy-config/`, and whatever Actual writes in
    `data/`: the containers' own state, not the tool's writes.

  `make lint` enforces the rule ([forbidigo](#4-files-and-formats-it-owns)): `os.WriteFile` and `os.Create` are
  banned outside `internal/hostfs`. The exceptions above open their files with `os.OpenFile`, which the lint
  allows.

### `internal/op`: the contract between modules and the CLI

```go
// Events: one progress line each. Modules emit; only the CLI prints.
type KV struct{ Key, Value string }
func F(key, value string) KV

type Event struct {
    At     time.Time // set by the Emitter from its Clock, never by the module
    Step   string    // short verb for the line, e.g. "backup", "pull", "stop"
    Fields []KV      // in the order given
}

type Emitter interface {
    Emit(step string, fields ...KV)
}

func NewEmitter(clock Clock, sink func(Event)) Emitter // the CLI's; sink renders the line
func Discard() Emitter
type Recorder struct{ /* … */ }                        // for tests in any package
func NewRecorder(clock Clock) *Recorder                // Recorder implements Emitter
func (r *Recorder) Events() []Event

func Mask(secret string) string // "…abcd": the last four characters; "…" alone if shorter than 8

// Typed errors and exit codes.
var (
    ErrRefused       = errors.New("refused")            // exit 2
    ErrRolledBack    = errors.New("rolled back")        // exit 3
    ErrNeedsOperator = errors.New("needs the Operator") // exit 4
)

type Error struct {
    Class error  // ErrRefused, ErrRolledBack or ErrNeedsOperator
    Msg   string // one sentence for the Operator, no trailing period, never a Secret
    Hint  string // optional next step as a full command line, e.g. "abyssctl restart kids"
    Cause error  // optional underlying error
}
func (e *Error) Error() string   // Msg, then ": " and Cause if set
func (e *Error) Unwrap() []error // Class and Cause, so errors.Is(err, op.ErrRefused) works

func Refused(msg string) *Error
func RolledBack(msg string) *Error
func NeedsOperator(msg, hint string) *Error // panics on an empty hint: it is required
func (e *Error) WithHint(hint string) *Error
func (e *Error) WithCause(err error) *Error

func ExitCode(err error) int // 0, 1, 2, 3 or 4
func Hint(err error) string  // the Hint of the outermost *Error, or ""

// Plans: what a changing operation would do.
type Row struct{ Label, Value string }

type Summary struct {
    Title    string   // e.g. "Upgrade kids"
    Rows     []Row    // facts in order, e.g. {"from", "26.10.0"}, {"to", "26.11.0"}, {"downtime", "about 1 min"}
    Steps    []string // what will happen, in order, one short line each
    Warnings []string // e.g. "the key file is deleted; keep a copy if you need it"
    NoOp     bool     // nothing to do
}

// The clock seam.
type Clock interface{ Now() time.Time }
func SystemClock() Clock
type ManualClock struct{ /* … */ }        // for tests in any package
func NewManualClock(t time.Time) *ManualClock
func (c *ManualClock) Now() time.Time
func (c *ManualClock) Set(t time.Time)
func (c *ManualClock) Advance(d time.Duration)
```

**Events**

- Each module that needs them takes an `Emitter`, a `Clock` and the `Paths` in its constructor (with its other
  dependencies). A module never
  writes to stdout, stderr or the activity log. The CLI renders each `Event` as one timestamped `key=value` line
  on screen and in the activity log; the line format, quoting and colour are [cli.md](cli.md)'s output contract.
- `Step` and every `Key` are lowercase snake_case (`[a-z][a-z0-9_]*`). A `Recorder` panics on a step or key that
  breaks the rule, so module tests catch it; the production Emitter passes it through.
- A field value is never a [Secret](../../GLOSSARY.md). A Pushover credential appears only as `Mask(value)`.
  Nothing from inside an Instance's `data/` is ever a field value.
- Emitting is synchronous: `Emit` returns after the sink has taken the event, so lines appear in the order the
  module emitted them.

**Typed errors and exit codes**

- `ExitCode(nil)` is 0. Otherwise it takes the outermost `*Error` in the chain (`errors.As`) and maps its `Class`:
  `ErrRefused` → 2, `ErrRolledBack` → 3, `ErrNeedsOperator` → 4. With no `*Error` in the chain, a wrapped bare
  sentinel maps the same way (checked in the order 4, 3, 2). Anything else, a lock timeout included, is 1.
- **Refused (2)** means nothing was changed: bad arguments, not root, an unstamped build, a declined or
  unconfirmed prompt, or a target the rules don't allow. A module returns `Refused` only before it has changed
  anything.
- **Rolled back (3)** means an Upgrade failed and the Instance is back on its old version and data
  ([upgrade.md](upgrade.md)).
- **Needs the Operator (4)** means the tool stopped and won't go on by itself: a failed Rollback, an interrupted
  Restore, an Instance whose `TOOL_VERSION` is newer than the binary. It always carries a `Hint`, which is why
  `NeedsOperator` takes one.
- A failure of any other kind (Docker, disk, network) is a plain wrapped Go error and exits 1. A module that
  refuses because of such an error writes `op.Refused(msg).WithCause(err)`.
- [cli.md](cli.md) owns how an error is shown (the failure panel, the hint line, the log file pointer) and the
  verbs whose exit codes differ, such as `health check`.

**Plans**

- Each operation `X(…)` that the CLI shows a panel for ([cli.md](cli.md)'s verb table) has a twin
  `PlanX(…) (op.Summary, error)` with the same arguments. A module may add other `Plan…` reads, such as
  `Backup.PlanCreate`. The CLI renders the `Summary` as the summary panel before the prompt, and alone for
  `--dry-run`.
- A `Plan…` never takes a lock, never writes a file, never pulls an image and never calls a Docker method that
  changes anything. It may read files, inspect Docker and query a registry.
- A `Plan…` runs every check its operation makes before acting. If the operation would refuse, the plan returns
  the same `Refused` error, so `--dry-run` refuses (exit 2) exactly when the real run would, given the same
  state.
- The operation never takes a `Summary` as input. Once the CLI holds the locks, the operation checks again and
  acts on what it finds.
- `NoOp` means there is nothing to do; the CLI prints the panel, skips the prompt and exits 0.
- `Summary` values are never Secrets, the same rule as event fields.

**Clock**

- The clock is one of the three real seams (with Docker and the Alert channel). Production code gets the time
  only from a `Clock`, never from `time.Now()`, so Retention, the Backup schedule, debounce and staleness are
  tested with a `ManualClock`. `make lint` bans `time.Now`, `time.Since` and `time.Until` outside `internal/op`,
  where the real `Clock` lives.

### `internal/cli` and `cmd/abyssctl`: start-up, the two checks, `version`

```go
// cmd/abyssctl/main.go: the whole file.
func main() {
    syscall.Umask(0o077)
    os.Exit(cli.Main())
}

// internal/cli
type Env struct {
    Args           []string
    Stdin          io.Reader
    Stdout, Stderr io.Writer
    Euid           func() int   // os.Geteuid in production
    Paths          hostfs.Paths // hostfs.System() in production; the Data root is added after the checks
    Clock          op.Clock     // op.SystemClock() in production
    // cli.md adds the rest of the fields and defines them: Zone, Getenv, PID, Term (the terminal) and
    // Seams (the adapters tests replace).
}

func Run(ctx context.Context, env Env) int // the whole CLI; returns the exit code
func Main() int                            // Run with the process's real Env, under signal.NotifyContext
func Root() *cobra.Command                 // the command tree; also what `make docs` generates the reference from
```

`Run` is the CLI's test seam: [cli.md](cli.md)'s module tests (the activity log, for example) call it with
`Under(t.TempDir())` paths, a `ManualClock` and an `Euid` that returns 0, so they run unprivileged and never
touch the real Host. `Main` adds nothing but the real values and the signal context; it is the only place the
production values are filled in ([cli.md](cli.md)).

`internal/cli` is the only package that imports Cobra. The verbs, their flags and the rest of the CLI layer are
[cli.md](cli.md)'s and each module spec's.

## 3. Behaviour

### Start-up

1. `main` sets the process umask to `077` before anything else, so every file and folder the tool or a child
   process creates starts out owner-only.
2. `cli.Run` builds the command tree. Cobra's default `completion` command is turned off.
3. Cobra parses the arguments. Its parse errors (an unknown command, a flag error through `SetFlagErrorFunc`, a
   failed `Args` validator) are wrapped as `op.Refused`, so they exit 2.
4. **Exempt commands run now:** `version`, and help (`--help`, `-h`, `help`, and `abyssctl` with no command).
   They read only what is compiled in, so the Operator can check a downloaded binary before giving it root. The
   exemption is a fixed list in `internal/cli`, not a per-command flag. Any later exemption must pass the same
   test: it reads nothing but what is compiled in.
5. **Stamped-build check** (in the root command's `PersistentPreRunE`): `buildinfo.CheckStamped()`. An unstamped
   or badly stamped build stops with exit 2 and, on stderr (with the binary's actual `Version`):

   ```
   abyssctl is an unstamped development build (<Version>) and won't act on a Host.
   Build it with: make build VERSION=X.Y.Z
   ```

6. **Root check** (same hook): if `Env.Euid()` is not 0, stop with exit 2 and, on stderr:

   ```
   abyssctl <command path> needs root. Run it with sudo.
   ```

   Neither check reads or writes anything on the Host first. Neither refusal reaches the activity log: the
   log is written only by a run that passed both checks, and a non-root process can't write it.
7. The command runs and returns an error or nil. `cli.Run` returns `op.ExitCode(err)`, after the CLI has shown
   the error as [cli.md](cli.md) says.

Until [host.md](host.md) lands, `host setup` is registered with no flags and a body that returns "host setup is
not built yet" (exit 1), so the two checks guard a real command.

### `abyssctl version`

Prints the values from `buildinfo.Current()` to stdout and exits 0. It makes no network or Docker call and reads
no file.

```
abyssctl  0.1.0
commit    9b1c4e2d7f3a… (the full 40-character hash)
go        go1.27.1
platform  linux/amd64
caddy     2.N.P sha256:…
```

`version --json` prints the `version` document whose schema [cli.md](cli.md) owns, built from the same
`buildinfo.Current()`.

### `make build`

```
CGO_ENABLED=0 GOOS=linux GOARCH=<arch> go build -trimpath \
  -ldflags "-s -w -X $(BI).Version=$(VERSION) -X $(BI).Commit=$(COMMIT) $(LDFLAGS_EXTRA)" \
  -o dist/abyssctl-linux-<arch> ./cmd/abyssctl
```

for `amd64` and `arm64`, with `BI = github.com/DocCyblade/abyssctl/internal/buildinfo`. `VERSION` defaults to
`0.0.0-dev` (so a plain `make build` gives an unstamped binary) and `COMMIT` to `git rev-parse HEAD`.
`LDFLAGS_EXTRA` carries more `-X` values: the journey build sets `ActualRepo`, `CaddyRepo`, `RegistryURL`
and `PushoverURL` there ([testing.md](testing.md)). The tag workflow ([release.md](release.md)) calls `make build` with `VERSION`
set to the tag without its `v`, so a local build and a [Release](../../GLOSSARY.md) are built the same way.

### `make tools`, `make lint`, `make test`

- `make tools` installs the pinned dev tools into `./bin/` with `GOBIN=$(CURDIR)/bin go install <tool>@<version>`,
  with `GOTOOLCHAIN` set to the module's toolchain: `go install tool@version` ignores `go.mod`, and golangci-lint
  refuses a module whose Go version is newer than the one it was built with. The versions are Makefile variables (`GOLANGCI_LINT_VERSION` for golangci-lint v2, `GOTESTSUM_VERSION`).
  [testing.md](testing.md) adds `crane`. Dev tools never enter `go.mod`.
- `make lint` runs `bin/golangci-lint run`.
- `make test` runs `bin/gotestsum -- -race ./...` with `CGO_ENABLED=1`, because the race detector needs cgo. Only
  tests use cgo; every build is `CGO_ENABLED=0`. In CI it also writes a JUnit report. The journeys sit behind the
  `journey` build tag, so `make test` never runs them ([testing.md](testing.md)).
- Module tests use only portable standard-library calls, so `make lint` and `make test` run the same on a Linux
  CI runner and on a macOS dev machine.

### The import-rules test

`internal/archtest` holds tests and no code. `archtest_test.go` runs `go list -deps -json ./...` and fails when:

- `go.mod` requires a module outside the allowed list: `github.com/spf13/cobra`, `modernc.org/sqlite`,
  `golang.org/x/term`, and their own requirements;
- a package other than `internal/cli` and `internal/cli/gendocs` (the reference generator `make docs` runs,
  [docs.md](docs.md)) imports Cobra or `cobra/doc`;
- a package other than `internal/backup` imports `modernc.org/sqlite`
  ([ADR 0004](../adr/0004-restore-regroups-offline.md));
- `internal/buildinfo`, `internal/hostfs` or `internal/op` imports anything outside the standard library, or
  `hostfs` or `op` imports another `internal/` package;
- any package other than `cmd/abyssctl` and `internal/cli/gendocs` imports `internal/cli`;
- `release` (the release tooling, [release.md](release.md)) imports anything other than the standard library and
  `internal/buildinfo`, or any package imports `release`.

A module spec that adds a call-graph rule adds it here.

`lint_test.go` checks the lint's banned calls: it copies `.golangci.yml` and the fixture module
`internal/archtest/testdata/forbid/` into `t.TempDir()`, runs `bin/golangci-lint run` there, and expects a
`forbidigo` finding for each banned call in a package that may not make it, and none for the same calls in the
packages that may. A missing `bin/golangci-lint` fails the test with the hint `make tools`.

### CI (`.github/workflows/ci.yml`)

`on: push` for branches only (`branches: ["**"]`, so a tag push doesn't run it on its own), `pull_request`, and
`workflow_call`, so the tag workflow can call it. One job on `ubuntu-24.04`: check out, set up Go from `go.mod`
(`actions/setup-go` with `go-version-file`), `make tools`, `make lint`, `make test`, `make docs-check` (from [docs.md](docs.md)), `make build`, a check that `file dist/*`
reports every binary as statically linked, and upload the JUnit report. Third-party actions are pinned to full commit SHAs. The runner's user is not root, which the smoke
tests below rely on. [testing.md](testing.md) adds the journey jobs; [release.md](release.md) owns the tag
workflow.

## 4. Files and formats it owns

```
go.mod  go.sum
Makefile                        tools, lint, test, build (testing and release add targets; docs, docs-check
                                from docs.md, ticketed here)
.golangci.yml
.gitignore                      bin/, dist/ (testing.md adds its cache)
.github/workflows/ci.yml
cmd/abyssctl/main.go            umask, then cli.Main
cmd/abyssctl/main_test.go       the smoke tests
internal/buildinfo/             buildinfo.go, version.go, pins.json, testdata/plain_versions.txt, tests
internal/hostfs/                paths.go, stamp.go, write.go, tests
internal/op/                    event.go, errors.go, summary.go, clock.go, tests
internal/cli/                   root.go (tree, checks, exit code), version.go, host.go (placeholder)
internal/archtest/              archtest_test.go, lint_test.go, testdata/forbid/ (the lint fixture module)
from docs.md, ticketed here:    internal/cli/gendocs/, internal/docscheck/, LICENSE, the make docs-check step in
                                ci.yml, H2 skeletons of docs/guides/*.md and CONTRIBUTING.md
```

- **`go.mod`:** module `github.com/DocCyblade/abyssctl`; a `go 1.27` line and a `toolchain go1.27.N` line at the
  latest patch release when the repo is created. Moving the toolchain is a commit of its own. Foundation adds the
  first dependency, Cobra. `golang.org/x/term` arrives with [cli.md](cli.md)'s prompts and `modernc.org/sqlite`
  with [backup.md](backup.md).
- **`internal/buildinfo/pins.json`**, embedded with `go:embed`:

  ```json
  {
    "caddy": { "version": "2.N.P", "digest": "sha256:<64 hex>" },
    "docker_engine_min": "25.0.5",
    "docker_compose_min": "2.24"
  }
  ```

  All four values (three top-level keys) are required and unknown keys are an error. The Caddy pin is a stable Caddy release (its version passes `PlainVersion`) and the
  digest of its multi-arch index under `CaddyRepo`. Moving a pin is a commit of its own and ships in the next
  Release. [proxy.md](proxy.md) uses the Caddy pin; [docker-and-locks.md](docker-and-locks.md) and
  [host.md](host.md) use the minimums. The journeys' Actual versions live in `journey/testdata/pins.json`, and the
  test image cache key hashes both files ([testing.md](testing.md)).
- **The `-X` variable names** (`Version`, `Commit`, `ActualRepo`, `CaddyRepo`, `RegistryURL`, `PushoverURL`).
  [testing.md](testing.md) sets them all for the journey builds. A Release sets only `Version` and `Commit`, and
  [release.md](release.md)'s `release-binary-check` proves the other four keep their release defaults. Everything
  else reads them only through `buildinfo`.
- **The file-name timestamp** (`Stamp`): `YYYYMMDDTHHMMSSZ`, UTC.
- **The `version` text output** (above). Its `--json` schema is [cli.md](cli.md)'s.
- **`.golangci.yml`**, golangci-lint v2 format:

  ```yaml
  version: "2"
  linters:
    default: none
    enable: [govet, staticcheck, errcheck, ineffassign, unused, forbidigo]
    settings:
      forbidigo:
        analyze-types: true
        forbid:
          - pattern: ^time\.(Now|Since|Until)$
            msg: "take the time from the op.Clock you were given (rule: clock)"
          - pattern: ^os\.(WriteFile|Create)$
            msg: "write through hostfs.WriteFile or hostfs.Create (rule: write)"
          - pattern: ^(fmt\.Print(f|ln)?|os\.(Stdout|Stderr)|print|println)$
            msg: "emit an op.Event; only internal/cli prints (rule: print)"
    exclusions:
      rules:
        - path: ^internal/op/
          linters: [forbidigo]
          text: "rule: clock"
        - path: ^internal/hostfs/
          linters: [forbidigo]
          text: "rule: write"
        - path: ^internal/cli/gendocs/
          linters: [forbidigo]
          text: "rule: write"
        - path: ^internal/cli/
          linters: [forbidigo]
          text: "rule: print"
        - path: (_test\.go$|^journey/|^release/)
          linters: [forbidigo]
  formatters:
    enable: [gofumpt]
  ```

  Paths are relative to the config file (golangci-lint v2's default `relative-path-mode: cfg`). The print rule bans any use of `os.Stdout` and
  `os.Stderr`, so `fmt.Fprint*` to them is caught too. `internal/cli/gendocs` writes the reference pages in the
  repo, not on a Host. Tests, the journey harness (`journey/`) and the release tooling (`release/`) never run as
  the tool on a Host, so the three rules skip them. Adding a linter is a commit with its reason in the message. A `//nolint` needs a reason on the same line.
- **The smoke tests** (`cmd/abyssctl/main_test.go`) build the binary for the native `GOOS` and `GOARCH` into
  `t.TempDir()` twice, stamped and unstamped, and run it with `os/exec` as the unprivileged test user. They check the built binary from outside;
  the CLI's panels, prompts and `--json` are checked by the CLI tests through `clitest.Env` ([cli.md](cli.md)), and
  once more on the real binary by journey 8's golden files ([testing.md](testing.md)).
  The root-related smoke tests skip when the test runs as root; journey 8 covers the same refusals as root on a
  real Host.

## 5. Depends on

Nothing. Every other spec depends on this one.

## 6. Decisions

- One static Go binary, `CGO_ENABLED=0`, for linux/amd64 and linux/arm64, with every template and pin compiled
  in: [ADR 0003](../adr/0003-single-static-go-binary.md).
- **Cobra** is v1's first dependency. Reason: it gives the nested verb groups (`host setup`, `cert install`,
  `backup list`), flag parsing and help that about 25 commands need, and `cobra/doc` generates the reference
  pages that CI checks ([docs.md](docs.md)). Only `internal/cli` imports it, so no module depends on the CLI
  framework. v1 allows two more dependencies, each with its reason in its owning spec: `golang.org/x/term`
  ([cli.md](cli.md)) and `modernc.org/sqlite` ([backup.md](backup.md)). Each new dependency needs a written
  reason in its owning spec's Decisions section and an entry in the import-rules test.
- `cmd/abyssctl/main.go` stays tiny and the CLI lives in `internal/cli`, so the reference generator can import
  the command tree.
- Every command that touches the Host needs root. Only `version` and help are exempt, because they read only
  what is compiled in. Cobra's `completion` command is off in v1, so it can't become a third exemption.
- An unstamped build (`0.0.0-dev`) refuses to act on a Host, so a file stamped `TOOL_VERSION=0.0.0-dev` can never
  be written: an unstamped build never writes `TOOL_VERSION`. Journey binaries are always stamped.
- Versions are SemVer without a leading `v` inside the tool (`TOOL_VERSION`, `buildinfo.Version`). Release tags
  are `vX.Y.Z`; the first public Release is `v0.1.0`, and `v1.0.0` marks the v1 capabilities complete.
- `umask 077`, and temp file → fsync → chmod → rename for every write, so no file is ever briefly readable with
  a looser mode. This is the base of the [Secret](../../GLOSSARY.md) rules.
- Image names, the registry URL and the Pushover URL are build-time variables with no runtime override: production has no hidden
  setting, and journeys still pull from a local registry.
- One paths value, built only by the CLI, so module tests run against a temp folder without privileges.
- Modules emit events and return typed errors and `Summary` values; only the CLI prints, prompts and picks exit
  codes. Exit codes: 0 done, 1 failed, 2 refused, 3 rolled back, 4 needs the Operator.
- Plain `go build` through `make build`; no GoReleaser.

## 7. Later

None of its own. The new tool version notice and the [Tool update](../../GLOSSARY.md) command
([ROADMAP.md](../../ROADMAP.md), Next) build on `buildinfo.Version` and `CompareVersions`.

## 8. Docs it changes

- `CONTRIBUTING.md`: the "Build and test" section (`make tools`, `make lint`, `make test`, `make build`, stamping
  `VERSION` to try a build on a test Host, the toolchain pin, the dependency rule). [docs.md](docs.md) owns the
  rest of the file; until it lands, this spec creates the file with only that section.
- Reference: the `abyssctl version` page, generated once `make docs` exists ([docs.md](docs.md)).

## 9. Acceptance list

Module tests sit beside their package; "smoke" tests are in `cmd/abyssctl/main_test.go`.

1. A stamped binary's `version` prints the stamped version and commit, the Go version, the platform and the
   Caddy pin, and exits 0. → `TestSmoke_VersionPrintsStampedValues`
2. `version --json` prints one document matching the `version` schema in [cli.md](cli.md). →
   `TestSmoke_VersionJSON`
3. `version`, `--help`, `help` and a bare `abyssctl` exit 0 as a non-root user, stamped or not. →
   `TestSmoke_ExemptCommandsNeedNoRoot`
4. A stamped binary refuses `host setup` as a non-root user with exit 2 and "needs root. Run it with sudo." on
   stderr; run in an empty temp working folder, the folder stays empty. → `TestSmoke_NotRootIsRefused`; as
   root on a Host: journey 8 (checked by cli.md's journey slice)
5. An unstamped binary refuses `host setup` with exit 2 and the unstamped-build message naming its `Version`. →
   `TestSmoke_UnstampedIsRefused`
6. An unknown command or flag exits 2, and `completion` is an unknown command. → `TestSmoke_BadArgumentsExit2`
7. `CheckStamped` refuses `0.0.0-dev` and invalid SemVer and accepts `0.1.0` and `0.2.0-dev.3`. →
   `TestCheckStamped`
8. `CompareVersions` follows SemVer 2.0 precedence: a pre-release sorts before its release, build metadata is
   ignored, a leading `v` or any other invalid version is an error. → `TestCompareVersions` (table)
9. The embedded `pins.json` parses, has all four values, a plain (`PlainVersion`) Caddy version and a `sha256:` digest; a
   file with a missing or unknown key is rejected. → `TestCompiledPins_Valid`, `TestParsePins_Rejects`
10. `System().WithDataRoot("/srv/abyssctl")` gives every path in the overview's Host layout table except Docker's
    apt files, path for path, the four lock files (`HostLock`, `HealthLock`, `InstanceLock`, `CreatingLock`)
    included. → `TestPaths_MatchHostLayout`
11. `WithDataRoot` moves every Data root path and no fixed path, and rejects a relative or unclean folder. →
    `TestWithDataRoot`
12. `Under(prefix)` puts every fixed path, `/etc`, `/usr` and `/var` ones included, under the prefix. →
    `TestUnder_EveryPathUnderPrefix`
13. A Data root method on a `Paths` without a Data root panics. → `TestPaths_NoDataRootPanics`
14. `Stamp` gives UTC `YYYYMMDDTHHMMSSZ` from any zone and `ParseStamp` reverses it; text order is time order. →
    `TestStamp`
15. `WriteFile` and `Create` → `Commit` leave the file with exactly the requested mode whatever the umask, replace
    an existing file in one step, and leave no `.partial` file behind. → `TestWriteFile_ExactMode`,
    `TestWriteFile_Replaces`, `TestCreate_CommitAndAbort`
16. The temp file sits in the target's folder, is named `<base>.<8 hex>.partial`, and `Pending.Path()` returns
    it until `Commit`. → `TestCreate_TempName`
17. A failed write leaves the old file untouched and no temp file; a write into a missing folder fails and creates
    nothing. → `TestWriteFile_ErrorLeavesOldFile`, `TestWriteFile_MissingFolderFails`
18. `EnsureDir` creates a missing folder with exactly `perm` and fixes the mode of an existing one. →
    `TestEnsureDir`
19. `ExitCode` maps nil → 0, each class → 2, 3 and 4 through any wrapping (outermost `*Error` wins), and any
    other error → 1; `Hint` returns the outermost hint; `NeedsOperator` refuses an empty hint (panics). →
    `TestExitCode` (table), `TestHint`, `TestNeedsOperator_RequiresHint`
20. An `Emitter` stamps each event from its `Clock`, keeps field order and delivers events in order; a `Recorder`
    panics on a step or key that isn't snake_case. → `TestEmitter_StampsAndOrders`, `TestRecorder_RejectsBadKey`
21. `Mask` shows only the last four characters, and nothing of a value shorter than eight. → `TestMask`
22. `ManualClock` returns the set time and moves only on `Set` and `Advance`. → `TestManualClock`
23. The import rules hold: only the allowed dependencies, Cobra only in `internal/cli` and its `gendocs`, SQLite only in
    `internal/backup`, the shared packages standard-library only, `internal/cli` imported only by `cmd/abyssctl`.
    → `TestImportRules`
24. The binary sets `umask 077`: everything the tool creates on a Host has the modes in the Host layout table. →
    journey 1 (Bootstrap) (checked by host.md's journey slice)
25. `make build` produces statically linked `dist/abyssctl-linux-amd64` and `dist/abyssctl-linux-arm64`. → the CI
    `make build` step and the `file dist/*` check that follows it
26. `make lint` is clean and `make test` passes in the CI workflow on every push and pull request. → the CI run
27. `make lint` reports `time.Now`, `os.WriteFile`, `os.Create`, `fmt.Println` and a write to `os.Stdout` in a
    module package, and none of them in the package each is allowed in (`internal/op`, `internal/hostfs`,
    `internal/cli`) or in a `_test.go` file. → `TestLintForbids` in `internal/archtest`
28. `PlainVersion` gives the expected answer for every line of `testdata/plain_versions.txt`, which covers a
    leading `v`, a leading zero, two or four parts, a pre-release and a build part. → `TestPlainVersion` (table)
29. `CONTRIBUTING.md` has the "Build and test" section, and the `version` reference page is generated. → (review)
