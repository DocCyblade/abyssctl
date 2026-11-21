# Spec: docker-and-locks

Build step 2 of 16. The [overview](README.md) has the module map, the call graph, the Host layout and the Docker
names table. Shared packages (`internal/hostfs`, `internal/op`, `internal/buildinfo`) are in
[foundation.md](foundation.md). Capitalised terms are defined in [GLOSSARY.md](../../GLOSSARY.md).

## 1. Purpose

Build the two leaf adapters that every changing module stands on:

- **Docker** (`internal/docker`): the only code that runs `docker` or `docker compose`. Each method is one or two
  CLI invocations; the adapter parses their `--format json` output into Go values, classifies failures into a
  few sentinels, and returns every image name in one normalised form. It also holds the tool's fixed Docker
  names (`internal/docker/names`), a fake for every other module's tests (`internal/docker/dockerfake`) and a
  contract suite that both the fake and the real CLI pass (`internal/docker/dockertest`).
- **Locks** (`internal/locks`): the Host lock and the Instance locks, on `flock`, with a bounded wait for
  commands run by hand and a try-lock for scheduled runs and the [Health check](../../GLOSSARY.md).

Neither module knows what an [Instance](../../GLOSSARY.md), a [Backup](../../GLOSSARY.md) or the
[Proxy](../../GLOSSARY.md) is. They take names, references and paths, and do what they are told. When this spec
is done, later modules can start, stop and inspect Compose projects, containers, images and networks, and
serialise their work, all testable without Docker and without root.

## 2. Interface

Signature sketches, not final code.

### `internal/docker`: the Docker adapter

```go
// Client is the whole Docker surface the tool uses. *CLI is the production adapter, *dockerfake.Fake the
// other. Both are safe for concurrent use. Modules take a Client in their constructor.
type Client interface {
    // Engine and Compose.
    Versions(ctx context.Context) (Versions, error)

    // Compose projects. The file is always <Dir>/compose.yaml.
    ComposeUp(ctx context.Context, p Project, o UpOptions) error
    ComposeStop(ctx context.Context, p Project) error
    ComposeDown(ctx context.Context, p Project) error
    ComposePs(ctx context.Context, p Project) ([]PsEntry, error)

    // Containers.
    Container(ctx context.Context, name string) (Container, bool, error) // false: no such container
    ListContainers(ctx context.Context, label string) ([]Container, error) // "key" or "key=value"; "" lists every container; running or not
    RemoveContainer(ctx context.Context, name string) error               // force-removes one container

    // Images.
    Pull(ctx context.Context, ref string) error
    Image(ctx context.Context, ref string) (Image, bool, error) // false: not present locally
    ListImages(ctx context.Context) ([]Image, error)            // every local image, one entry per image ID
    RemoveImage(ctx context.Context, ref string) error          // never forced

    // Networks.
    Network(ctx context.Context, name string) (Network, bool, error)
    CreateNetwork(ctx context.Context, name string, labels map[string]string) error

    // Running things in containers.
    Exec(ctx context.Context, container string, cmd []string) (Result, error)       // output captured
    ExecAttached(ctx context.Context, container string, cmd []string, io IO) error  // passthrough
    Logs(ctx context.Context, container string, o LogOptions, io IO) error          // passthrough or to a file
    Run(ctx context.Context, spec RunSpec) (Result, error)                          // one-shot, always --rm
}

func New() *CLI // never fails; a missing docker binary shows up as ErrNotInstalled on the first call

type Versions struct{ Engine, Compose string } // as Docker reports them, a leading "v" removed

// CheckVersions returns nil, or an error wrapping ErrTooOld that names the part, the version found and the
// minimum. The minimums are pins.DockerEngineMin and pins.DockerComposeMin.
func CheckVersions(v Versions, pins buildinfo.Pins) error

type Project struct{ Name, Dir string }

type UpOptions struct {
    Wait          bool          // --wait: running, and healthy if the image has a healthcheck
    WaitTimeout   time.Duration // with Wait; must be > 0 (callers pass 5 min); rounded up to whole seconds
    ForceRecreate bool
}

type PsEntry struct {
    Name, Service, State, Health string // State and Health as in Container
    ExitCode                     int
}

type Container struct {
    ID, Name    string
    Labels      map[string]string
    Image       string            // the reference it was created from, normalised
    ImageID     string
    State       string            // created, running, paused, restarting, removing, exited, dead
    Health      string            // "", starting, healthy, unhealthy ("" when it has no healthcheck)
    ExitCode    int
    StartedAt   time.Time
    FinishedAt  time.Time
    Networks    map[string]Attachment // by network name
    Published   []Port                // Host ports it publishes
    User        string
    CapAdd      []string
    CapDrop     []string
    ReadOnly    bool                  // read-only root filesystem
    SecurityOpt []string
}

type Attachment struct {
    IP      string
    Aliases []string
}

type Port struct {
    HostIP        string
    HostPort      int
    ContainerPort int
    Proto         string // tcp, udp
}

type Image struct {
    ID          string
    RepoTags    []string // normalised, e.g. "docker.io/library/caddy:2.10.2"
    RepoDigests []string // normalised, e.g. "docker.io/library/caddy@sha256:…"
    Size        int64    // bytes; shared layers are counted in every image that has them
    Created     time.Time
    Labels      map[string]string // the image config's labels, e.g. org.opencontainers.image.version
}

// DigestFor returns the digest recorded for repo (compared after normalising both sides), if any.
func (i Image) DigestFor(repo string) (string, bool)

type Network struct {
    ID, Name, Driver string
    Subnets          []string // CIDRs from the network's IPAM config, in Docker's order
    Labels           map[string]string
}

type Result struct {
    Stdout, Stderr []byte // each capped at 1 MiB; the cap is noted in Truncated
    Code           int    // the command's exit status inside the container
    Truncated      bool
}

type IO struct {
    Stdin          io.Reader // nil: no stdin
    Stdout, Stderr io.Writer // nil: discarded
    TTY            bool      // allocate a terminal (-t); the caller has checked Stdin is one
}

type LogOptions struct {
    Follow bool
    Tail   int // lines from the end; 0 means all
}

type RunSpec struct {
    Image  string  // a full reference, normally repo@digest; must already be local
    Cmd    []string
    Mounts []Mount // bind mounts
}

type Mount struct {
    Source, Target string
    ReadOnly       bool
}
```

