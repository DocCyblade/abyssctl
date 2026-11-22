# Spec: lifecycle

Build step 7 of 16. The [overview](README.md) has the module map, the call graph and the Host layout. Shared
packages (`internal/hostfs`, `internal/op`, `internal/buildinfo`) are in [foundation.md](foundation.md); the Docker
adapter and locks are in [docker-and-locks.md](docker-and-locks.md); the Instance record and folder are in
[store.md](store.md); generated files are in [render.md](render.md); sites and Certificates are in
[proxy.md](proxy.md); the account, Settings and `host setup` are in [host.md](host.md). Capitalised terms are
defined in [GLOSSARY.md](../../GLOSSARY.md).

## 1. Purpose

Build the Lifecycle module (`internal/lifecycle`) and its verbs: `create`, `remove`, `remove --orphan`, `list`,
`status`, `start`, `stop`, `restart`, `set`, `logs`, `exec` and `reset-password`. Lifecycle owns:

- the order of every step that brings an [Instance](../../GLOSSARY.md) into being, changes what it runs, or takes
  it away, and what is undone when a step fails;
- all-or-nothing `create`: either the Instance exists, runs, is healthy and is reachable through the
  [Proxy](../../GLOSSARY.md), or nothing of it is left;
- the final [Backup](../../GLOSSARY.md) on `remove`, taken before anything is deleted;
- wanted vs. actual state: `STATE` in the [Instance record](../../GLOSSARY.md) is what the
  [Operator](../../GLOSSARY.md) wants, Docker says what is; `status` shows both;
- regenerating an Instance's `compose.yaml` and site file on `create`, `start` and `restart`, and writing
  `TOOL_VERSION` when it does;
- [Internet access](../../GLOSSARY.md): off unless `create --internet` or `set <name> --internet=on` turns it on, and which
  networks an Instance joins because of it ([ADR 0005](../adr/0005-internet-access-per-instance.md));
- the refusals every changing command makes on an Instance in a state it must not touch;
- [Orphans](../../GLOSSARY.md): finding them for `status`, and removing one on `remove --orphan`;
- `status`, which joins live Docker state with the [Problems](../../GLOSSARY.md) and available updates the
  [Health check](../../GLOSSARY.md) last found.

Lifecycle also builds the part of `backup.Create` that `remove` needs, to the interface in
[backup.md](backup.md) (build step 8 adds the rest).

When this spec is done, the tracer bullet runs end to end: `sudo abyssctl host setup --base-domain
budget.example.com --cert wild.pem --key wild.key`, then `sudo abyssctl create kids --version 26.11.0`, and
`https://kids.budget.example.com` answers through the Proxy; `sudo abyssctl remove kids` takes it away and keeps
its Backups. Journeys 2 and 3 pass, and with them the Instance parts of journey 4.

## 2. Interface

Signature sketches, not final code.

```go
// The parts of other modules Lifecycle calls: its test seams. *proxy.Proxy and *backup.Backup satisfy them;
// module tests use fakes.
type Proxy interface {
    Status(ctx context.Context) (proxy.Status, error)
    Covers(hostname string) (id string, ok bool, err error)
    AddSite(ctx context.Context, name, hostname string) error // caller holds the Host lock
    RemoveSite(ctx context.Context, name string) error        // caller holds the Host lock
}

type Backup interface {
    // PlanCreate runs Create's refusal checks, the free-space one included, with no lock and no change.
    PlanCreate(ctx context.Context, name string, kind backup.Kind) (op.Summary, error)
    // Create takes a Backup of the Instance. Caller holds the Instance lock.
    Create(ctx context.Context, name string, kind backup.Kind) (backup.Info, error)
    // Lifecycle calls both only with backup.KindFinal.
}

// HealthReader gives status what the latest Health check wrote. The CLI builds it from health's reader
// (health.md); nil means no Health check exists in this build yet.
type HealthReader interface {
    Read() (HealthView, error) // ErrNoHealthResults when the file does not exist yet
}

var ErrNoHealthResults = errors.New("no Health check results yet")

type HealthView struct {
    CheckedAt         time.Time       // UTC
    Problems          []HealthProblem // current Problems, Live and Event
    LatestActual      string          // the latest stable Actual version when checked, "" when unknown
    LatestCaddy       string          // the latest stable Caddy version when checked, "" when unknown
    VersionsFetchedAt time.Time       // UTC; when LatestActual and LatestCaddy were fetched; zero when never
}

type HealthProblem struct {
    Label string    // what the Problem is about, as shown: an Instance or Orphan name, "proxy", "disk", "docker"
                    // or "host"
    Text  string    // one sentence for the Operator
    Hint  string    // a full command line, or ""
    Since time.Time // UTC
}

type Deps struct {
    Paths      hostfs.Paths                 // with the Data root (Paths.WithDataRoot)
    Store      *store.Store
    Docker     docker.Client
    Proxy      Proxy
    Backup     Backup
    Locker     *locks.Locker                // used only by Create, for CreatingLock(name)
    Health     HealthReader                 // may be nil
    Account    func() (store.Owner, error)  // host.Account; called only when a compose.yaml is rendered
    Emit       op.Emitter
    Clock      op.Clock
    Version    string                       // buildinfo.Version
    ActualRepo string                       // buildinfo.ActualRepo
    BaseDomain string                       // Settings.BaseDomain
}

func New(d Deps) *Lifecycle

// CheckVersion accepts only a plain X.Y.Z (buildinfo.PlainVersion). The CLI calls it on --version before it asks
// Versions or touches anything.
func CheckVersion(v string) error // nil, or op.Refused

// --- create ---

type CreateRequest struct {
    Name     string
    Hostname string // "" means <Name>.<BaseDomain>
    Version  string // resolved by the CLI through Versions: X.Y.Z
    Digest   string // its "sha256:<64 hex>"; "" only on the interim create --dry-run (Depends on)
    Internet bool   // --internet: the Instance gets Internet access
}

// Options of Start and Restart.
type Options struct {
    // AfterSite, when set, is called once, after Proxy.AddSite succeeds and before ComposeUp. The CLI passes the
    // Host lock's release, so the Instance lock alone covers compose up and the 5-minute wait.
    AfterSite func()
}

// PlanCreate returns the panel. No lock, no write, no pull.
func (l *Lifecycle) PlanCreate(ctx context.Context, r CreateRequest) (op.Summary, error)

// Create makes the Instance, all or nothing. Precondition: the caller holds the Host lock until Create returns
// (Create never calls AfterSite: its teardown removes the site file). Create itself takes the new Instance's lock
// (CreatingLock) before Store's Commit and releases it when it returns.
func (l *Lifecycle) Create(ctx context.Context, r CreateRequest) (store.Record, error)

// --- changing one Instance ---

// Start brings the Instance up from freshly generated files. Caller holds the Host lock until o.AfterSite is
// called, and the Instance lock throughout.
func (l *Lifecycle) Start(ctx context.Context, name string, o Options) error

// Restart is stop + start, so a changed compose.yaml or site file takes effect. Same locks as Start.
func (l *Lifecycle) PlanRestart(ctx context.Context, name string) (op.Summary, error)
func (l *Lifecycle) Restart(ctx context.Context, name string, o Options) error

// Stop stops the container and records STATE=stopped. Caller holds the Instance lock.
func (l *Lifecycle) PlanStop(ctx context.Context, name string) (op.Summary, error)
func (l *Lifecycle) Stop(ctx context.Context, name string) error

// Remove checks free space for the final Backup, stops the Instance, takes the final Backup, then removes the
// site, the containers and the folder. Caller holds the Host lock and the Instance lock.
func (l *Lifecycle) PlanRemove(ctx context.Context, name string) (op.Summary, error)
func (l *Lifecycle) Remove(ctx context.Context, name string) (store.Removed, error)

// RemoveOrphan force-removes every container labelled io.abyssctl.instance=<name> when no Instance folder of that
// name exists. Caller holds the Host lock.
func (l *Lifecycle) PlanRemoveOrphan(ctx context.Context, name string) (op.Summary, error)
func (l *Lifecycle) RemoveOrphan(ctx context.Context, name string) error

// Set turns the Instance's Internet access on or off: it rewrites INTERNET in the record, re-renders
// compose.yaml and recreates the container when it is running. Setting the current value is a no-op. Caller
// holds the Instance lock.
func (l *Lifecycle) PlanSet(ctx context.Context, name string, internet bool) (op.Summary, error)
func (l *Lifecycle) Set(ctx context.Context, name string, internet bool) error

// --- reading ---

func (l *Lifecycle) List(ctx context.Context) ([]Entry, error)
func (l *Lifecycle) Status(ctx context.Context) (Report, error)

type Entry struct {
    Name   string
    Record *store.Record      // nil when the record is invalid
    Err    *store.RecordError // nil when the record is valid
    Live   Live
}

type Live struct {
    Exists      bool      // a container abyssctl-<name> exists
    State       string    // Docker's: created, running, exited, …; "" when none
    Health      string    // "", starting, healthy, unhealthy
    StartedAt   time.Time
    ContainerID string
    Networks    []string  // the networks the container is attached to, sorted; nil when none
}

type Report struct {
    Version   string          // this binary's
    Proxy     proxy.Status
    Instances []InstanceStatus
    Orphans   []Orphan
    Health    HealthSummary
}

type InstanceStatus struct {
    Entry
    Agrees          bool   // wanted and actual agree: running+running healthy, or stopped+not running
    FilesOutOfDate  string // "" or why: "Abyss 0.2.0 → 0.3.0", "compose.yaml differs from what Abyss renders",
                           // "site file missing", "site file names <h>, not <HOSTNAME>"
    WrittenByNewer  string // "" or the newer TOOL_VERSION
    Interrupted     string // "", "upgrade" or "restore"
    Networks        string // "" or the mismatch: "on abyssctl only; its record says Internet access",
                           // "on abyssctl-external; its record says no Internet access"
    UpdateAvailable string // "" or the latest stable Actual version, when newer than ACTUAL_VERSION
}

type Orphan struct {
    Name        string // the label's value
    Containers  []string
}

type HealthSummary struct {
    CheckedAt     time.Time       // zero when there are no results
    Note          string          // "no Health check results yet", or ""
    Problems      []HealthProblem // from HealthView, plus one for a read error or a Proxy.Status error
    CaddyUpdate   string          // "" or LatestCaddy, when newer than the Proxy's version
    VersionsStale bool            // VersionsFetchedAt is non-zero and more than 7 days before Clock's now
}

// --- passthrough ---

// Logs writes the container's log to stdio. No lock.
func (l *Lifecycle) Logs(ctx context.Context, name string, o docker.LogOptions, stdio docker.IO) error

// Exec runs cmd (["/bin/sh"] when empty) in the running container. No lock. A non-zero exit inside the container
// is *docker.ExitError.
func (l *Lifecycle) Exec(ctx context.Context, name string, cmd []string, stdio docker.IO) error

// ResetPassword runs Actual's own reset-password script in the running container, on a terminal. Caller holds
// the Instance lock.
func (l *Lifecycle) ResetPassword(ctx context.Context, name string, stdio docker.IO) error
```

### Invariants and error modes

- **Refusals come before any change.** Every `op.Refused` (exit 2) and `op.NeedsOperator` (exit 4) below is
  found before Lifecycle writes a file, pulls an image or touches a container, with two exceptions:
  - `remove` checks free space with `Backup.PlanCreate` before `ComposeStop`, so a Host already short of space is
    refused with the Instance still running; the final Backup checks again after the stop, because the space can
    change in between, and on that refusal (exit 2) Lifecycle starts the Instance again, so nothing is left
    changed;
  - the `ErrNotFound` refusal (exit 2) for a pinned image comes from the pull itself, in `create`, `start` and
    `restart`; nothing is written before that pull.

  After the first change, a failure is a plain error (exit 1) naming the step.
- **Each `Plan…` twin agrees with its run.** It returns the same refusals for the same Host, takes no lock,
  writes nothing, pulls nothing and starts nothing. The only refusals a run can add are the two exceptions above
  (the final Backup's free-space refusal and a pull's `ErrNotFound`), which depend on the moment it runs, and Docker's own
  failures.
- **The disk wins over Docker.** An Instance exists exactly when its folder holds a valid record. A folder with an
  invalid record shows as `invalid record`, changing verbs refuse it (exit 4) until the Operator fixes the record
  by hand, and its labelled containers are not Orphans (`remove --orphan` refuses that name too). A valid record with no container is shown as "not created" and
  `start` creates it. A labelled container with no Instance folder is an Orphan, which only `remove --orphan`
  removes.
- **Generated files are regenerated, never edited.** `create`, `start`, `restart` and `set` render
  `compose.yaml` with `render.InstanceCompose` from the record, the account's IDs (`Deps.Account`) and the
  subnets of each network the Instance joins (`docker.Network(…).Subnets`): `abyssctl` (`names.Network`), plus
  `abyssctl-external` (`names.ExternalNetwork`) when the record has Internet access, in that order. They write it
  through `Store.WriteCompose` (only when the bytes change); `create`, `start` and `restart` also rewrite the site
  file through `Proxy.AddSite`. `stop`, `remove` and the reading verbs never render.
- **Networks follow the record.** An Instance joins `abyssctl` always and `abyssctl-external` only when its
  record has `INTERNET=1`; it publishes no port. Its container's networks change only when its `compose.yaml`
  is applied by `ComposeUp`.
- **`AddSite` comes before the container starts.** In `create`, `start` and `restart`, the site file is written,
  validated and reloaded before `ComposeUp`, after `Covers` has confirmed a [Certificate](../../GLOSSARY.md)
  covers the Hostname. A Hostname no Certificate covers is refused (exit 2) with the cause `proxy.ErrNotCovered`
  and the hint `abyssctl cert install <cert.pem> <key.pem>`, before anything changes.
- **One wait.** Every `ComposeUp` waits for the container to be healthy (`UpOptions{Wait: true, WaitTimeout:
  5 * time.Minute}`). The Docker healthcheck is Actual's `/health` script ([render.md](render.md)). Lifecycle never
  pulls inside `ComposeUp`: it pulls first, only when the pinned image is not local.
- **`TOOL_VERSION` follows the container.** `create`'s record gets `TOOL_VERSION` from `Stage` (the Store's
  `toolVersion`, which is `buildinfo.Version`); a `create` that fails is discarded, so no record keeps it without
  a container. `start` and `restart` write `TOOL_VERSION=Deps.Version` only after `ComposeUp` returns without
  error. Nothing else in Lifecycle writes it.
- **Guards on every changing verb.** `start`, `restart`, `stop`, `set`, `remove` and `reset-password` read the record
  with `Store.Get` and refuse, in this order:
  1. a `TOOL_VERSION` newer than `Deps.Version` (`buildinfo.CompareVersions`): `op.NeedsOperator("<name> was
     last changed by Abyss <v>, newer than this binary (<mine>)", "install abyssctl <v> or newer")` (exit 4).
     The version is the record's, or, for an invalid record, the `*store.RecordError`'s `ToolVersion` (read from
     the `TOOL_VERSION=` line before the full check, [store.md](store.md)), so a record a newer Abyss wrote gets
     this hint even when this binary can't parse the rest;
  2. an invalid record: the `*store.RecordError` (`op.NeedsOperator`, exit 4), naming the file, line and problem,
     hint `fix <file> by hand`;
  3. `UPGRADE_BACKUP` set: `op.NeedsOperator("an Upgrade of <name> was interrupted", "abyssctl upgrade <name>")`
     (exit 4). The caller holds the Instance lock, so no [Upgrade](../../GLOSSARY.md) is under way. It comes
     before the next guard because an interrupted Rollback leaves both, and `upgrade` finishes it;
  4. an `instances/<name>/data.pre-restore/` folder: `op.NeedsOperator("a Restore of <name> was interrupted",
     "abyssctl restore <name>")` (exit 4). The caller holds the Instance lock, so no
     [Restore](../../GLOSSARY.md) is under way.

  `exec` has no guard beyond the Instance existing and its container running; `logs` needs only the folder.