**Image references and Docker Hub normalisation.** The tool names every image with its registry host
(`buildinfo.ActualRepo` is `docker.io/actualbudget/actual-server`). Docker shows a Docker Hub image without
`docker.io/`, and an official image without `library/` as well (`caddy`), in `RepoTags`, `RepoDigests` and a
container's image. The adapter is the one place that reconciles the two:

```go
type Ref struct {
    Repo   string // normalised: registry host first, e.g. "docker.io/library/caddy"
    Tag    string // "" if none
    Digest string // "sha256:<64 hex>", or "" if none
}

func ParseRef(s string) (Ref, error)   // an error for anything Docker would not accept as a reference
func NormalizeRepo(repo string) string // ParseRef(repo).Repo, for a repo with no tag or digest
func SameRepo(a, b string) bool        // equal after normalising both
func (r Ref) String() string           // Repo[:Tag][@Digest]
```

- **The rule** (Docker's own): split off `@digest`, then a `:tag` that follows the last `/`. The first path
  element is a registry host when it contains `.` or `:` or is `localhost`; otherwise the repo gets `docker.io/`
  in front. A host of `index.docker.io` becomes `docker.io`. On `docker.io`, a repo with one path element gets
  `library/` in front. Nothing else changes: `127.0.0.1:5000/actualbudget/actual-server` stays as it is.
- **Out: always normalised.** Every image name in a `Container`, `Image` or error the adapter returns is
  normalised, so callers compare names with `==` or `SameRepo` and never see Docker's short forms.
- **In: passed through.** Every reference a caller hands in is passed to `docker` as given; Docker accepts the
  full form. [versions.md](versions.md) relies on this for `image prune`; [render.md](render.md) writes the
  full form into `compose.yaml`.

**Errors.** Every failed `docker` invocation returns a `*CommandError`. `errors.Is` matches it against a sentinel
when the failure is recognised:

```go
var (
    ErrNotInstalled = errors.New("docker is not installed")
    ErrNoCompose    = errors.New("docker compose v2 is not installed")
    ErrDaemon       = errors.New("docker daemon is not reachable")
    ErrTooOld       = errors.New("docker is older than the minimum") // only from CheckVersions
    ErrNotFound     = errors.New("no such object")       // container, image, network, or not pullable
    ErrConflict     = errors.New("conflict")             // name taken, image in use
    ErrNotRunning   = errors.New("container is not running")
    ErrNotHealthy   = errors.New("container did not become healthy") // ComposeUp with Wait
)

type CommandError struct {
    Args   []string // the arguments after "docker"; never a Secret (see invariants)
    Exit   int      // docker's own exit status
    Stderr string   // the last 4 KiB of stderr
    Kind   error    // one of the sentinels, or nil
}
func (e *CommandError) Error() string  // "docker <args>: exit <n>: <last stderr line>"
func (e *CommandError) Unwrap() error  // Kind

// ExitError is ExecAttached's result when the command inside the container exits non-zero.
type ExitError struct{ Code int }
```

Invariants:

- `Container`, `Image` and `Network` return `(zero, false, nil)` for a missing object. Every other method
  returns an error wrapping `ErrNotFound` for a missing object.
- A missing `<Dir>/compose.yaml` fails every `Compose…` method with an error wrapping `fs.ErrNotExist`, before
  `docker` runs. `ComposeUp` with `Wait` and a `WaitTimeout` of zero or less fails the same way, with a plain
  error, before `docker` runs.
- `ComposeUp` and `Run` never pull: an image that is not local fails with `ErrNotFound`. Only `Pull` reaches a
  registry, so `start` and `restart` reach a registry only to re-pull a pinned image missing locally, and
  Restore's swap and Rollback never need the network.
- `ComposeDown` never removes volumes or images. Instance data is a bind mount and the network is external, so a
  down removes only the project's containers.
- `Exec` and `Run` return a `Result` with no error when the command ran, whatever its exit code; the caller
  judges `Code`. They return an error only when Docker could not run it: container missing (`ErrNotFound`),
  not running (`ErrNotRunning`), or exit 125, 126 or 127. Docker gives 126 and 127 both for its own failures
  and for a command inside the container that exits with them; the adapter can't tell these apart and reports
  every 126 or 127 as a Docker failure. This is accepted: no command the tool execs uses those codes.
- `ExecAttached` returns `*ExitError{Code}` for a non-zero exit of the command, so the CLI can exit with it.
- No argument passed to `docker` is ever a [Secret](../../GLOSSARY.md): arguments show in `ps` and in
  `CommandError`. A container gets what it needs through its `compose.yaml` and mounts.
- The adapter never prints and never emits events. A successful call's progress output is discarded; a failed
  call's stderr tail is in its error. `ExecAttached` and `Logs` pass output through unfiltered, and nothing
  else sees it.
- The adapter keeps no state between calls and caches nothing.

### `internal/docker/names`: the tool's Docker names

```go
const (
    Prefix        = "abyssctl-"            // every Compose project and container the tool owns
    ProxyProject  = "abyssctl-proxy"       // the Proxy's Compose project and container
    Network       = "abyssctl"             // the private network
    InstanceLabel = "io.abyssctl.instance" // value: the Instance name
)

func InstanceProject(name string) string // "abyssctl-<name>": project, container and network alias
```

This is the one Go spelling of the overview's Docker names table. The package imports nothing, so the
[Renderer](render.md), which writes these names into generated files, imports it without depending on the
adapter. Every other module takes the names from here, never from a string literal.

### `internal/docker/dockerfake`: the fake Docker

```go
func New(pins buildinfo.Pins) *Fake // empty: no containers, images or networks; Versions = the pinned minimums

// Seeding.
func (f *Fake) SetVersions(v docker.Versions)
func (f *Fake) AddImage(img docker.Image)               // local; names normalised on the way in
func (f *Fake) AddRemote(ref string, img docker.Image)  // Pull(ref) makes img local
func (f *Fake) AddContainer(c docker.Container)         // e.g. an Orphan, or another tool's container
func (f *Fake) AddNetwork(n docker.Network)
func (f *Fake) SetImageHealth(ref, health string)       // what a ComposeUp --wait of ref reaches
func (f *Fake) SetHealth(container, health string)      // change a running container's health later
func (f *Fake) SetExec(container string, cmd []string, r docker.Result)
func (f *Fake) SetRun(image string, cmd []string, r docker.Result)
func (f *Fake) SetLogs(container, text string)

// Failure injection: the next call matching op (e.g. "compose.up", "pull", "exec") returns err, wrapped in a
// *docker.CommandError when err is a sentinel. FailWhen matches on the whole Call and fires every time.
func (f *Fake) Fail(op string, err error)
func (f *Fake) FailWhen(match func(Call) bool, err error)

// Inspection.
func (f *Fake) Calls() []Call                     // every call, reads included, in order
func (f *Fake) ComposeFile(project string) []byte // compose.yaml as read at the project's last ComposeUp

type Call struct {
    Op   string   // compose.up, compose.stop, compose.down, compose.ps, container.inspect, container.ls,
                  // container.rm, pull, image.inspect, image.ls, image.rm, network.inspect,
                  // network.create, exec, exec.attached, logs, run, versions
    Name string   // the project, container, reference or network named
    Args []string // the rest: the command, "wait", "force-recreate", the label filter
}
```

**The fake's model.** It keeps containers, images and networks in memory and follows the same invariants as the
CLI adapter (the contract suite checks both).

- A Compose project is one container named after the project. `ComposeUp` reads `<Dir>/compose.yaml` from disk
  (missing → `fs.ErrNotExist`), records it, and takes the container's image from the file's first `image:`
  line. It does not otherwise parse the file. A project named `names.InstanceProject(x)` gets the label
  `names.InstanceLabel=x`, and every project gets `com.docker.compose.project=<name>`, as the generated files
  give real containers ([render.md](render.md) tests the files themselves).
- `ComposeUp` of a missing image fails `ErrNotFound`. An unchanged running project is left alone; a changed
  file or `ForceRecreate` gives a new container ID. With `Wait`, the container reaches the image's
  `SetImageHealth` value (default `healthy`); anything but `healthy` fails `ErrNotHealthy` and leaves the
  container running in that state, as Compose does.
- `ComposeStop` leaves the container `exited` with code 0; `ComposeDown` removes it. Both succeed on a project
  with no container.
- `RemoveImage` fails `ErrConflict` only when removing the reference would delete an image a container uses,
  running or not; otherwise it removes that reference (and the repo's digest references with its last tag), and
  the image goes when its last reference does. `CreateNetwork` of an
  existing name fails `ErrConflict` and otherwise assigns the subnet `172.30.0.0/16`.
- `Exec` on a container that is not running fails `ErrNotRunning`. An `Exec` or `Run` with no `SetExec` /
  `SetRun` entry returns code 0 and no output.

### `internal/docker/dockertest`: the contract suite

```go
type Env struct {
    Healthy   string // a local image with a healthcheck that passes
    Unhealthy string // a local image whose healthcheck never passes
    Pullable  string   // a reference not yet local that Pull can fetch
    Exec      []string // a command both images can run that exits 0 (the fake Actual image's stub `node`)
    Dir       string   // a writable temp folder for compose files
}

func Contract(t *testing.T, c docker.Client, env Env)
```

One suite of behaviour both adapters must share (see the acceptance list). Module tests run it against the fake;
the journey harness ([testing.md](testing.md)) runs it against the real CLI with the fake Actual image's tags.
Every object the suite creates (projects, containers, networks) has a unique name of its own, never the network
`abyssctl` or a real Instance or Proxy project, and is removed in `t.Cleanup`, so the suite can run on a Host
that also runs Abyss.

### `internal/locks`: the Host and Instance locks

```go
const DefaultWait = 30 * time.Second // what the CLI passes for commands run by hand

// One Locker per process. purpose is what this run is doing ("upgrade kids", "backup --all --scheduled"),
// written into each lock file it holds; it is limited to [a-z0-9 .-] and New panics otherwise.
func New(p hostfs.Paths, clock op.Clock, emit op.Emitter, purpose string) *Locker

// Acquire waits up to wait for the lock at path. TryAcquire never waits.
func (l *Locker) Acquire(ctx context.Context, path string, wait time.Duration) (*Lock, error)
func (l *Locker) TryAcquire(path string) (*Lock, error)

func (k *Lock) Release() error // safe to call twice

type Holder struct {
    PID     int
    Purpose string
    Since   time.Time // zero when the lock file's line could not be read
}

var (
    ErrBusy      = errors.New("lock is held")               // TryAcquire found it held
    ErrTimeout   = errors.New("timed out waiting for lock") // Acquire gave up
    ErrLockOrder = errors.New("lock order violated")        // a bug in the caller, never a runtime condition
)

// BusyError is what TryAcquire and a timed-out Acquire return. It matches ErrBusy, and also ErrTimeout when
// Acquire waited.
type BusyError struct {
    Path   string
    Holder Holder
    Waited time.Duration
}
```

Callers pass `p.HostLock()` (`state/host.lock`), `p.HealthLock()` (`state/health.lock`),
`p.InstanceLock(name)` (`instances/<name>/.lock`) or
`p.CreatingLock(name)` (`instances/.<name>.creating/.lock`, the lock of an Instance being built, which
[store.md](store.md)'s `Commit` renames to `InstanceLock(name)`) from [foundation.md](foundation.md)'s
`hostfs.Paths`. No other path is a lock: `Acquire` or `TryAcquire` of any other path fails with `ErrLockOrder`
without touching the file. The Locker keys a held Instance lock by Instance name, not by path:
`CreatingLock(name)` is name's Instance lock for the order and "already held" rules, before and after the
rename, so a later `Acquire(InstanceLock(name))` in the same process fails with `ErrLockOrder` ("already
held") instead of deadlocking on its own `flock`.

Invariants:

- **A lock is an exclusive `flock` on an open lock file.** It lasts until `Release` or until the process ends,
  however it ends, so a held lock always means a live operation and a crash never leaves a stale lock.
- **Order.** Within one Locker: while holding any lock, `Acquire` of the Host lock fails at once with
  `ErrLockOrder`, and so does `Acquire` or `TryAcquire` of the health lock; while holding the health lock,
  `Acquire` of any other lock fails with `ErrLockOrder`; while holding an Instance lock, `Acquire` of another
  Instance lock fails with `ErrLockOrder` unless its name sorts after every Instance lock held; `Acquire` or
  `TryAcquire` of a path already held fails with `ErrLockOrder`. `TryAcquire` never waits, so it is otherwise
  allowed at any time, an Instance lock under the health lock included. Together with the CLI's lock table
  ([cli.md](cli.md)), this leaves no wait cycle between two `abyssctl` processes.
- `Acquire` and `TryAcquire` create a missing lock file (mode 0600) but never create folders: a missing folder
  fails with an error wrapping `fs.ErrNotExist`.
- **A lock on a file that was removed is not a lock.** After `flock` succeeds, the Locker checks that `path`
  still names the file it locked. If `path` is gone, it releases and returns an error wrapping
  `fs.ErrNotExist`; if `path` now names a different file, it starts over on the new one within the same wait.
- Lock files are opened close-on-exec, so no `docker` child or other child process inherits a lock.
- An `Acquire` cancelled through `ctx` (Ctrl-C) returns `ctx.Err()` and holds nothing.
- A timed-out `Acquire` is a plain error (exit 1, as every lock timeout is). `ErrBusy` from `TryAcquire` is
  information for the caller, who skips the Instance and says so.

## 3. Behaviour

### How the Docker adapter runs a call

1. **Find `docker`** on a fixed `PATH`
   (`/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin`), on every call. Not found →
   `ErrNotInstalled`. So one adapter value serves `host setup` before and after it installs Docker.
2. **Build the arguments.** Every call starts `docker --context default`, so the tool always manages the local
   daemon, whatever context root's Docker config names. Compose calls add `compose --ansi never
   --project-name <Name> --project-directory <Dir> --file <Dir>/compose.yaml`.
3. **Build the environment** from scratch: that `PATH`, `HOME` set to root's home directory from
   `/etc/passwd` (so a `docker login` done as root still applies to pulls), `LC_ALL=C` (stable error text) and
   `DOCKER_CLI_HINTS=false`. Nothing else passes through, so `DOCKER_HOST`, `DOCKER_CONTEXT`, `DOCKER_CONFIG` or
   any `COMPOSE_*` variable in the Operator's shell can't redirect or change a call.
4. **Start the process.** A **managed** call (every call except `ExecAttached` and `Logs` with an `IO` whose
   `Stdin` or `Follow` is set) runs in its own process group with no stdin, so a Ctrl-C at the terminal reaches
   only `abyssctl`, which cancels `ctx`; the adapter then sends SIGTERM to the child's process group and, 10 s later, SIGKILL to it, and returns
   an error wrapping `ctx.Err()`. An **attached** call stays in the terminal's process group with the given
   stdin, so Ctrl-C and the terminal reach the container as the Operator expects.
5. **Finish.** Exit 0: parse stdout, if the method reads anything. Non-zero: build a `*CommandError` with the
   stderr tail and classify it (below).

The adapter adds no timeout of its own. Callers bound calls with `ctx` (30 s for Caddy validate and reload) or
`UpOptions.WaitTimeout` (5 minutes for every wait on `/health`).

### The calls

| Method | `docker --context default` … | Parsing and notes |
|---|---|---|
| `Versions` | `version --format json`, then `compose version --format json` | `.Server.Version` and `.version`, leading `v` removed; daemon down → `ErrDaemon`; no compose plugin → `ErrNoCompose` |
| `ComposeUp` | `compose … up --detach --pull never [--wait --wait-timeout <seconds>] [--force-recreate]` | on a `--wait` failure, a `compose ps` decides `ErrNotHealthy` (below) |
| `ComposeStop` | `compose … stop` | Docker's default stop timeout |
| `ComposeDown` | `compose … down` | never `--volumes` or `--rmi` |
| `ComposePs` | `compose … ps --all --format json` | one JSON object per line; a single JSON array is also accepted |
| `Container` | `container inspect <name>` | |
| `ListContainers` | `container ls --all --quiet --no-trunc --filter label=<label>` (with `""`, no `--filter`), then one `container inspect <ids…>` | no IDs → no second call; an ID gone between the two calls is dropped |
| `RemoveContainer` | `container rm --force <name>` | for Orphans; Instances and the Proxy go through `ComposeDown` |
| `Pull` | `image pull --quiet <ref>` | the Host's platform only |
| `Image` | `image inspect <ref>` | |
| `ListImages` | `image ls --quiet --no-trunc`, then one `image inspect <unique ids…>` | |
| `RemoveImage` | `image rm <ref>` | last reference of an image a container uses → `ErrConflict` |
| `Network` | `network inspect <name>` | |
| `CreateNetwork` | `network create --driver bridge [--label k=v …] <name>` | Docker assigns the subnet; taken → `ErrConflict` |
| `Exec` | `container exec <name> <cmd…>` | output captured into `Result` |
| `ExecAttached` | `container exec [--interactive] [--tty] <name> <cmd…>` | `--interactive` when `Stdin` is set, `--tty` when `TTY` |
| `Logs` | `container logs [--follow] [--tail <n>] <name>` | written to `io.Stdout` / `io.Stderr` as Docker splits them |
| `Run` | `container run --rm --pull never --network none --cap-drop ALL --security-opt no-new-privileges [--mount type=bind,src=…,dst=…[,readonly]] <image> <cmd…>` | output captured into `Result` |

What the names, labels, images and commands mean belongs to the callers: the network to [host.md](host.md), the
Proxy project, Caddy's validate and reload and the Proxy upgrade pre-check to [proxy.md](proxy.md), Instance
projects to [lifecycle.md](lifecycle.md), images and digests to [versions.md](versions.md). The adapter only
carries them.

### `ComposeUp` with `Wait`

Compose 2.24 and later wait for the container to be running and, when it has a healthcheck, healthy, up to
`--wait-timeout`. When the `up` fails, the adapter runs `compose ps --all --format json` once: if the project's
container started and then failed to become healthy, the error's `Kind` is `ErrNotHealthy`. That is: its state is
`running` with health `starting` or `unhealthy` (which includes the wait running out), or its state is `exited`
or `restarting` and its `StartedAt` is set (it started, then stopped). A container that is `created` and never
started, or no container at all, is a failure to start, not to become healthy: the `up` failure is classified as
below (for example "port is already allocated"). The container is left as Compose left it; tearing it down or rolling back is the caller's choice.

### Classifying a failure

In order, from the exit status and stderr under `LC_ALL=C`:

1. "Cannot connect to the Docker daemon" or "permission denied" on the Docker socket → `ErrDaemon`.
2. "'compose' is not a docker command" or "unknown command" for `compose` → `ErrNoCompose`.
3. "is not running" from `exec` → `ErrNotRunning`.
4. "No such container", "No such image", "No such object", "network … not found", "pull access denied",
   "manifest unknown", or a missing image under `--pull never` → `ErrNotFound`.
5. "already exists", "is already in use", "conflict", "image is being used" → `ErrConflict`.
6. Anything else: `Kind` is nil and the caller reports the stderr tail.

Each match string is tested against stderr captured from real Docker (below), so a Docker release that rewords
one shows up as a failing test when the fixtures are refreshed.

### Minimum versions

The minimums are Docker Engine **24.0** and Compose **2.24**, read from `buildinfo.CompiledPins()`
(`docker_engine_min`, `docker_compose_min` in `internal/buildinfo/pins.json`, which
[foundation.md](foundation.md) owns). Both versions have `up --wait --wait-timeout` and a stable `--format json`,
and every flag in the table above. `CheckVersions` compares the leading numeric `MAJOR.MINOR[.PATCH]` of each
version and ignores anything after it (`26.1.5+dfsg1`, `2.24.6-1`). [Host](host.md) calls `Versions` and
`CheckVersions` before it uses an existing Docker, and refuses one that is too old. Raising a minimum is a commit
to `pins.json` with its reason and a fresh fixture set.

### Taking locks

The CLI takes the locks before it calls a module, from the per-verb lock table in [cli.md](cli.md). Module specs
state "caller holds X" as a precondition and never lock, with two exceptions: Health, which calls `TryAcquire`
on each Instance lock inside `Check` so it can skip a busy Instance, while the CLI holds the health lock
([health.md](health.md)); and Lifecycle's
`Create`, which `Acquire`s the staged Instance's `CreatingLock(name)` between Store's `Stage` and `Commit`, so the
new Instance is locked from the moment it is visible ([lifecycle.md](lifecycle.md), [store.md](store.md)).

- **Three kinds of lock.** The Instance lock is held by any command that changes that Instance, Backups
  included. The Host lock guards the Proxy's files, `create`, `remove`, `cert` changes, `proxy upgrade`, `image prune` and `host setup`.
  The health lock is held only by `health check` and `health clear`, so no two of them write `health.json` or
  send an Alert at once. It is taken while holding nothing, and the only lock taken under it is an Instance
  lock by `TryAcquire`, inside the Health check.
  Read-only commands and `--dry-run` take no lock.
- **Order: the Host lock first, then Instance locks.** A command that needs both takes the Host lock, then the
  Instance lock; `start` and `restart` release the Host lock right after the site step and keep the Instance
  lock to the end ([lifecycle.md](lifecycle.md)). `host setup` and `image prune` take the Host lock, then every Instance lock in name order. No process ever
  waits for the Host lock while holding an Instance lock, and only `host setup` and `image prune` hold more than
  one Instance lock, taken in name order.
  The Locker enforces this inside the process (`ErrLockOrder`).
- **By hand vs. scheduled.** Commands run by hand use `Acquire` with `DefaultWait`. Timer runs use
  `TryAcquire`: `backup --all --scheduled` skips a busy Instance and logs it, `health check --scheduled` skips the
  whole run when the health lock is busy, and the Health check skips a busy Instance for that run.
  `backup --all` (by hand or scheduled) and the Health check take and release each
  Instance lock in turn, so they never hold two at once.
- **An Instance removed while waiting.** A waiter whose Instance is removed gets `fs.ErrNotExist` (step 5
  below). The CLI shows it as "no such Instance"; [cli.md](cli.md) owns the wording.

### How a lock is taken

1. Check the path and the order rule. A violation returns `ErrLockOrder` before touching the file.
2. Open the lock file read-write, creating it with mode 0600 if missing, close-on-exec.
3. `flock(LOCK_EX|LOCK_NB)`. If it succeeds, go to 5.
4. Held. `TryAcquire`: read the holder line, close, return a `*BusyError` (`ErrBusy`). `Acquire`: read the
   holder line, emit one `lock_wait` event (`path`, `held_by` = the holder's purpose, `pid`), then retry every
   250 ms until the lock is taken (go to 5), `ctx` is done (close, return `ctx.Err()`) or `wait` has passed
   (close, return a `*BusyError` matching `ErrTimeout`). The wait is measured with a context deadline, so tests
   pass short waits.
5. Check that `path` still names the locked file (same device and inode). Gone: unlock, close, return
   `fs.ErrNotExist`. Replaced: unlock, close, go to 2.
6. Write the holder line in place: truncate, write, no rename (the lock is on this file, so
   `hostfs.WriteFile` can't be used). Return the `Lock`.

`Release` truncates the holder line, unlocks and closes. If the process dies first, the kernel drops the lock and
the holder line stays; nothing reads that line unless the lock is actually held, so a stale line is never shown.

## 4. Files and formats it owns

```
internal/docker/
  docker.go             Client, types, sentinels, CheckVersions
  ref.go                Ref, ParseRef, NormalizeRepo, SameRepo
  cli.go                the CLI adapter: finding docker, arguments, environment, process groups
  parse.go              JSON → types; stderr → sentinel
  *_test.go             fixture, argument and process tests
  testdata/<engine>-<compose>/   one folder per captured version pair
  capture_test.go       fixture capture, build tag `capture`
  names/names.go        the fixed Docker names
  dockerfake/fake.go    the fake
  dockertest/contract.go the contract suite
internal/locks/
  locks.go  locks_test.go
.github/workflows/capture.yml   the fixture capture job, run by hand
Makefile                        capture
```

- **Lock files** (`state/host.lock`, `state/health.lock`, `instances/<name>/.lock`, `instances/.<name>.creating/.lock`): root:root 0600, as in the overview's Host layout.
  They are [foundation.md](foundation.md)'s one exception to "every file goes through `WriteFile`": opened in
  place, written in place and held with `flock`. Empty when free; while held, one line:

  ```
  pid=4123 since=2026-11-17T14:15:00Z purpose="upgrade kids"
  ```

  `since` is RFC 3339 UTC from the Locker's `Clock`. The line is information only: the lock is the `flock`, never
  the text. A line that does not parse gives a zero `Holder`.
- **Docker fixtures.** Each `testdata/<engine>-<compose>/` holds the stdout of `version`, `compose version`,
  `compose ps` (running, exited), `container inspect` (running healthy, unhealthy, exited, no healthcheck, with
  published ports, with `cap_drop` and a read-only root), `image inspect` (a Docker Hub official image, a Docker
  Hub user image, a local-registry image, an image whose config has `Labels` with
  `org.opencontainers.image.version`), `image ls --quiet`, `container ls --quiet`, `network inspect`, and the
  stderr and exit status of every failure the classifier matches. Two sets are kept: the minimum versions
  (Engine 24.0, Compose 2.24) and the newest stable Docker when the set is captured, whose default image store
  may differ. `go test -tags capture -run TestCapture ./internal/docker/`, run as root on a throwaway Linux
  machine with that Docker, rewrites one set from objects it creates and removes.
- **Refreshing the fixtures.** Two ways, with the same test:
  - `.github/workflows/capture.yml` (`on: workflow_dispatch`) has one job per set, on an Ubuntu runner. Each job
    installs that Docker Engine and Compose from Docker's apt repository (the minimums from
    `internal/buildinfo/pins.json`, or the newest stable), runs the capture test as root, and uploads
    `internal/docker/testdata/<engine>-<compose>/` as the workflow artifact `fixtures-<engine>-<compose>`. The
    workflow never pushes or opens a pull request: Actions may not create pull requests in this repo. The
    [Maintainer](../../GLOSSARY.md) runs `gh run download <run-id>`, reviews the files, and commits them by a
    normal pull request.
    Third-party actions are pinned to full commit SHAs.
  - `make capture` runs `sudo go test -tags capture -run TestCapture ./internal/docker/` on this machine, which
    must be a throwaway Linux machine with the Docker to capture.
- **The Docker names** in `names` (the Go form of the overview's table) and the **normalised image name** form.

## 5. Depends on

- [foundation](foundation.md): `buildinfo.Pins` (minimum versions), `hostfs.Paths` (lock paths), `op.Clock` and
  `op.Emitter` (the holder line and the `lock_wait` event).
- No other module. Both packages are leaves, and `names` imports nothing.

Import rules this spec adds to `internal/archtest`:

- `internal/docker/names` imports only the standard library.
- `internal/docker` is imported only by `cli` (to construct the adapter and pass it to modules), `host`, `proxy`,
  `lifecycle`, `backup`, `versions`, `upgrade` and `health`. No other non-test package imports it.
- [render.md](render.md) imports only `internal/docker/names` from `internal/docker`.
- `internal/docker/dockerfake` and `internal/docker/dockertest` are imported only by tests (and the journey
  harness).

## 6. Decisions

- Docker only through the `docker` and `docker compose` CLI, parsed from `--format json`, behind one module; no
  Docker socket in any container: [ADR 0003](../adr/0003-single-static-go-binary.md).
- Compose files are generated from the [Instance record](../../GLOSSARY.md)
  ([ADR 0002](../adr/0002-instance-folder-is-the-record.md)), so the adapter works on projects and never writes a
  file.
- A container belongs to an Instance through the label `io.abyssctl.instance=<name>`. One with that label and no
  Instance record is an [Orphan](../../GLOSSARY.md): `ListContainers` finds it, and only `remove --orphan`
  removes it (`RemoveContainer`).
- `--pull never` on `up` and `run`: images arrive only by an explicit `Pull`, so the network is never needed to
  start, restart or roll back.
- The local daemon through the default context and a cleaned environment, whatever the Operator's shell holds.
- Ctrl-C reaches the tool, not a managed `docker` child; the tool cancels the call itself.
- Image names come out of the adapter in one normalised form, so no caller handles Docker Hub's short names.
- Docker is a real seam with two adapters; one contract suite keeps the fake honest.
- `flock` locks, which the kernel drops when a process dies: "lock held" always means "operation live", so there
  is no stale-lock cleanup. 30 s wait by hand, try-lock for timer runs and the Health check, Host lock before
  Instance locks.
- No new dependency: `syscall.Flock` and `os/exec` from the standard library.
- Fixtures are captured from real Docker, by a hand-run workflow or `make capture`, and reach the repo only
  through a reviewed pull request: the workflow uploads an artifact and never writes to the repo.

## 7. Later

None of its own. Later capabilities (clone, off-host Backups) add methods to `Client` as they need them, each with
fixtures and a contract case.

## 8. Docs it changes

- README, Requirements: Docker Engine 24.0 and Compose 2.24 or newer; an existing Docker that meets them is used as
  is.
- `docs/guides/security.md`: Abyss manages only the local daemon through the default context and ignores
  `DOCKER_HOST`, `DOCKER_CONTEXT` and `DOCKER_CONFIG`.
- `CONTRIBUTING.md`: a "Docker fixtures" section: when to refresh them (a minimum moves, or a new Docker breaks
  parsing), running `capture.yml` and taking its artifacts with `gh run download <run-id>` into a normal pull
  request, and `make capture` on a throwaway Linux machine.

## 9. Acceptance list

`docker` tests run the CLI adapter in-package with a fake command runner that replays `testdata/` fixtures and
records the arguments and environment; process tests use a stub `docker` executable (the test binary re-run as a
helper) in a temp folder in place of the fixed `PATH`. `contract` runs `dockertest.Contract` against the fake in
`make test` and against the real CLI in the journey harness. `locks` tests use real `flock` on files under
`t.TempDir()`, with helper child processes as other holders.

1. `CheckVersions` accepts the pinned minimums and anything newer, refuses an older Engine or Compose naming the
   part, the version found and the minimum, and ignores distribution suffixes. → `TestCheckVersions` (table)
2. `Versions` parses both fixture sets and strips a leading `v`; captured stderr gives `ErrDaemon` and
   `ErrNoCompose`. → `TestVersions_ParsesFixtures`, `TestVersions_DaemonDown`, `TestVersions_NoCompose`
3. With no `docker` on the fixed `PATH`, every method returns `ErrNotInstalled`; once a `docker` appears, the same
   adapter value finds it. → `TestCLI_NotInstalledThenFound`
4. Every call starts `--context default` and runs with exactly `PATH`, `HOME`, `LC_ALL=C` and
   `DOCKER_CLI_HINTS=false`, whatever the test process's environment holds. → `TestCLI_ContextAndEnvironment`
5. Compose calls pass the project name, folder and file; `ComposeUp` always passes `--detach --pull never`, and
   `Wait`, `WaitTimeout` (rounded up to whole seconds) and `ForceRecreate` map to their flags; `ComposeStop` passes
   only `stop`; a missing `compose.yaml`, or `Wait` with a `WaitTimeout` of zero or less, fails without running
   `docker`. → `TestCompose_Arguments`, `TestCompose_MissingFile`, `TestComposeUp_BadWaitTimeout`
6. `ComposeDown` never passes `--volumes` or `--rmi`. → `TestComposeDown_KeepsVolumesAndImages`
7. A failed `ComposeUp --wait` whose container is running and unhealthy or still starting, or exited or
   restarting after it started, is `ErrNotHealthy`; one whose container is `created` and never started ("port is
   already allocated"), or that has no container, is classified from stderr instead. → `TestComposeUp_NotHealthy`
   (table)
8. `ComposePs` parses line-delimited JSON and a JSON array alike. → `TestComposePs_Formats`
9. `Container` parses each inspect fixture into every field (state, health, exit code, times, networks and
   aliases, published ports, user, capabilities, read-only root, security options), and a missing container is
   `false` with no error. → `TestContainer_ParsesFixtures`, `TestContainer_Missing`
10. `ListContainers` passes the label filter (and none for `""`), inspects all IDs in one call, makes no second call when there are
    none, and drops an ID that vanished between the calls; `RemoveContainer` passes `container rm --force <name>`.
    → `TestListContainers`, `TestRemoveContainer_Arguments`
11. `Image` and `ListImages` parse ID, size, tags, digests and config `Labels`; `ListImages` inspects each unique ID once. →
    `TestImage_ParsesFixtures`, `TestListImages_DedupesIDs`; `Pull` passes `image pull --quiet <ref>`. →
    `TestPull_Arguments`
12. Every image name the adapter returns is normalised: `caddy` → `docker.io/library/caddy`,
    `actualbudget/actual-server` → `docker.io/actualbudget/actual-server`, a local-registry name unchanged; and
    `DigestFor` finds a digest whichever form the caller passes. → `TestImage_NormalisedNames`,
    `TestImage_DigestFor`
13. `ParseRef` and `NormalizeRepo` follow Docker's rule (registry host detection, `docker.io` and
    `index.docker.io`, `library/`, tags after the last `/`, digests) and reject what Docker would reject. →
    `TestParseRef` (table)
14. `Network` parses the subnets; `CreateNetwork` passes the bridge driver and labels and lets Docker pick the
    subnet. → `TestNetwork_ParsesSubnets`, `TestCreateNetwork_Arguments`
15. Each captured failure classifies to its sentinel; an unknown one has a nil `Kind` and keeps at most 4 KiB of
    stderr. → `TestClassify_Fixtures`, `TestClassify_UnknownKeepsTail`
16. `Exec` and `Run` return a `Result` with the command's code and no error for a non-zero exit, cap each stream
    at 1 MiB with `Truncated` set, and return an error for exit 125–127 and for a stopped container
    (`ErrNotRunning`). → `TestExec_Result`, `TestExec_Truncates`, `TestExec_DockerFailure`
17. `ExecAttached` passes `--interactive` and `--tty` as asked and returns `*ExitError` with the command's code. →
    `TestExecAttached_Arguments`, `TestExecAttached_ExitCode`; `Logs` passes `--follow` and `--tail <n>` as asked
    and none when not. → `TestLogs_Arguments`
18. `Run` always passes `--rm`, `--pull never`, `--network none`, `--cap-drop ALL` and `no-new-privileges`, and
    marks read-only mounts `readonly`. → `TestRun_Hardened`
19. Cancelling `ctx` stops a managed call with SIGTERM, then SIGKILL after 10 s, and returns `ctx.Err()`;
    managed calls run in their own process group with no stdin, attached calls in the caller's. →
    `TestCLI_CancelTerminatesChild`, `TestCLI_ProcessGroups`
20. A successful call writes nothing to the tool's stdout or stderr. → `TestCLI_SilentOnSuccess`
21. No method puts its input into an argument it was not given: `ComposeUp` and `Run` arguments hold only the
    names, paths and references passed in. → `TestCLI_ArgumentsOnlyFromInputs`
22. Both adapters agree on: up → running → stop → exited → up → down → gone; up of a missing image is
    `ErrNotFound`; up `--wait` of the unhealthy image is `ErrNotHealthy`; `Pull` makes an image local; removing one of
    several references of a used image removes just that reference, removing the last one is `ErrConflict`, and
    removing a repo's last tag drops its digest references; creating a network twice is `ErrConflict`; `Exec` on a stopped
    container is `ErrNotRunning`; missing objects read as `false`; `RemoveContainer` of a missing container is
    `ErrNotFound`; `ListContainers` finds a project's container by `io.abyssctl.instance`. → `dockertest.Contract`: `TestContract_Fake` (module test) and
    `TestDockerContract` (journey harness, real CLI)
23. The fake records every call in order, keeps each project's `ComposeFile`, returns a `Fail` once and a
    `FailWhen` every time, and refuses to start a missing image. → `TestFake_RecordsCalls`,
    `TestFake_ComposeFile`, `TestFake_FailureInjection`
24. `names.InstanceProject("kids")` is `abyssctl-kids`, and `names` imports nothing; Render imports only
    `names`; only `cli`, `host`, `proxy`, `lifecycle`, `backup`, `versions`, `upgrade` and `health` import
    `internal/docker`; `dockerfake` and `dockertest` appear in no non-test build. → `TestNames`, `TestImportRules`
25. Two `Acquire`s of one lock from two processes serialise; `TryAcquire` of a held lock returns a `*BusyError`
    matching `ErrBusy` at once, with the holder's pid and purpose. → `TestAcquire_Serialises`,
    `TestTryAcquire_Busy`
26. `Acquire` gives up after `wait` with a `*BusyError` matching `ErrTimeout` and `ErrBusy`, emits exactly one
    `lock_wait` event with the holder's purpose and pid, and returns `ctx.Err()` holding nothing when cancelled.
    → `TestAcquire_Timeout`, `TestAcquire_EmitsWaitOnce`, `TestAcquire_Cancelled`
27. Within one Locker: `Acquire` of the Host lock while holding any lock, `Acquire` or `TryAcquire` of the health
    lock while holding any lock, `Acquire` of any lock while holding the health lock, `Acquire` of an Instance
    lock that sorts before a held one, of any path already held, or of a path that is not `HostLock()`,
    `HealthLock()`, `InstanceLock(x)` or `CreatingLock(x)` fails with `ErrLockOrder` without touching the file;
    Host then Instance locks in name order succeed; `TryAcquire` of Instance locks out of order, and under the
    health lock, is allowed. → `TestLocker_Order` (table)
28. A lock held by a child process is released when that process is killed, and the next `TryAcquire` succeeds
    despite the stale holder line. → `TestLock_ReleasedOnProcessDeath`
29. `CreatingLock(name)` is accepted and counts as name's Instance lock: after the folder is renamed to
    `instances/<name>/`, a second process's `TryAcquire(InstanceLock(name))` is busy until `Release`, and in the
    same Locker `Acquire(InstanceLock(name))` fails with `ErrLockOrder` (already held) rather than blocking. →
    `TestLocker_CreatingLock`
30. `Acquire` creates a missing lock file with mode 0600 and fails with `fs.ErrNotExist` when the folder is
    missing. → `TestAcquire_CreatesFile`, `TestAcquire_MissingFolder`
31. A waiter whose lock file is deleted while it waits gets `fs.ErrNotExist`; one whose lock file is replaced
    takes the lock on the new file. → `TestAcquire_FileRemovedWhileWaiting`, `TestAcquire_FileReplaced`
32. A lock file is not inherited by a child process. → `TestAcquire_CloseOnExec`
33. While held, the lock file holds one parseable holder line with `since` in UTC; `Release` empties it and is safe
    to call twice; `New` panics on a purpose outside `[a-z0-9 .-]`. → `TestHolderLine`, `TestRelease_Idempotent`,
    `TestNew_RejectsBadPurpose`
34. `capture.yml` runs only on `workflow_dispatch`, has one job per fixture set, uploads each set as
    `fixtures-<engine>-<compose>`, and has no step that pushes, commits or opens a pull request. → (review)
35. Docs updated: README Requirements, `docs/guides/security.md`, `CONTRIBUTING.md` "Docker fixtures". → (review)