- **Missing Instance.** Every verb that takes a name returns Store's `ErrNoInstance` (matching `fs.ErrNotExist`)
  for a name with no folder; [cli.md](cli.md) shows it as "no such Instance". A command that waited for the lock
  of an Instance that was removed meanwhile gets the same from the Locker.
- **Account and networks.** A missing `abyssctl` account (`host.ErrNoAccount`) or `abyssctl` network is refused
  (exit 2) with the hint `abyssctl host setup`; so is a missing `abyssctl-external` network for an Instance with
  Internet access, and a Proxy that is not set up (`proxy.Status().SetUp` false). In `create`, `start`,
  `restart` and `set`, each network the record says the Instance joins and its container is not on yet (all of
  them when there is no container) is refused (exit 2) when its `Free()` is 0: "network <n> has no free address",
  hint "remove an Instance to free an address".
- **No [Secret](../../GLOSSARY.md) passes through Lifecycle's events.** `logs`, `exec` and `reset-password` pass
  the container's input and output straight between the terminal and Docker; nothing else sees it and none of it
  is an event field.
- **Ctrl-C.** When `ctx` is cancelled during `create`, the teardown runs on `context.WithoutCancel(ctx)` with a
  two-minute deadline, so an interrupted `create` still leaves nothing behind. When it is cancelled during
  `remove`'s final Backup, the `ComposeUp` that starts the Instance again runs on `context.WithoutCancel(ctx)`
  with a 5-minute deadline (the one health wait), so an interrupted `remove` leaves the Instance as it found it.

## 3. Behaviour

### What the CLI does around Lifecycle

1. **Settings and wiring.** The CLI reads `abyssctl.env` (`host.ReadSettings`), builds the Paths with the Data
   root, the Store, the Proxy, the Backup module and the Locker, and passes `BaseDomain`, `buildinfo.Version`,
   `buildinfo.ActualRepo` and `host.Account` into `Deps`.
2. **Flags.**

   | Verb | Arguments and flags | Prompt | `--dry-run` | `--json` |
   |---|---|---|---|---|
   | `create` | `<name> [--hostname H] [--version X.Y.Z] [--internet]` | none | yes | no |
   | `remove` | `<name>`, `--yes` | type the name | yes | no |
   | `remove --orphan` | `<name>`, `--yes` | type the name | yes | no |
   | `start` | `<name>` | none | no | no |
   | `stop` | `<name>`, `--yes` | y/N | yes | no |
   | `restart` | `<name>`, `--yes` | y/N | yes | no |
   | `set` | `<name> --internet=on\|off`, `--yes` | y/N | yes | no |
   | `list` | | | | yes |
   | `status` | | | | yes |
   | `logs` | `<name>` or `proxy`, `-f`/`--follow`, `--tail N` (default: all) | | | |
   | `exec` | `<name> [-- <cmd…>]` | | | |
   | `reset-password` | `<name>` | the script's own | | |

   With no TTY and no `--yes`, a prompt refuses (exit 2). `logs proxy` is `docker.Logs` of the Proxy container,
   called by the CLI directly ([proxy.md](proxy.md)); `proxy` is a reserved Instance name, so it never clashes.
3. **Locks**, from the lock table in [cli.md](cli.md), Host lock first:

   | Verb | Locks the CLI takes |
   |---|---|
   | `create` | Host (and `Create` takes the new Instance's lock itself) |
   | `remove`, `start`, `restart` | Host, then Instance |
   | `remove --orphan` | Host |
   | `stop`, `set`, `reset-password` | Instance |
   | `list`, `status`, `logs`, `exec`, every `--dry-run` | none |

   `start` and `restart` take the Host lock because they may rewrite the site file, and the Host lock guards the
   Proxy's files. They hold it only through the site step: the CLI passes its release as `Options.AfterSite`, so
   the Instance lock alone covers `ComposeUp` and the 5-minute wait, and other Host-lock commands need not wait
   for it. `create` holds the Host lock until `Create` returns, because its teardown removes the site file.
4. **`create`'s version.** The CLI calls `CheckVersion` on `--version` first, so `26.11.0-beta.1`, `v26.11.0` or
   `26.11` is refused (exit 2) before any registry call, Store or Docker. It then resolves the version to its
   digest with `versions.Resolve(ActualRepo, version)`, or, with no `--version`, asks `versions.Latest(ActualRepo)`
   afresh. A version the registry does not have is refused (exit 2); any other registry failure is exit 1; either
   way nothing is created. Both happen before the Host lock is taken ([versions.md](versions.md)). Until build
   step 9, the interim path in [Depends on](#5-depends-on).
5. **Activity log.** Changing verbs are logged ([cli.md](cli.md)). `list`, `status` and `logs` write nothing to
   the activity log; `exec` writes only its `begin` and `end` lines ([below](#exec)); `reset-password` logs its
   header and its one event, never its input or output.

### `create`

`PlanCreate` and `Create` share the checks, in this order:

1. The Hostname is `r.Hostname`, or `<name>.<BaseDomain>` (the [Base domain](../../GLOSSARY.md)) when it is
   empty.
2. `Proxy.Status`: not set up → refused, hint `abyssctl host setup`.
3. `Deps.Account()` → the `DataOwner` for `store.NewInstance`; `ErrNoAccount` passes through.
4. `Store.CheckNew(NewInstance{Name, Hostname, Version, Digest, DataOwner})`: name, an existing Instance, the
   Hostname and its uniqueness, the version and digest. Its refusals pass through.
5. `Proxy.Covers(hostname)`: not covered → refused with `ErrNotCovered` and the `cert install` hint.
6. `Docker.Network(names.Network)`, and with `r.Internet` also `Docker.Network(names.ExternalNetwork)`: a
   missing one → refused, hint `abyssctl host setup`; one with `Free() == 0` → refused ("network <n> has no free
   address"). Their subnets, in that order, become `render.Instance.TrustedProxies`.
7. `Docker.Image(record image)`: whether a pull is needed.

The panel: title "Create kids"; rows Hostname, Actual (`26.11.0`), Certificate (its id, from `Covers`), Data
(`instances/kids/`), Internet access (`no (pass --internet for bank sync)`, or `yes`); steps "pull docker.io/actualbudget/actual-server:26.11.0" (only when not local), "move the
old Backups in backups/kids/ to backups/removed/kids-<stamp>/" (only when `Store.OldBackups(name)` finds that
folder, left by an earlier Instance of the name), "write the Instance folder", "add the site and reload the Proxy",
"start kids and wait up to 5 min for it to be healthy".

`Create` then runs:

1. **Pull** `Record.Image(ActualRepo)` (`<repo>:<version>@<digest>`) if it is not local. `ErrNotFound` →
   refused ("Actual 26.11.0 with that digest is not available"); any other failure → exit 1. Nothing is written
   yet. Event `pull image=…`.
2. **Stage**: `Store.Stage(spec)`, with `NewInstance.Internet = r.Internet`. From here on, any failure runs the teardown below. When `Stage` moved an old
   `backups/<name>/` aside (`Staged.BackupsMovedTo()`), event `backups_moved instance=kids
   to=backups/removed/kids-20261117T080000Z/`; the teardown does not move them back.
3. **Lock**: `Locker.Acquire(ctx, Paths.CreatingLock(name), locks.DefaultWait)`. The lock file is new, so the
   wait is never used. After `Commit` the same flock is the Instance lock, so a command waiting for it waits for
   the whole `create`.
4. **Compose file**: `Staged.WriteCompose(render.InstanceCompose(Instance{Name, Image, UID, GID, Internet,
   TrustedProxies}))`.
5. **Commit**: `Staged.Commit()`. Event `created instance=kids hostname=kids.budget.example.com version=26.11.0`.
6. **Site**: `Proxy.AddSite(ctx, name, hostname)`, which starts a stopped Proxy first and validates and reloads.
   Event `site instance=kids`.
7. **Start**: `Docker.ComposeUp(Project{names.InstanceProject(name), Paths.InstanceDir(name)}, UpOptions{Wait:
   true, WaitTimeout: 5 min})`. `ErrNotHealthy` → "kids did not become healthy within 5 min". Event `healthy
   instance=kids after=41s`.
8. **Release** the Instance lock and return the record.

**Teardown**, on any failure from step 2 on, each part only if its step was reached, each failure emitted as an
event and joined to the returned error: `ComposeDown` of the project, `Proxy.RemoveSite(name)`,
`Staged.Discard()`, then release the lock. The result is the Host as it was before `create`, except a pulled
image, which stays for a retry and which `image prune` can remove.

A crash part-way leaves at most a `.creating` folder, which `host setup` or the next `create` of that name
deletes, or a committed Instance whose container never started. `status` shows that one as "not created" and
`start` brings it up.

### `start`

1. Guards (above), on `Store.Get(name)`.
2. `Proxy.Status` set up; `Proxy.Covers(record.Hostname)`; `Deps.Account()`; `Docker.Network` of each network
   the record says the Instance joins, with the free-address check for each one the container is not on yet
   (`Docker.Container`'s `Networks`). Refusals as in `create`. A Hostname changed by hand in the record is checked here, before anything changes.
3. Pull the pinned image if it is not local (for example after a `docker image rm` by hand). Event `pull`.
   `ErrNotFound` → refused (exit 2) as in `create`; any other failure → exit 1. Either way nothing is changed.
4. `Store.Put` with `STATE=running` (only when it changes), so the Health check counts the Instance as wanted up
   even if the start fails.
5. Render `compose.yaml` and `Store.WriteCompose` (written only when the bytes change).
6. `Proxy.AddSite(name, hostname)` (written and reloaded only when the bytes change), then `o.AfterSite()` when it
   is set, so the CLI releases the Host lock.
7. `ComposeUp` with `Wait` and 5 min, without `ForceRecreate`. An unchanged running container is left as it is
   and keeps its ID; a changed `compose.yaml` recreates it.
8. `Store.Put` with `TOOL_VERSION=Deps.Version` (only when it changes).

`ErrNotHealthy` is exit 1 ("kids started but is not healthy after 5 min", hint `abyssctl logs kids`); the container
is left as Compose left it and `TOOL_VERSION` is not written. `start` has no panel, no prompt and no `--dry-run`,
so it has no `Plan…` twin.

### `restart`

Steps 1–6 of `start` (so the pull and its refusals too, and `AfterSite`), then `ComposeStop`, then steps 7–8. Stopping first
restarts Actual's process even when nothing changed. `restart` of a stopped Instance starts it and sets
`STATE=running`. The site file is rewritten only through `AddSite`, which already writes and reloads only when the
bytes change; Lifecycle asks nothing else of the Proxy. The panel: title "Restart kids"; rows Hostname, Actual,
downtime "until kids is healthy, up to 5 min"; steps, including "regenerate compose.yaml (Abyss 0.2.0 → 0.3.0)"
when the rendered bytes differ from the file on disk, and "update the site file if it changed and reload the
Proxy"; the warning "kids runs as <old> and is recreated as <new>" when the account's IDs changed.

### `stop`

1. Guards.
2. `Store.Put` with `STATE=stopped` (only when it changes), so the Health check stops counting the Instance as
   down.
3. `ComposeStop`. `restart: unless-stopped` keeps it stopped across a reboot.

No file is rendered and `TOOL_VERSION` is not changed. `PlanStop` of an Instance whose record says stopped and whose
container is not running returns `NoOp`. The panel's warning: "kids is unreachable until abyssctl start kids".

### `set`

`set <name> --internet=on|off` turns the Instance's Internet access on or off. `PlanSet` and `Set` share steps
1–3:

1. Guards.
2. The record's Internet access already equals the value asked for: `PlanSet` returns `NoOp` and `Set` returns
   nil having changed nothing.
3. `Deps.Account()`; `Docker.Network(names.Network)`, plus `Docker.Network(names.ExternalNetwork)` when turning
   it on. A missing network is refused (exit 2, hint `abyssctl host setup`); turning it on, an
   `abyssctl-external` with `Free() == 0` is refused as in `create`.

`Set` then runs:

4. `Store.Put` with `INTERNET=1`, or with `INTERNET` left out for off. Event `internet instance=kids
   access=on`.
5. Render `compose.yaml` and `Store.WriteCompose`.
6. When the container is running, `ComposeUp` with `Wait` and 5 min, without `ForceRecreate`: the changed file
   recreates the container on its new networks. Event `healthy instance=kids after=12s`. When it is not running,
   nothing more: a stopped Instance stays stopped and the change takes effect at its next `start`.

`set` changes nothing else: not the data, the Hostname, `STATE`, `TOOL_VERSION` or the site file. A failure after
step 4 is exit 1 and leaves the record with the new value; `ErrNotHealthy` is "kids was recreated but is not
healthy after 5 min", hint `abyssctl logs kids`, and `abyssctl restart kids` applies the file again. The panel:
title "Internet access for kids"; rows Hostname, Internet access (`no → yes`); steps "write INTERNET=1 to
instance.env" (or "remove INTERNET from instance.env"), "regenerate compose.yaml", and, for a running Instance,
"recreate kids and wait up to 5 min for it to be healthy"; warnings "kids is unreachable until it is healthy
again, up to 5 min" for a running Instance, "kids is stopped; the change takes effect at the next abyssctl start
kids" for one that is not running, and, turning it off, "bank sync, OpenID login and the CORS proxy stop working
for kids".

### `remove`

1. Guards. An invalid record is refused (exit 4, hint `fix <file> by hand`): the final Backup needs the record's
   version and digest for its manifest. Once the Operator fixes the record, `remove` works as usual.
2. **Free space**: `Backup.PlanCreate(ctx, name, backup.KindFinal)`. Its refusals, the free-space one included
   (exit 2), pass through with the Instance still running and nothing changed.
3. **Stop**: `ComposeStop` if the container is `running`, `restarting` or `paused` (backup.md's stop rule), so
   nothing reaches the data between the final Backup and the delete. Event `stop instance=kids`.
4. **Final Backup**: `Backup.Create(ctx, name, backup.KindFinal)`. It finds the container stopped, so it copies
   and does not start it. With no container at all (a `remove` re-run after a later step failed), there is nothing
   to stop and the Backup copies the folder as it is. On any failure (including Backup's free-space refusal,
   exit 2, when the space shrank after step 2, and a cancelled context), Lifecycle starts the container again with
   `ComposeUp` (`Wait`, 5 min, on `context.WithoutCancel(ctx)`) if step 3 stopped a `running` or `restarting` one,
   and returns the Backup's error: nothing is changed.
5. **Site**: `Proxy.RemoveSite(name)` (validate and reload; a missing file is no change).
6. **Containers**: `ComposeDown` of the project. Data is a bind mount and the networks are external, so this
   removes only the container.
7. **Folder**: `Store.Remove(name)`: moves `backups/<name>/` (the final Backup included) to
   `backups/removed/<name>-<stamp>/` and deletes `instances/<name>/` with `data/`.

A failure in steps 5–7 is exit 1 and leaves the Instance with its record, its data and a verified final Backup,
its container stopped (step 5), stopped or already gone (step 6), or gone (step 7). `remove` does not change `STATE`, so a record that says
`running` with no running container makes the Health check raise a "down" Alert. That is intended: the Operator
learns the `remove` did not finish. Running `remove` again starts over at step 1, takes another final Backup and
finishes the job. The outcome line names where the Backups went: "✓ kids is removed; its Backups are in
/srv/abyssctl/backups/removed/kids-20261117T081500Z/".

`PlanRemove` runs steps 1 and 2. The panel: title "Remove kids"; rows Hostname, Actual, Backups "kept, moved to backups/removed/<name>-<stamp>/";
steps as above, from "stop kids" on; warnings "kids's data is deleted" and "its Backups are kept under backups/removed/; abyssctl
restore does not offer them". The CLI then asks the Operator to type the name. There is no flag that deletes the Backups.

### `remove --orphan`

1. `Store.List()` must show no Instance folder of that name: one (valid record or not) is refused ("kids is an
   Instance, not an Orphan", hint `abyssctl remove kids`). `ValidName` does not apply, because no path is built
   from the name, so an Orphan whose label value is not a valid Instance name can still be removed.
2. `Docker.ListContainers("io.abyssctl.instance=<name>")`: none → refused ("no Orphan named ghost").
3. `Docker.RemoveContainer` on each, one event each (`removed container=abyssctl-ghost`).

Lifecycle never touches the Orphan's mounts, images or any file. The panel lists each container with its image and
state.

### `list`

`Store.List()`, then one `Docker.ListContainers(names.InstanceLabel)` call, matched by the label's value. Each
entry carries the record (or its `RecordError`) and the live state. Sorted by name. The CLI's table: NAME,
HOSTNAME, ACTUAL, WANTED, ACTUAL STATE (`running (healthy)`, `exited`, `not created`), INTERNET (`yes` or `no`,
the record's Internet access), and `invalid record` for an invalid one. `list` reads no health results and makes no judgement.

### `status`

`Status` reads, without a lock: `List`'s entries, `Proxy.Status`, the `abyssctl` and `abyssctl-external`
networks, `Deps.Account()` and `Deps.Health`. For each valid Instance it works out:

- **Agrees**: wanted `running` with the container running and healthy, or wanted `stopped` with the container not
  running. Anything else shows both ("wanted running, actual exited").
- **FilesOutOfDate**: `TOOL_VERSION` older than this binary ("Abyss 0.2.0 → 0.3.0"), or the `compose.yaml` on disk
  not equal to what `render.InstanceCompose` gives now ("compose.yaml differs from what Abyss renders", which
  covers a hand edit, changed account IDs and a changed subnet), or a site file that is missing or whose
  `render.ParseSite` Hostname is not the record's `HOSTNAME` ("site file missing", "site file names
  old.example.org, not kids.example.org", which covers a hand-edited `HOSTNAME`). The hint is `abyssctl restart
  <name>`. When the account or a network the Instance joins can't be read, the `compose.yaml` comparison is left
  out; a site file `ParseSite` refuses counts as differing.
- **Networks**: when a container exists and `Live.Networks` is not the set its record says (`abyssctl`, plus
  `abyssctl-external` with Internet access), which one it is on and what the record says ("on abyssctl only; its
  record says Internet access"), hint `abyssctl restart <name>`. The panel shows it with `!`.
- **WrittenByNewer**: `TOOL_VERSION` newer than this binary. Read commands still work; `status` shows the
  warning "kids was last changed by Abyss 0.4.0; install abyssctl 0.4.0 or newer".
- **Interrupted**: `upgrade` when `UPGRADE_BACKUP` is set, else `restore` when `data.pre-restore/` exists
  (`upgrade` wins when both are there). `status`
  takes no lock, so it says "an Upgrade is under way or was interrupted" with the hint that finishes it.
- **UpdateAvailable**: the health results' `LatestActual`, when it is newer than `ACTUAL_VERSION`. Display only.

Orphans are the labelled containers whose label value has no Instance folder; a folder with an invalid record
counts as an Instance folder.

**Versions.** `CaddyUpdate` is the health results' `LatestCaddy` when it is newer than the Proxy's version.
`VersionsStale` is set when `VersionsFetchedAt` is non-zero and more than 7 days before `Deps.Clock`'s now; the
Host line then says "version info stale". Display only.

**Proxy.** A `Proxy.Status` error (for example an invalid `proxy.env`, which is `op.NeedsOperator`) becomes one Problem,
label `proxy`, with the error's sentence and its hint, if any; the Host line shows it, and `status` still succeeds.

**Health results.** With `Deps.Health` nil, or `ErrNoHealthResults`, the note is "no Health check results yet"
and there are no Problems from it. Any other read error becomes one Problem, label `host`, "Health check results
could not be read: <error>", no hint, and `status` still succeeds. Otherwise the Problems and `CheckedAt` are shown as
written; `status` does not re-check them.

The panel follows [cli.md](cli.md)'s style: a Host line (Abyss version, Base domain, the Proxy's state and
version, `↑` when `Proxy.Status().Behind`, `↑ caddy <v> → <latest>` from `CaddyUpdate`, "version info stale",
a Problem labelled `proxy`, and "checked 09:15 EST" for the health results), one entry per
Instance (`●` wanted and running, `○` stopped, `↑` an update or out-of-date files, `!` an invalid record or a
disagreement), Orphans, then Problems with their labels and hints (`→ abyssctl restart kids`). `status` exits 0 whatever it shows;
it exits 1 only when Store or Docker can't be read at all.

### `logs`

`Store.Get(name)` must find the folder (an invalid record is fine: `logs` is how the Operator looks). No container
→ refused ("kids has no container", hint `abyssctl start kids`). Then `Docker.Logs(names.InstanceProject(name),
LogOptions{Follow, Tail}, stdio)`. Ctrl-C ends a follow with exit 0.

### `exec`

The container must exist and be running, else refused ("kids is not running", hint `abyssctl start kids`). The
command is `cmd`, or `/bin/sh` when empty. `docker.IO.TTY` is set when the CLI's stdin and stdout are terminals.
The CLI exits with the command's own exit code when it is non-zero (`*docker.ExitError`). The CLI writes an
activity log `begin` line with the command line (the `begin` line's `command` field, masked by
[cli.md](cli.md)'s rule), and an `end` line with the exit code; the container's input and output are never logged.

### `reset-password`

1. `stdio.TTY` must be true, else refused (exit 2, "reset-password needs a terminal"). The CLI sets `TTY` only
   when its stdin and stdout are terminals. Actual's script reads the password with a raw-mode prompt, so piped
   input does not work.
2. The guards (as for `start`). The container must be running, else refused ("kids is not running", hint
   `abyssctl start kids`).
3. `Docker.ExecAttached(container, ["node", "/app/src/scripts/reset-password.js"], IO{Stdin, Stdout, Stderr,
   TTY: true})`. The script sets the password in `account.sqlite`; existing sessions must log in again.
4. Event `reset_password instance=kids`. A non-zero exit of the script is exit 1, with its code in the error.

The Instance lock keeps a Backup from stopping the container in the middle of the prompt.

## 4. Files and formats it owns

Lifecycle owns no file format. It decides when the files of others are written:

| File | Format owner | Written by Lifecycle |
|---|---|---|
| `instances/<name>/instance.env` | [store.md](store.md) | `Stage` on `create`; `STATE` and `TOOL_VERSION` through `Put` on `start`, `restart`, `stop`; `INTERNET` through `Put` on `set` |
| `instances/<name>/compose.yaml` | [render.md](render.md) | `create`, `start`, `restart`, `set`, through Store, only when the bytes change |
| `proxy/sites/<name>.caddy` | [render.md](render.md), written by [proxy.md](proxy.md) | `AddSite` on `create`, `start`, `restart`; `RemoveSite` on `remove` and a failed `create` |
| `backups/<name>/<name>-<stamp>-final.tar.gz` and `.sha256` | [backup.md](backup.md) | the final Backup on `remove` |

### Package layout

```
internal/lifecycle/
  lifecycle.go   New, Deps, the seams, CheckVersion, the guards
  create.go      PlanCreate, Create, teardown
  change.go      Start, Restart, Stop, Set and their plans
  remove.go      Remove, RemoveOrphan and their plans
  read.go        List, Status
  passthrough.go Logs, Exec, ResetPassword
```

The part of `internal/backup` built in this step is listed in [Depends on](#5-depends-on).

## 5. Depends on

- [foundation](foundation.md): `hostfs.Paths`; `op.Emitter`, `op.Clock`, `op.Summary` and the typed errors;
  `buildinfo.Version`, `buildinfo.ActualRepo`, `buildinfo.CompareVersions`, `buildinfo.PlainVersion`.
- [docker-and-locks](docker-and-locks.md): `docker.Client` (`ComposeUp`, `ComposeStop`, `ComposeDown`,
  `Container`, `ListContainers`, `RemoveContainer`, `Image`, `Pull`, `Network`, `Logs`, `ExecAttached`) and its error
  kinds; `docker/names`; `locks.Locker.Acquire` for `CreatingLock(name)` only.
- [store](store.md): `Get`, `List`, `Put`, `WriteCompose`, `CheckNew`, `OldBackups`, `Stage` and `Staged`,
  `Remove`, `ValidName`, `Owner`, `RecordError.ToolVersion`.
- [render](render.md): `InstanceCompose`, `ParseSite`.
- [proxy](proxy.md): `Status`, `Covers`, `AddSite`, `RemoveSite`, `ErrNotCovered`, through the `Proxy` seam.
- [host](host.md): `Account` and `ReadSettings` (`BaseDomain`), called by the CLI.
- [backup](backup.md) (build step 8): `PlanCreate`, `Create`, `Kind`, `Info`, through the `Backup` seam. **This
  step builds the part of `internal/backup` that `PlanCreate` and `Create(ctx, name, KindFinal)` need**, to
  backup.md's interface: the guards, the free-space check (backup.md's [rule](backup.md#invariants-and-error-modes)), deleting leftover `.partial` files in `backups/<name>/` first, stop if running and start
  again afterwards (also when the copy fails), the case with no container at all (nothing to stop or start), the
  `.tar.gz` of `data/` and `instance.env` with `manifest.json` written as `.partial` and renamed, the read-back
  verification and the `.sha256` sidecar. Build step 8 adds the other kinds, `List`, `Prune`, `Restore`,
  `Revert` and Regroup.
- [versions](versions.md) (build step 9): `Latest` and `Resolve`, called by the CLI for `create`; `--version` is
  optional. Until it lands, the CLI's `create` requires `--version` and takes the digest from Docker
  (`Pull(<repo>:<version>)`, then `Image.DigestFor(repo)`); build step 9 replaces that path. On the interim path
  `create --dry-run` pulls nothing: the CLI calls `PlanCreate` with an empty `Digest`, which skips the digest
  check (Store's `CheckNew` allows that) and the image lookup, and the panel shows the row "image digest: resolved
  at run time" and the step "pull docker.io/actualbudget/actual-server:26.11.0 if it is not local".
- [health](health.md) (build step 12): the results `status` reads, through `HealthReader`. Until it lands, the
  CLI passes nil and `status` shows "no Health check results yet".

Import rule this spec adds to `internal/archtest`: `internal/lifecycle` imports only the standard library,
`internal/hostfs`, `internal/op`, `internal/buildinfo`, `internal/docker`, `internal/docker/names`,
`internal/locks`, `internal/store`, `internal/render`, `internal/proxy` and `internal/backup`. Only `internal/cli`
imports `internal/lifecycle`.

## 6. Decisions

- The Instance folder is the record ([ADR 0002](../adr/0002-instance-folder-is-the-record.md)): the disk wins over
  Docker, a container without a folder is an Orphan, and Orphans are never removed without `remove --orphan`.
- `create` is all-or-nothing: checks first, then build in a `.creating` folder, rename, site, start; any failure
  tears everything down. The new Instance is locked from the moment it is visible, so `start` can't slip in
  between the rename and the first `compose up`.
- Coverage is checked before anything changes, and the site file is written before the container starts, so an
  uncovered Hostname never leaves a half-made Instance and the Proxy answers as soon as Actual is healthy.
- `remove` stops the Instance before its final Backup, so nothing written after the Backup is lost; it deletes
  nothing until that Backup is verified. Backups of a removed Instance are always kept; there is no purge in v1,
  and the teardown guide explains deleting them by hand.
- `STATE` is written before Docker changes the container, on `start`, `restart` and `stop`: the Operator's intent
  is recorded even when Docker then fails, and the Health check judges against it.
- `restart` is stop + up, so it is the one command that applies a changed `compose.yaml` and also restarts
  Actual when nothing changed. A [Tool update](../../GLOSSARY.md) recreates no container; `status` and the MOTD
  point at `restart`.
- Every changing command refuses an Instance last changed by a newer Abyss (exit 4), an invalid record (exit 4,
  fixed by hand) and an Instance with an interrupted Upgrade or Restore (exit 4), because acting on any of them
  could lose data. Read commands still work. The newer-Abyss check reads the `TOOL_VERSION=` line first, so a
  record whose newer format this binary can't parse still gets the "install newer" hint.
- `start` and `restart` release the Host lock once the site file is written (`Options.AfterSite`), so a slow
  health wait holds up only that Instance. `create` keeps it to the end, because its teardown removes the site
  file and a command may not wait for the Host lock while holding an Instance lock.
- `remove` checks free space for the final Backup before it stops the Instance, so a full disk refuses with the
  Instance still serving.
- Internet access is off unless the Operator asks for it ([ADR 0005](../adr/0005-internet-access-per-instance.md)):
  an Instance needs it only for bank sync, OpenID login and the CORS proxy, and `create`'s panel names it so the
  Operator sees the choice. `set` changes it without touching the data or the Hostname, and recreates only a
  running container, so a stopped Instance stays stopped.
- `exec`, `logs` and `reset-password` pass the container's I/O through unfiltered and unlogged: it is the
  Operator's own data. `exec` logs only that it ran, with its masked command line and exit code. `reset-password` works only on a running Instance on a terminal, because Actual's script
  needs a raw-mode TTY.
- `status` shows the health results as they were last written, with when, and never runs a check itself, so it is
  quick and changes nothing.
- No new dependency.

## 7. Later

- Clone (ROADMAP Next): cloning an Instance, and restoring from a removed Instance's Backups.
- `set-hostname` and rename.
- `doctor`.

## 8. Docs it changes

- README: the `create`, `status` and "open the Hostname" lines of **Quickstart**; **Updating Abyss**'s line that
  each Instance's files are regenerated on its next `start`, `restart`, `upgrade` or `restore` (`status` shows which).
- `docs/guides/backup-and-restore.md`: `remove` takes a `final` Backup first; removed Instances' Backups live in
  `backups/removed/<name>-<stamp>/` and are not offered to `restore`.
- `docs/guides/upgrades.md`: after a Tool update, `status` flags files out of date and the next `start`,
  `restart`, `upgrade` or `restore` of each Instance applies them (`restart <name>` does it at once); an Instance changed by a newer Abyss is refused until that Abyss is installed.
- `docs/guides/security.md`: `exec` and `logs` show the Instance's own data; `reset-password` signs out existing
  sessions; Internet access: off by default, `create --internet` or `set <name> --internet=on` for bank sync,
  and OpenID login and the CORS proxy (`ACTUAL_CORS_PROXY_ENABLED`) need it too, even with an OpenID provider on
  the LAN.
- `docs/guides/teardown.md`: `remove` each Instance first; delete `backups/removed/` by hand.
- Reference: the pages of `create`, `remove`, `list`, `status`, `start`, `stop`, `restart`, `set`, `logs`,
  `exec` and `reset-password`, from their help text: arguments, flags, the default Hostname, prompts and locks.

## 9. Acceptance list

Module tests are in `internal/lifecycle`, on `hostfs.Under(t.TempDir())` with a Data root, a real Store,
`dockerfake`, a fake `Proxy`, a fake `Backup`, a fake `HealthReader`, a real `Locker`, an `op.Recorder` and an
`op.ManualClock`, without root. Journey items run the real binary as root on an Ubuntu runner; this spec's
journey slice is journeys 2 (update from v0.0.1) and 3 (Instance life) and the Instance parts of journey 4.

1. `CheckVersion` accepts `26.11.0` and refuses (exit 2) `26.11.0-beta.1`, `v26.11.0`, `26.11` and `26.11.0+x`;
   the CLI calls it before Versions, Store or Docker. → `TestCheckVersion` (table); `TestCreateCmd_VersionFirst`
   in `internal/cli`
2. `create` with no `--hostname` uses `<name>.<BaseDomain>`. → `TestCreate_DefaultHostname`
3. `PlanCreate` and `Create` refuse, with the same error and nothing written, pulled or started: an invalid
   name, an existing Instance, a taken Hostname, an uncovered Hostname (cause `ErrNotCovered`, the `cert install`
   hint), no Proxy set up, no account, no `abyssctl` network, no `abyssctl-external` network with `Internet`
   (hint `abyssctl host setup`), and either network it would join with `Free() == 0` ("network <n> has no free
   address"). → `TestCreate_Refusals` (table)
4. `Create` calls, in order: `Proxy.Status`, `Deps.Account`, `Store.CheckNew`, `Covers`, `Network`, `Image`,
   `Pull` (only when the image is not local; `ErrNotFound` is refused, exit 2, with nothing written), `Stage`,
   `Acquire` of `CreatingLock`, `WriteCompose` with exactly `render.InstanceCompose`'s bytes for the account's IDs,
   `Internet` and the subnets of `abyssctl`, plus `abyssctl-external` with `Internet`, `Commit`, `AddSite`, `ComposeUp` with `Wait` and 5 min; the record has
   `STATE=running` and `TOOL_VERSION` = the binary's. With an old `backups/<name>/` left by an earlier Instance of
   the name, the panel names the move and `Create` emits `backups_moved` with where they went. →
   `TestCreate_Order`, `TestCreate_PullNotFound`, `TestCreate_OldBackups`
5. While `Create` waits in `ComposeUp`, another Locker's `TryAcquire(InstanceLock(name))` is refused as held. →
   `TestCreate_LockedBeforeCommit`
6. A failure at each step from `Stage` on (including `ErrNotHealthy` and a cancelled context) tears down: the
   project is downed, the site removed and the Instance discarded, leaving no folder, site file or container;
   a teardown error is joined to the result. → `TestCreate_Teardown` (table)
7. `Start` refuses an uncovered (hand-edited) Hostname, a Proxy that is not set up, a missing account, a
   missing `abyssctl` network and, for an Instance with Internet access, a missing `abyssctl-external` network
   (hint `abyssctl host setup`) before any write; otherwise it writes `STATE=running`,
   regenerates `compose.yaml` and the site file only when their bytes change, calls `AddSite` before `ComposeUp`
   (`Wait`, 5 min, no `ForceRecreate`), and writes `TOOL_VERSION` after it; `ErrNotHealthy` leaves `TOOL_VERSION`
   as it was and gives the `logs` hint. `Options.AfterSite` is called exactly once, after `AddSite` succeeds and
   before `ComposeUp`, and not at all when a step before it fails. → `TestStart_Refusals` (table), `TestStart`,
   `TestStart_AfterSite`
8. `Start` of a running Instance whose files did not change keeps its container ID; a changed `compose.yaml`
   gives a new one; a missing image is pulled after the checks and before `STATE` or any file is written, and a
   pull `ErrNotFound` is refused (exit 2) and any other pull failure is exit 1, both with nothing changed. →
   `TestStart_ContainerID`, `TestStart_PullsMissingImage` (table)
9. `Restart` calls `ComposeStop` between `AddSite` and `ComposeUp`, and on a stopped Instance sets
   `STATE=running`; its panel names a regenerated `compose.yaml` and changed account IDs; `Options.AfterSite` is
   called once, after `AddSite` and before `ComposeStop`. → `TestRestart`
10. `Stop` writes `STATE=stopped`, then calls `ComposeStop`, renders nothing and leaves `TOOL_VERSION`;
    `PlanStop` of a stopped Instance is `NoOp`. → `TestStop`
11. `start`, `restart`, `stop`, `set`, `remove` and `reset-password`, and the plans, refuse with nothing changed, in this
    order: a newer `TOOL_VERSION` (exit 4, hint `install abyssctl <v> or newer`), also on a record that is
    otherwise invalid; an invalid record (exit 4, hint `fix <file> by hand`, the message naming the file, line and
    problem); `UPGRADE_BACKUP` set (exit 4, hint `abyssctl upgrade <name>`) and, checked after it, a
    `data.pre-restore/` folder (exit 4, hint `abyssctl restore <name>`), so a folder with both gets the `upgrade`
    hint. After the record is fixed, `remove` of that Instance succeeds. → `TestGuards` (table),
    `TestRemove_AfterRecordFixed`
12. Every verb that takes a name (`create` aside) returns `ErrNoInstance` for a name with no folder, with nothing
    changed. → `TestMissingInstance` (table, one row per verb)
13. `Remove` calls `Backup.PlanCreate` with `KindFinal`, `ComposeStop`, `Backup.Create` with `KindFinal`,
    `RemoveSite`, `ComposeDown`, `Store.Remove`, in that order, and returns where the Backups went; a `PlanCreate`
    refusal (free space included, exit 2) comes before `ComposeStop`, with the Instance still running and nothing
    changed, and `PlanRemove` returns it too. → `TestRemove_Order`, `TestRemove_NoSpaceBeforeStop`
14. When the final Backup fails, `Remove` starts again an Instance it stopped, leaves a stopped one stopped, and
    changes nothing else; a free-space refusal from `Create` is exit 2; with a cancelled context it still starts
    the Instance again, on a context that is not cancelled. → `TestRemove_BackupFails` (table, with a
    cancelled-context row)
15. A failure in `RemoveSite`, `ComposeDown` or `Store.Remove` leaves the Instance with its record, its data and
    `STATE` unchanged, its container stopped or gone, and a second `Remove` completes, taking its final Backup with
    no container to stop when `ComposeDown` had run. → `TestRemove_LaterFailure`
16. `RemoveOrphan` removes every container labelled with the name, also when the name is not a valid Instance
    name; it refuses a name with an Instance folder (valid or not) and a name with no labelled container. →
    `TestRemoveOrphan`
17. `List` returns every Instance sorted by name, with its record or `RecordError` and its live state, and "not
    created" when there is no container, from one `ListContainers` call. → `TestList`
18. `Status` reports wanted vs. actual disagreements, files out of date for an older `TOOL_VERSION`, for a
    `compose.yaml` that differs from the rendered one, for a missing site file and for a site file whose
    `ParseSite` Hostname is not the record's `HOSTNAME`, the newer-`TOOL_VERSION` warning, interrupted Upgrades and
    Restores, Orphans (not the containers of an invalid-record folder), invalid records, the Proxy's `Behind`, an
    update when `LatestActual` is newer, `CaddyUpdate` when `LatestCaddy` is newer than the Proxy's version,
    `VersionsStale` when `VersionsFetchedAt` is over 7 days old, and a `Proxy.Status` error as one Problem labelled
    `proxy` while `Status` still succeeds. → `TestStatus` (table)
19. `Status` with a nil reader or `ErrNoHealthResults` shows "no Health check results yet"; with a read error it
    succeeds and adds one Problem; otherwise it shows the Problems and `CheckedAt` as given. → `TestStatus_Health`
20. `Logs` passes `Follow` and `Tail` to `docker.Logs` of `abyssctl-<name>`, works on an invalid record, and
    refuses an Instance with no container; `logs proxy` reads the Proxy container. → `TestLogs`;
    `TestLogsCmd_Proxy` in `internal/cli`
21. `Exec` runs `/bin/sh` with no command and the given command after `--`, refuses a container that is not running
    (hint `abyssctl start <name>`), and returns `*docker.ExitError` for a non-zero exit. → `TestExec`
22. `ResetPassword` runs `node /app/src/scripts/reset-password.js` with a TTY; it refuses (exit 2) when
    `stdio.TTY` is false and on a container that is not running. The CLI sets `TTY` only when stdin and stdout
    are terminals. → `TestResetPassword`; `TestResetPasswordCmd_TTY` in `internal/cli`
23. Each `Plan…` returns the same refusal as its run, and takes no lock, writes nothing, pulls nothing and starts
    nothing. → `TestPlansMatchRuns` (table)
24. No event field from any Lifecycle operation holds container output or input. → `TestEvents_NoPassthrough`
25. `internal/lifecycle`'s direct imports are only those in the import rule, and only `internal/cli` imports it.
    → `TestImportRules` in `internal/archtest`
26. The CLI takes, per verb: the Host lock for `create` (`Create` then takes the new Instance's lock) and
    `remove --orphan`; the Host lock, then the Instance lock, for `remove`, `start` and `restart`; the Instance
    lock for `stop` and `reset-password`; none for `list`, `status`, `logs`, `exec` and any `--dry-run`. `start`
    and `restart` release the Host lock through `Options.AfterSite`, so another Host-lock command runs while they
    wait for health; `create` and `remove` hold it until they return. → `TestLifecycleCmds_Locks` (table),
    `TestStartCmd_ReleasesHostLockAfterSite` in `internal/cli`
27. `stop` and `restart` ask y/N, `remove` and `remove --orphan` ask for the typed name, `--yes` skips both, and
    with no TTY and no `--yes` each is refused (exit 2) with nothing changed. → `TestLifecycleCmds_Prompts` (table)
    in `internal/cli`
28. `--dry-run` on `create`, `remove`, `remove --orphan`, `stop` and `restart` prints the panel, takes no lock
    and changes nothing. → `TestLifecycleCmds_DryRun` (table) in `internal/cli`
29. `list --json` and `status --json` carry `"schema": 1`, snake_case keys, RFC 3339 UTC times and
    `"internet": true|false` for each Instance with a valid record. →
    `TestListCmd_JSON`, `TestStatusCmd_JSON` in `internal/cli`
30. `status` exits 0 whatever it shows, including Problems, an invalid record and a `Proxy.Status` error, and 1
    only when Store or Docker can't be read; its Host line shows `↑ caddy <v> → <latest>` and "version info
    stale" from the Report. → `TestStatusCmd_ExitCodes`, `TestStatusCmd_HostLine` in `internal/cli`
31. `reset-password` writes its header and one `reset_password` event to the activity log, never its input or
    output; `exec` writes a `begin` line with the masked command line and an `end` line with the exit code, never
    the container's input or output; `list`, `status` and `logs` write nothing there. →
    `TestResetPasswordCmd_Log`, `TestExecCmd_Log` in `internal/cli`
32. On the interim path, `create` without `--version` is refused (exit 2), and with it the CLI calls
    `CheckVersion`, then `Pull(<repo>:<version>)` and `Image.DigestFor(repo)`, before the Host lock; `create
    --dry-run` pulls nothing, and its panel shows "image digest: resolved at run time". →
    `TestCreateCmd_InterimVersion` (table, with a `--dry-run` row) in `internal/cli`, replaced at build step 9 by
    [versions.md](versions.md)'s `TestCreateCmd_ResolvesVersion`
33. The part of `backup.PlanCreate` and `backup.Create` built here takes a verified `final` Backup of a stopped Instance without
    starting it, of a running one and starts it again (also when the copy fails), and of a folder with no
    container; it deletes leftover `.partial` files in `backups/<name>/` first, and refuses when free space is
    below the Backup's need (backup.md's rule). → the `Create` tests for `KindFinal` in `internal/backup` ([backup.md](backup.md))
34. Journey 3: `create kids --version <journey pin>` and `create bob --version <journey pin> --hostname
    money.<base domain> --internet` exit 0; `list --json` and `status --json` show both running, healthy and wanted running.
    → journey 3
35. Journey 3: each Instance container carries the label `io.abyssctl.instance=<name>`, runs as the `abyssctl`
    account's `UID:GID`, drops all capabilities and publishes no port; `kids`, created without `--internet`, is
    only on the `abyssctl` network, and `bob`, created with it, is on `abyssctl` and `abyssctl-external`; the Proxy
    is on both and runs `read_only` with only `NET_BIND_SERVICE`. → journey 3
36. Journey 3: `instances/kids/` holds `instance.env`, `compose.yaml`, `.lock` and `data/` at the Host layout's
    owners and modes, and `data/` is owned by the container's user. → journey 3
37. Journey 3: `stop kids` leaves it exited and wanted stopped; `start kids` brings it back healthy; `restart kids`
    gives a later start time; `logs kids --tail 5` prints at most five lines; `exec kids -- id -u` prints the
    account's UID; `exec kids -- sh -c 'exit 7'` exits 7. → journey 3
38. Journey 3: a file in `kids`'s `data/` changed to root:root by hand is owned by `abyssctl:abyssctl` again after a
    `host setup` re-run, with its mode unchanged. → journey 3
39. Journey 3: `remove kids --yes` leaves no `instances/kids/`, no container and no site file, and a verified
    `final` Backup with its `.sha256` under `backups/removed/kids-<stamp>/`; a labelled container started by hand
    shows in `status` as an Orphan and `remove --orphan` of its name removes it. → journey 3
40. Journey 2: with an Instance created by the stamped v0.0.1 binary, `host setup` from the build under test
    replaces the v0.0.1 binary and the units with the build's bytes, the Proxy container keeps its ID when its
    `compose.yaml` bytes did not change, and the Instance keeps its container ID, and `status --json` flags its files out of date; `restart` clears the flag; then a changing command
    from the v0.0.1 binary on that Instance exits 4 and changes nothing, and the v0.0.1 binary's `host setup` is
    refused with exit 2 and changes nothing. → journey 2
41. Journey 4: `host setup --cert/--key` with a wildcard from the test CA, then two Instances, each reached through
    the Proxy with `curl --resolve <hostname>:443:127.0.0.1 --cacert <test CA>`; a Hostname outside every
    Certificate fails the handshake; a covered Hostname with no site gets an empty 200; port 80 redirects to HTTPS.
    → journey 4
42. Journey 4: `cert install` of a Certificate that covers no Instance warns "covers no Instance yet"; `create
    --version <journey pin>` with a Hostname no Certificate covers exits 2 and leaves no folder, container or site
    file; `cert list` shows the wildcard used by both Instances and the other unused; `cert remove` of the wildcard
    is refused and of the unused one deletes its files; a renewed wildcard replaces the old one and both Hostnames
    serve it. → journey 4
43. `reset-password` on a running Instance prompts for the new password and the Instance accepts it at login. →
    manual step in the Release check (checked by release.md's Release check)
44. `PlanCreate`'s panel has the row Internet access `no (pass --internet for bank sync)` without `Internet` and
    `yes` with it, and no other prompt; with `Internet` the record has `INTERNET=1`, and without it no `INTERNET`
    line. → `TestCreate_InternetAccess` (table)
45. `Set` turning Internet access on and off, on a running Instance, writes `INTERNET` (or leaves it out), renders
    `compose.yaml` with the matching networks and trusted proxies, and calls `ComposeUp` with `Wait` and 5 min, so
    the container gets a new ID and is attached to exactly the record's networks; the data, `HOSTNAME`, `STATE`,
    `TOOL_VERSION` and the site file are unchanged. → `TestSet` (table)
46. `Set` on a stopped Instance, and on one with no container, writes the record and `compose.yaml` and calls no
    `ComposeUp`; the Instance stays stopped, and `PlanSet`'s panel says the change takes effect at the next
    `start`. → `TestSet_Stopped`
47. `PlanSet` and `Set` of the record's current value are `NoOp` and change nothing; turning it on with no
    `abyssctl-external` network, or with one whose `Free()` is 0, is refused (exit 2) with nothing changed;
    `ErrNotHealthy` after the recreate is exit 1 with the `logs` hint and leaves the record with the new value. →
    `TestSet_NoOp`, `TestSet_Refusals` (table), `TestSet_NotHealthy`
48. `List` carries each record's Internet access; `Status` reports a container whose networks differ from its
    record's, both ways, with the hint `abyssctl restart <name>`, and nothing for a container that matches. →
    `TestList_Internet`, `TestStatus_Networks` (table)
49. `set` asks y/N, `--yes` skips it, `--dry-run` prints the panel and changes nothing, it takes only the Instance
    lock, and `--internet` takes only `on` or `off` (anything else is refused, exit 2, before any lock). →
    `TestSetCmd` (table) in `internal/cli`
50. Journey 3: the harness listens on the Host's own non-loopback address. From inside `bob`, which has Internet
    access, a connection to that listener succeeds; from inside `kids`, which has none, it fails (no route, or a
    timeout), and a lookup of an external name gets no answer. After `set kids --internet=on --yes` and `set bob
    --internet=off --yes`, `kids` connects, `bob` fails and `bob`'s lookup gets no answer, and `list --json` shows each one's new `internet` value. → journey 3
51. Docs: the README lines, `backup-and-restore.md`, `upgrades.md`, `security.md`, `teardown.md` and the reference
    pages say what [Docs it changes](#8-docs-it-changes) lists. → (review)
