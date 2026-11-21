# Spec: host

Build step 6 of 16. The [overview](README.md) has the module map, the call graph and the Host layout. Shared
packages (`internal/hostfs`, `internal/op`, `internal/buildinfo`) are in [foundation.md](foundation.md); the Docker
adapter and locks are in [docker-and-locks.md](docker-and-locks.md). Capitalised terms are defined in
[GLOSSARY.md](../../GLOSSARY.md).

## 1. Purpose

Build the Host module (`internal/host`) and the `host setup` verb: [Bootstrap](../../GLOSSARY.md), its
re-runs, and the [Tool update](../../GLOSSARY.md), which is a re-run of a newer binary. Host owns:

- the Host checks: OS and architecture, concrete conflicts (ports 80 and 443, the `abyssctl` network and the
  Proxy's name, a Data root the tool doesn't own), Docker present and new enough;
- installing Docker from Docker's official apt repository when it is missing, and the few other packages;
- the `abyssctl` system account that every [Instance](../../GLOSSARY.md) container runs as;
- `/etc/abyssctl/abyssctl.env`: its format, every key, its defaults and checks. Host is its only writer; `alerts
  setup` writes the Pushover keys through Host;
- the folders under the [Data root](../../GLOSSARY.md), `/etc/abyssctl/` and `/var/log/abyssctl/`, and every
  owner and mode in the overview's [Host layout](README.md#host-layout);
- installing the binary at `/usr/local/bin/abyssctl`, and refusing to replace a newer one;
- the private network `abyssctl`;
- installing the systemd units, the [MOTD](../../GLOSSARY.md) script and the logrotate rule that the
  [Renderer](render.md) produces, and keeping the two timers enabled;
- drift fixes: owners, modes, the account, the units, and an Instance container whose networks or published
  ports have drifted.

Host starts the [Proxy](../../GLOSSARY.md) and installs a [Certificate](../../GLOSSARY.md) given with
`--cert`/`--key` by calling the Proxy module, which owns the Caddyfile, the Proxy's `compose.yaml`, `proxy.env`,
site files, Certificates and the Proxy container's own drift ([proxy.md](proxy.md)).

Inside, Host works in three stages: **inspect** (read the Host, change nothing), **plan** (from what was observed
and the options, decide each change) and **apply** (make the changes in a fixed order). Module tests cover the plan
against observed states; journeys cover apply on a real Host. When this spec is done, `sudo ./abyssctl host setup
--base-domain budget.example.com` turns a fresh supported Host into one where the Proxy runs with no sites, the
timers are enabled and every later module has its folders, settings and account, and journey 1 passes.

## 2. Interface

Signature sketches, not final code.

```go
// Settings are the values in abyssctl.env, defaults filled in.
type Settings struct {
    DataRoot       string        // DATA_ROOT, absolute and clean; fixed after the first host setup
    BaseDomain     string        // BASE_DOMAIN, a valid Hostname; fixed after the first host setup
    HealthInterval time.Duration // HEALTH_INTERVAL, whole minutes, 1–60
    DiskMinFree    int           // DISK_MIN_FREE, a percentage of the Data root's file system, 1–90
    CertWarnDays   int           // CERT_WARN_DAYS, 1–90
    BackupTime     string        // BACKUP_TIME, "HH:MM", Host local time
    BackupKeep     int           // BACKUP_KEEP, 1–1000
    PushoverUser   string        // PUSHOVER_USER, "" when not set; a Secret
    PushoverToken  string        // PUSHOVER_TOKEN, "" when not set; a Secret
    Extra          []KV          // unknown keys, kept in file order
    Warnings       []string      // from parsing (an unknown key, a comment, a blank line); never written
}

// ReadSettings reads abyssctl.env through p.SettingsFile(). p needs no Data root. ErrNotSetUp (matching
// op.ErrRefused, hint "abyssctl host setup") when the file is missing; a *SettingsError (matching
// op.ErrNeedsOperator, exit 4, hint "fix /etc/abyssctl/abyssctl.env by hand") naming each bad line otherwise. The CLI shows Warnings.
func ReadSettings(p hostfs.Paths) (Settings, error)

var ErrNotSetUp = errors.New("this Host is not set up")

type SettingsError struct{ Problems []string } // one per bad line or key, "line 4: HEALTH_INTERVAL: …"

// What host setup was asked to do. Every string is as the Operator typed it; "" means "not given, keep the
// current value" (or the default on a first run).
type Options struct {
    BaseDomain     string // required on a first run
    DataRoot       string // default /srv/abyssctl on a first run
    CertFile       string // with KeyFile, or neither
    KeyFile        string
    PushoverUser   string // with PushoverToken, or neither
    PushoverToken  string // read from stdin by the CLI (--pushover-token -); never a command-line value
    HealthInterval string // "5m"
    DiskMinFree    string // "10%"
    CertWarnDays   string
    BackupTime     string // "03:00"
    BackupKeep     string
    Unsupported    bool   // go on with a warning on an OS or architecture not on the supported list
    Executable     string // os.Executable() of the running binary, resolved; the file copied to Binary()
    // Lock is set by the CLI only when the Host lock's folder (state/) doesn't exist yet. Setup calls it once,
    // right after apply step 4 has created the folders, with the Paths that carry the Data root, and the CLI
    // takes its locks there. nil when the CLI already holds the locks.
    Lock func(hostfs.Paths) error
}

// The part of the Proxy module Host calls: Host's test seam. *proxy.Proxy satisfies it; module tests use a fake.
type Proxy interface {
    // PlanEnsure says what Ensure would do; it writes nothing, starts nothing and pulls nothing. Host uses its
    // Steps and Warnings; on a Host without Docker or without proxy/ it returns the first-run steps.
    PlanEnsure(ctx context.Context) (op.Summary, error)
    // Ensure writes the Caddyfile, the Proxy compose.yaml and (only if missing) proxy.env from the compiled pin,
    // and brings the Proxy up, ready, on the abyssctl network with only 80 and 443 published.
    Ensure(ctx context.Context) error
    // CheckCertificate runs cert install's checks on the two files and changes nothing.
    CheckCertificate(certFile, keyFile string) (summary []op.Row, err error)
    // InstallCertificate is cert install: copy, re-render the site files whose chosen Certificate changes,
    // validate and reload. hostnames are the Hostnames of every Instance.
    InstallCertificate(ctx context.Context, certFile, keyFile string, hostnames []string) error
}

type Deps struct {
    Paths    hostfs.Paths             // always hostfs.System() (Under() in tests): never with a Data root
    Docker   docker.Client
    NewProxy func(hostfs.Paths) Proxy // builds the Proxy on the Paths that carry the Data root
    OS       System                   // the real Host in production; hostfake in module tests
    Emit     op.Emitter
    Clock    op.Clock
    Pins     buildinfo.Pins           // buildinfo.CompiledPins()
    Version  string                   // buildinfo.Version
}

func New(d Deps) *Host

// PlanSetup inspects the Host and returns the summary panel. It takes no lock, writes nothing and runs no
// command that changes anything. It returns the same refusals Setup would.
func (h *Host) PlanSetup(ctx context.Context, o Options) (op.Summary, error)

// Setup inspects again, plans and applies. Precondition: the caller holds the Host lock and every Instance lock,
// or o.Lock is set (see Behaviour).
func (h *Host) Setup(ctx context.Context, o Options) error

// SetPushover writes PUSHOVER_USER and PUSHOVER_TOKEN into abyssctl.env and changes nothing else. Both must be
// valid Pushover keys. Precondition: the caller holds the Host lock. Used by alerts setup (alerts.md).
func (h *Host) SetPushover(user, token string) error

// ValidPushoverKey reports whether s is exactly 30 characters of [A-Za-z0-9], the form of a Pushover user or
// application key. The one key-format rule: ReadSettings, SetPushover and the CLI's Pushover flags use it.
func ValidPushoverKey(s string) bool

// Account returns the abyssctl account's actual UID and GID, read from the Host's account database.
// ErrNoAccount (matching op.ErrRefused, hint "abyssctl host setup") when it is missing.
func (h *Host) Account() (store.Owner, error)

var ErrNoAccount = errors.New("the abyssctl account is missing")

// System is everything Host does to the Host outside Docker and hostfs writes. Its production adapter uses
// os, os/exec and syscall; its fake records calls and returns a scripted state.
type System interface {
    OSRelease() (map[string]string, error)                 // /etc/os-release, parsed
    Arch() string                                          // runtime.GOARCH
    Listening(port int) (bool, error)                      // any TCP listener on port, IPv4 or IPv6
    LookupUser(name string) (User, bool, error)            // from the account database
    LookupGroup(name string) (Group, bool, error)
    PackageInstalled(pkg string) (bool, error)             // dpkg-query
    AptSources() ([]AptSource, error)                      // the files in /etc/apt/sources.list{,.d/}
    Run(ctx context.Context, cmd string, args ...string) error // apt-get, useradd, groupadd, usermod, systemctl
    Output(ctx context.Context, timeout time.Duration, cmd string, args ...string) ([]byte, error) // read-only
    Lstat(path string) (Meta, error)                       // owner, mode, kind; never follows a symlink
    Walk(root string, fn func(path string, m Meta) error) error // depth-first, never follows a symlink
    Chown(path string, uid, gid int) error                 // lchown
    Chmod(path string, mode fs.FileMode) error
}

type User struct {
    UID, GID    int
    Shell, Home string
}

type Group struct{ GID int }

type AptSource struct {
    Path string
    URIs []string
}

type Meta struct {
    UID, GID int
    Mode     fs.FileMode // permission bits only
    Kind     string      // "dir", "file", "symlink", "other"
}
```

### Invariants and error modes

- **Refusals come before any change.** Every refusal below is found by inspect and returned as `op.Refused`
  (exit 2) or, for an Instance newer than the binary and for an invalid `proxy.env` that Proxy finds
  ([proxy.md](proxy.md)), `op.NeedsOperator` (exit 4), with nothing changed. Once
  apply starts, a failure is a plain error (exit 1) that names the step; the Host is left part-way, and a re-run
  carries on from what it finds.
- **`PlanSetup` and `Setup` agree.** `Setup` returns the same refusals as `PlanSetup` for the same Host and
  options, and performs exactly the steps the plan listed, in the same order. `Setup` inspects again once it holds
  the locks, so it acts on what it finds then.
- **A re-run with nothing drifted is a no-op.** `PlanSetup` returns `NoOp: true`, and `Setup` writes no file,
  runs no command that changes anything and emits only the `checked` event.
- **No [Secret](../../GLOSSARY.md) leaves Host.** Pushover values appear only as `op.Mask(value)` in the summary
  and in events. A `SettingsError` names the key and the line, never a Pushover value.
- **`ReadSettings` is the only reader of `abyssctl.env`.** For every other command the CLI calls it, then
  `Paths.WithDataRoot(s.DataRoot)`, and hands the values to the modules that need them (`DiskMinFree`,
  `CertWarnDays` and `BaseDomain` to Health, `BackupKeep` to Backup, the Pushover pair to Alerts, `BaseDomain` to
  Lifecycle; `HealthInterval` and `BackupTime` are used only by `host setup`'s timer units). `ErrNotSetUp` stops every command except `host setup`, `version` and help.
- **Host builds its own Store and Proxy.** `Deps.Paths` never carries a Data root. Once inspect has settled the
  Data root (from `abyssctl.env`, or the options on a first run), Host calls `Paths.WithDataRoot`, then
  `store.New(p, Clock, Version)` and `NewProxy(p)`, and uses those for the run.
- **`Account` is how other modules learn the account's IDs.** The CLI passes them on: to Lifecycle for
  `store.NewInstance.DataOwner` and `render.Instance.UID`/`GID`, and to Backup for the owner after a Restore.
  Nothing else looks the account up. Host never stores the IDs in a file: the account database is the record.
- **`SetPushover`** refuses (exit 2) a user or token that is not exactly 30 characters of `[A-Za-z0-9]`, and a
  missing `abyssctl.env` with `ErrNotSetUp`. It rewrites the file with the other keys unchanged.

## 3. Behaviour

### What the CLI does around Host

1. `host setup` has the flags `--base-domain`, `--data-root`, `--cert`, `--key`, `--pushover-user`,
   `--pushover-token -`, `--health-interval`, `--disk-min-free`, `--cert-warn-days`, `--backup-time`,
   `--backup-keep`, `--unsupported`, `--dry-run` and `--yes`. An omitted flag keeps the current value.
2. With `--pushover-token -` the CLI reads one line from stdin and strips its trailing newline. On a TTY with
   `--pushover-user` and no `--pushover-token`, it prompts for the token with echo off. With no `abyssctl.env`
   and no `--base-domain`, it prompts for the Base domain on a TTY and refuses (exit 2) without one. These are
   the only input prompts, and each has its flag.
3. The CLI calls `PlanSetup` and prints the summary panel. With `--dry-run` it stops there (exit 0). If the plan
   is `NoOp`, it prints the outcome line and stops (exit 0). Otherwise it asks y/N; `--yes` answers yes, and with
   no TTY and no `--yes` it refuses (exit 2). When stdin carries the token, the y/N needs `--yes` unless [cli.md](cli.md)'s
   `CanPrompt` is true (an input terminal at `/dev/tty`).
4. **Locks.** If `state/` exists (read from `abyssctl.env`'s `DATA_ROOT`), the CLI takes the Host lock, then the
   lock of every Instance in name order, before calling `Setup`. It skips `store.List` entries whose folder name is
   not a valid Instance name. If `abyssctl.env` is missing, or names a Data root with no `state/` folder, the CLI
   sets `Options.Lock` instead: `Setup` runs apply steps 1–4, then calls the hook, and the CLI builds the `Locker`
   on the new Paths and takes the same locks there.
5. Two first runs at the same time can overlap in steps 1–4. The one that loses (a `useradd` for an account that
   now exists, or the Host lock not obtained in 30 s) fails with exit 1, and a re-run finishes the Host.
6. **Activity log.** The CLI keeps the run's log lines in memory until `/var/log/abyssctl/` exists (apply step 4),
   then writes them to the log and goes on appending. A first run refused or failed before the folder exists is
   shown on screen only ([cli.md](cli.md)).

### Inspect

Inspect reads, in this order, and stops at the first refusal:

1. **OS and architecture.** From `/etc/os-release`: `ID=debian` with `VERSION_ID=13`, or `ID=ubuntu` with
   `VERSION_ID` `24.04` or `26.04`. TurnKey Core 19 reports its Debian base there and is detected as Debian 13.
   Architecture is `amd64` or `arm64`. Anything else is refused ("Ubuntu 22.04 is not a supported Host"), unless
   `--unsupported`, which turns the refusal into a warning.
2. **Settings.** Read `abyssctl.env` if it exists; a broken file stops the run with the `SettingsError` (exit 4), because
   Host can't know the Data root. Merge the options over the file (or over the defaults on a first run) and check
   every value ([Files and formats](#etcabyssctlabyssctlenv)). On a re-run, a `--data-root` or `--base-domain`
   different from the file's is refused ("the Data root is fixed after the first host setup"). On a first run,
   a missing Base domain is refused.
3. **Data root ownership.** The folder is the tool's only when `abyssctl.env` names it. With no `abyssctl.env`,
   a Data root that exists and is not empty is refused ("/srv/abyssctl is not empty and is not an Abyss Data
   root"). An empty or missing folder is fine.
4. **Binary.** If `Options.Executable` is `Binary()` itself, nothing is copied. Otherwise, if `Binary()` exists,
   Host runs `Binary() version --json` with a 10 s timeout and compares its `version` with `Deps.Version` using
   `buildinfo.CompareVersions`. An installed binary that is newer is refused ("/usr/local/bin/abyssctl is 0.3.0,
   newer than this binary (0.2.0)", hint: download 0.3.0 or newer). The same version with identical bytes is no
   change; the same version with different bytes is replaced. A file there that doesn't run, times out or
   doesn't print a valid version is replaced, with a warning.
5. **Instances newer than the binary.** Store lists the Instances. One whose `TOOL_VERSION` is newer than
   `Deps.Version` stops the run with `op.NeedsOperator` (exit 4) naming it, hint "install abyssctl
   <TOOL_VERSION> or newer". The check reads the record's `TOOL_VERSION=` line before full validation, so a newer
   record that would not parse stops the run too. An otherwise invalid record is a warning, not a refusal:
   `host setup` must still fix the Host.
   An Instance with a `data.pre-restore/` folder or an `UPGRADE_BACKUP` key is not refused either; Host only
   fixes its owners and modes, and its container's drift as below.
6. **Docker.** `Docker.Versions`. `ErrNotInstalled` or `ErrNoCompose` plans the Docker install, which needs
   `ID=debian` or `ID=ubuntu` (under `--unsupported`, any other `ID` is refused: "install Docker yourself"). An
   apt source that already names `download.docker.com` while Docker is missing is refused ("<path> already
   points at Docker's repository", hint: remove it, or install Docker from it yourself). `ErrDaemon` is refused
   with the hint `systemctl start docker`. Versions below the minimums are refused with `docker.CheckVersions`'s
   error wrapped in `op.Refused`; Host never upgrades Docker.
7. **Conflicts.** For ports 80 and 443, `System.Listening`. A listener counts as the Proxy's only when the
   container `abyssctl-proxy` is running and publishes that port; any other listener is refused ("port 443 is in
   use by another service"). With no `abyssctl.env`, an existing network `abyssctl` or container `abyssctl-proxy`
   is refused; once `abyssctl.env` exists, both are the tool's. Containers without the `io.abyssctl.instance`
   label and not `abyssctl-proxy` (`ListContainers(ctx, "")`) are counted for a warning ("4 other containers run on this Host").
8. **Certificate.** With `--cert`/`--key`, `Proxy.CheckCertificate`; its refusals pass through. One of the two
   flags without the other is refused.
9. **Account.** `LookupGroup("abyssctl")` and `LookupUser("abyssctl")`, each on its own: a missing group plans
   `groupadd`, a missing user plans `useradd`. A user or group with an ID of 0 or 1000 or above is refused ("the
   abyssctl account exists and is not a system account"). A wrong shell, home or primary group is drift.
10. **Account IDs under running Instances.** A running Instance container whose `User` is not the account's
    `UID:GID` gives a panel warning, shown before the y/N: "<name> runs as <old> and cannot write its data until
    `abyssctl restart <name>`". Host still fixes the owners of its `data/` and does not recreate it.
11. **Drift.** Every owner and mode in the Host layout, the units, the MOTD script, the logrotate rule, the timers'
    state, the network, the Proxy (`Proxy.PlanEnsure`), and each running Instance container's networks and
    published ports. An Instance container is drifted when its networks are not exactly `{abyssctl}` or it
    publishes any port. Host first scans that Instance's `compose.yaml` line by line (not a YAML parse) for a
    `ports:` key or a network name other than `abyssctl`; the Renderer never writes either. If the file has one,
    it was edited by hand: Host warns ("compose.yaml of <name> was edited by hand; abyssctl restart <name>
    re-renders it") and does not recreate, so a hand edit doesn't cause a recreate on every run.

### Plan and the summary panel

The summary's title is "Set up this Host" on a first run and "Check this Host" on a re-run. Its rows are: Host
(`Debian 13 amd64`), Docker (`27.3.1 / Compose 2.29.7`, or `will be installed`), Data root, Base domain, Health
check (`every 5 min`), Backups (`03:00, keep 14`), Pushover (`…abcd` or `not set`), Certificate (the names it
covers, or `none given`), Binary (`0.2.0 → 0.3.0`, `0.3.0`, or `installed by this run`). Its steps are the apply
steps that will change something, one line each, including each drift fix and the Proxy's own steps from
`PlanEnsure`'s `Steps` (for example "recreate the Proxy: every Instance unreachable for a few seconds"). Its
warnings are `PlanEnsure`'s `Warnings` (for example a Proxy behind the compiled-in Caddy pin) and:
an unsupported Host, other containers, Instances running as old account IDs, hand-edited Instance compose files,
invalid Instance records, no Pushover ("No Alerts are sent (Problems show on the MOTD); run `abyssctl alerts setup`"), and no
Certificate on a first run ("create fails until abyssctl cert install"). `NoOp` is true when there is no step.

### Apply

Each step runs only if the plan lists it, and each change is one event. The order is fixed:

1. **Docker and packages.** `apt-get update` runs once before any `apt-get install`, also when only one of
   `ca-certificates`, `curl` and `openssl` is missing. If Docker is missing: first write Docker's repository key,
   embedded in the binary, to `/etc/apt/keyrings/docker.asc` (0644), and a deb822 source to
   `/etc/apt/sources.list.d/docker.sources` (0644) with `URIs: https://download.docker.com/linux/<debian|ubuntu>`,
   `Suites: <VERSION_CODENAME>`, `Components: stable`, the dpkg architecture and `Signed-By:` the key file. Then
   `apt-get install -y` the missing ones of `docker-ce docker-ce-cli containerd.io docker-compose-plugin
   ca-certificates curl openssl`, with `DEBIAN_FRONTEND=noninteractive`, and check `Versions` again. Events:
   `install package=docker-ce …`.
2. **Account.** `groupadd --system abyssctl` if the group is missing; `useradd --system --gid abyssctl
   --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin abyssctl` if the user is missing. Drift:
   `usermod --shell /usr/sbin/nologin`, `usermod --home /nonexistent`, `usermod --gid abyssctl`. The Operator is
   not added to the `docker` group.
3. **Settings.** `EnsureDir(/etc/abyssctl, 0700)`, then `WriteFile(abyssctl.env, 0600)` when the bytes would
   change. This comes before anything under the Data root, so from here on the Data root is the tool's.
4. **Folders.** `EnsureDir` at 0700 for the Data root, `instances/`, `backups/`, `backups/removed/`, `proxy/`,
   `proxy/sites/`, `proxy/certs/`, `proxy/caddy-data/`, `proxy/caddy-config/` and `state/`; and
   `/var/log/abyssctl/` at 0750, owner root:adm. If `Options.Lock` is set, Setup calls it now; an error from it
   ends the run (exit 1).
5. **Sweep.** `store.Sweep()` deletes leftover `.creating` and `.removing` folders; each path is one event
   `swept path=…`.
6. **Owners and modes.** A walk of the Data root sets every folder to root:root 0700 and every file to root:root
   0600, except:
   - `instances/<name>/data/` and `instances/<name>/data.pre-restore/`: the folder itself becomes
     `abyssctl:abyssctl` 0700, and every path inside gets owner `abyssctl:abyssctl` with its mode left as Actual
     wrote it;
   - the contents of `proxy/caddy-data/` and `proxy/caddy-config/`, which are Caddy's own state and are left
     alone (the folders themselves are fixed);
   - symlinks, which are never followed or changed and give a warning.

   Outside the Data root: `/etc/abyssctl/` root:root 0700, `abyssctl.env` 0600, `/var/log/abyssctl/` root:adm
   0750, and an existing `abyssctl.log` root:adm 0640. Each fix outside `data/` is one event, `fixed what=mode
   path=… was=0644 now=0600` (or `what=owner … was=1000:1000 now=0:0`). Inside a `data/` folder, one event per
   folder counts the paths whose owner changed: `fixed what=owner path=…/data count=12`. Contents are never read or
   changed.
7. **Binary.** `WriteFile(Binary(), <bytes of Options.Executable>, 0755)`, root:root. The rename means a running
   copy keeps its old file. Event: `installed path=/usr/local/bin/abyssctl version=0.3.0 was=0.2.0`.
8. **Network.** `CreateNetwork("abyssctl", nil)` if missing, then `Network` reads back its subnets for the
   event (`network name=abyssctl subnets=172.18.0.0/16`).
9. **Proxy.** `Proxy.Ensure`. On a first run this seeds `proxy.env` from the compiled Caddy pin and starts the
   Proxy with no site files. On a re-run Proxy rewrites the files this binary renders: a changed Caddyfile is
   validated and reloaded, and the Proxy container is recreated only when its rendered `compose.yaml` bytes
   changed or its networks or ports drifted ([proxy.md](proxy.md)). The Proxy's version moves only through
   `proxy upgrade`.
10. **Instance containers.** For each drifted running Instance container (step 11 of inspect) whose
    `compose.yaml` was not edited by hand: `ComposeUp(Project{abyssctl-<name>, InstanceDir(name)},
    UpOptions{Wait: true, WaitTimeout: 5 min, ForceRecreate: true})` from its existing `compose.yaml`, with the
    event `recreated instance=<name> reason=network` (or `reason=ports`). No Instance's `compose.yaml` or site
    file is rewritten, and no other container is touched. A stopped Instance is left stopped: its next `start`
    re-renders it.
11. **Units, MOTD script, logrotate rule.** From `render.Units(Schedule{Binary(), HealthInterval, BackupTime})`
    write each `SystemdUnit(name)` at 0644; `render.MotdScript(Motd{MotdText(), HealthInterval})` to
    `MotdScript()` at 0755; `render.LogrotateRule(LogFile())` to `LogrotateRule()` at 0644. Each is written only
    when its bytes differ. If a unit was written: `systemctl daemon-reload`, then `systemctl restart` for each
    timer whose file changed. Then, every run, for each of the two timers: `systemctl unmask --runtime` if it is
    masked at runtime, and `systemctl enable --now` if it is not enabled or not active.
12. **Certificate.** With `--cert`/`--key`: `Proxy.InstallCertificate` with the Hostnames from `store.List`.

The run ends with the outcome line from the CLI: "✓ This Host is set up (Abyss 0.3.0)" or "✓ Nothing to fix".

### Tool update

A Tool update is the same command run from a newer binary, a newer [Release](../../GLOSSARY.md): download,
`sha256sum -c`, `sudo ./abyssctl host setup`. Apply step 7 replaces the installed binary, and steps 9 and 11
rewrite the Host-level files this version renders. No Instance container is recreated by the update itself, except to fix drift (Apply step 10):
Instance files are regenerated, and `TOOL_VERSION` updated, on that Instance's next `start`, `restart`, `upgrade`
or `restore` ([lifecycle.md](lifecycle.md)), and `status` and the MOTD flag the rest
([lifecycle.md](lifecycle.md), [health.md](health.md)). The Proxy is recreated only when this version renders different Proxy `compose.yaml`
bytes, and the panel says so before the y/N. An older binary run on a Host with a newer one installed is refused
in inspect step 4 with nothing changed.

### Leaving the Host alone

Host never changes the firewall, SSH, unattended-upgrades, the hostname, the time zone or `daemon.json`, never
upgrades or reinstalls Docker, never adds anyone to the `docker` group, never reads or changes the contents of
Instance data or Backups, and never removes a container, a network or an image. Rootless Docker and userns-remap
are not supported.

## 4. Files and formats it owns

| Path | Mode | Written |
|---|---|---|
| `/etc/abyssctl/abyssctl.env` | root:root 0600 | `Setup` and `SetPushover`, only when the bytes change |
| `/usr/local/bin/abyssctl` | root:root 0755 | apply step 7 |
| `/etc/systemd/system/abyssctl-{health,backup}.{service,timer}` | root:root 0644 | from `render.Units` |
| `/etc/update-motd.d/90-abyssctl` | root:root 0755 | from `render.MotdScript` |
| `/etc/logrotate.d/abyssctl` | root:root 0644 | from `render.LogrotateRule` |
| `/etc/apt/keyrings/docker.asc`, `/etc/apt/sources.list.d/docker.sources` | root:root 0644 | only when Host installs Docker; never touched again |

Host also creates and fixes every folder in the [Host layout](README.md#host-layout) and the owner and mode of
every path under the Data root. The files in those folders belong to the modules that write them. The activity
log file is created by the CLI ([cli.md](cli.md)); Host creates its folder.

### `/etc/abyssctl/abyssctl.env`

```
DATA_ROOT=/srv/abyssctl
BASE_DOMAIN=budget.example.com
HEALTH_INTERVAL=5m
DISK_MIN_FREE=10%
CERT_WARN_DAYS=14
BACKUP_TIME=03:00
BACKUP_KEEP=14
PUSHOVER_USER=uQiRzpo4DXghDmr9QzzfQu27cmVRsG
PUSHOVER_TOKEN=azGDORePK8gMaC0QOYAMyEEuzJnyUi
```

- `KEY=value`, one per line, `\n` endings, UTF-8, no quoting, no comments, no blank lines, at most 64 KiB. A
  value is everything after the first `=`. The file is never sourced by a shell and never handed to a container.
- Host writes the keys in the order above, then unknown keys in the order it read them. The Pushover keys are
  left out while unset. Two writes of the same settings give the same bytes.
- On reading: a blank line, or a line whose first character is `#`, is skipped with a warning ("comments are
  not kept; the next write drops them"). A line that is not `KEY=value`, a duplicate key, a control character or
  a bad value is a problem; every problem is reported, not just the first. An unknown key gives a warning and is
  kept. A missing optional key takes its default, and the next `host setup` writes it in.

| Key | Required | Default | Rule |
|---|---|---|---|
| `DATA_ROOT` | yes | `/srv/abyssctl` at Bootstrap | absolute, clean (`filepath.Clean` gives it back), only `[A-Za-z0-9._/-]`, not `/`, and not inside `/etc`, `/usr`, `/boot`, `/dev`, `/proc`, `/sys`, `/run`, `/tmp`, `/var/tmp`, `/var/log` or `/var/lib/docker` |
| `BASE_DOMAIN` | yes | none | a valid [Hostname](../../GLOSSARY.md) (`store.ValidHostname`) |
| `HEALTH_INTERVAL` | no | `5m` | `<n>m`, whole minutes 1–60, checked with `render.CheckSchedule` |
| `DISK_MIN_FREE` | no | `10%` | `<n>%`, n 1–90: the free space the Health check expects on the Data root's file system |
| `CERT_WARN_DAYS` | no | `14` | integer 1–90 |
| `BACKUP_TIME` | no | `03:00` | `HH:MM`, 24-hour, Host local time, checked with `render.CheckSchedule` |
| `BACKUP_KEEP` | no | `14` | integer 1–1000: the scheduled Backups [Retention](../../GLOSSARY.md) keeps |
| `PUSHOVER_USER` | no | unset | exactly 30 characters of `[A-Za-z0-9]`; set together with `PUSHOVER_TOKEN` |
| `PUSHOVER_TOKEN` | no | unset | exactly 30 characters of `[A-Za-z0-9]`; a Secret |

A `--health-interval` or `--backup-time` that `render.CheckSchedule` rejects is refused with
`op.Refused(msg).WithCause(err)`; a hand-edited value it rejects is a `SettingsError` problem. A hand edit of
`DATA_ROOT` or `BASE_DOMAIN` is read as written: moving the Data root or changing the Base domain is not
supported in v1, and the docs say so. Pushover is removed by deleting its two lines by hand.

### Package layout

```
internal/host/
  host.go       New, Deps, PlanSetup, Setup, SetPushover, Account
  settings.go   Settings, ReadSettings, parse and format of abyssctl.env
  inspect.go    the observed state
  plan.go       observed state + Options → steps and the summary
  apply.go      the steps, in order
  system.go     System, the production adapter
  docker.asc    Docker's apt repository key, embedded
  hostfake/     a System fake for tests (scripted OS, listeners, accounts, apt sources, file metadata; records commands)
```

## 5. Depends on

- [foundation](foundation.md): `hostfs.Paths`, `hostfs.WriteFile`, `hostfs.EnsureDir`; `op.Emitter`, `op.Clock`,
  `op.Summary`, `op.Mask` and the typed errors; `buildinfo.Pins`, `buildinfo.CompareVersions`.
- [docker-and-locks](docker-and-locks.md): `docker.Client` (`Versions`, `CheckVersions`, `Network`,
  `CreateNetwork`, `Container`, `ListContainers`, `ComposeUp`) and `docker/names`. Host does not import
  `internal/locks`: the CLI takes the locks.
- [store](store.md): `New`, `List` (Instance names, Hostnames and `TOOL_VERSION`), `Sweep`, `ValidHostname`,
  `Owner`.
- [render](render.md): `Units`, `CheckSchedule`, `MotdScript`, `LogrotateRule`.
- [proxy](proxy.md) (build step 5): `Ensure`, `PlanEnsure`, `CheckCertificate`, `InstallCertificate`, through the
  narrow `Proxy` interface above, which is also Host's test seam.

Import rule this spec adds to `internal/archtest`: `internal/host` imports only the standard library,
`internal/hostfs`, `internal/op`, `internal/buildinfo`, `internal/docker`, `internal/docker/names`,
`internal/store`, `internal/render` and `internal/proxy`. Only `internal/cli` imports `internal/host`.

## 6. Decisions

- Bootstrap is a verb of the tool, `host setup`, run as root, never a separate script and never inside a
  container ([ADR 0003](../adr/0003-single-static-go-binary.md)). It is idempotent, so the same command is
  install, drift fix, settings change and Tool update.
- A shared Host is allowed: only a concrete conflict refuses, and everything else is a warning. The Proxy owns
  ports 80 and 443 on all interfaces ([ADR 0001](../adr/0001-tool-owned-caddy-proxy.md)).
- An existing Docker that meets the minimums is used as is; Host installs Docker only when it is missing, from
  Docker's own repository with a key embedded in the binary, so the install needs no `gpg` and no key download.
  A binary built before Docker rotates that key can't install Docker; moving the key is its own commit, as a
  change to `pins.json` is.
- One system account for every Instance, with no login and no home, and no per-Instance accounts. Its IDs come
  from the account database each run, never from a file.
- Settings are written before the Data root's folders, so an interrupted first run is finished by a re-run
  instead of being refused for a non-empty Data root.
- A re-run never rewrites an Instance's `compose.yaml` or site file and never recreates a working Instance
  container. The one exception is a container whose networks or published ports have drifted, recreated from its
  existing `compose.yaml`: those two are the Host's isolation, not the Instance's version. A hand-edited file that
  asks for them is warned about instead, so it isn't recreated on every run.
- After a Tool update the Proxy is recreated only when its rendered `compose.yaml` changed, and the panel says so,
  because the Proxy's hardening belongs to the binary while its version moves only through `proxy upgrade`.
- `host setup` resets owners and modes on every path it owns each run, and inside `data/` and
  `data.pre-restore/` only the owner, because Actual sets its own file modes.
- An older binary never replaces a newer one, and there is no override flag in v1. An Instance written by a newer
  binary stops `host setup` with exit 4, as it stops every changing command.
- Changing the Data root or the Base domain on a re-run is refused, because every Instance's Hostname, folder and
  Backups depend on them.
- `host setup` asks y/N after its panel when it would change something, like `upgrade` and `image prune`, so a
  first look at a Host shows the plan before acting.
- No new dependency: `os`, `os/exec`, `io/fs`, `syscall`, `embed` and `strconv` from the standard library. Package
  installs and account changes use the Host's own `apt-get`, `useradd`, `groupadd`, `usermod` and `systemctl`.

## 7. Later

- `host teardown`. In v1 `docs/guides/teardown.md` lists the manual steps.
- Moving the Data root, and changing the Base domain.
- A `.deb` package and an apt repository for Abyss (with [release.md](release.md)).
- ROADMAP Next's Operator-run Tool update command builds on this re-run.

## 8. Docs it changes

- README: **Requirements** (supported Hosts, root, ports 80 and 443 free, a Base domain with DNS pointing at the
  Host, the Operator's own Certificate, and that a Host without Docker gets it from `host setup`, from Docker's
  apt source), **Install** (download `abyssctl-linux-amd64` or `abyssctl-linux-arm64`
  and `SHA256SUMS` → `sha256sum -c --ignore-missing` → `chmod +x` → `sudo ./abyssctl-linux-<arch> host setup
  --base-domain …`, then the download can be deleted), the `host setup` line of **Quickstart**, and **Updating
  Abyss** (the same block, a downgrade is refused, the Proxy may restart for a few seconds, and no Instance container
  is recreated, except to fix drift, until the next action on each Instance).
- `docs/guides/security.md`: the root-only layout and the `abyssctl` account; that Host leaves the firewall,
  SSH, OS updates and Docker daemon hardening to the Operator; that rootless Docker and userns-remap are not
  supported; that Docker-published ports bypass ufw; that `abyssctl.env` holds the Pushover token at 0600 and must
  not be pasted into an issue; that a re-run resets owners and modes.
- `docs/guides/alerts.md`: `HEALTH_INTERVAL` (whole minutes, 1–60), `DISK_MIN_FREE` (a percentage) and
  `CERT_WARN_DAYS`; for the Pushover keys, a pointer to the Pushover part ([alerts.md](alerts.md)): without them
  no Alerts are sent; the MOTD still shows the Problems.
- `docs/guides/backup-and-restore.md`: `BACKUP_TIME` (Host local time) and `BACKUP_KEEP`.
- `docs/guides/teardown.md`: what `host setup` installed: the timers and units, the MOTD script, the logrotate
  rule, `/etc/abyssctl/`, `/var/log/abyssctl/`, the binary, the account, the network, the Proxy, the Actual and Caddy
  images and the Data root, and Docker's apt source if Host installed Docker.
- `docs/guides/certificates.md`: `host setup --cert <file> --key <file>` installs a Certificate the same way as
  `cert install` ([proxy.md](proxy.md)).
- Reference: the `host setup` page, from its help text: every flag, that an omitted flag keeps its value, and that
  `--data-root` and `--base-domain` are fixed after the first run.

## 9. Acceptance list

Module tests are in `internal/host`, on `hostfs.Under(t.TempDir())`, `hostfake`, `dockerfake`, a fake `Proxy`,
an `op.Recorder` and an `op.ManualClock`, without root. They check the plan and the commands and calls apply
makes; owners and modes on disk are checked only in journeys. Journey 1 (Bootstrap) and Host's slice of journey 2
(Tool update) run the real binary as root on an Ubuntu runner. Journey 1 runs with this step; journey 2 once
Lifecycle lands.

1. `ReadSettings` reads every key, fills each default for a missing optional key, keeps unknown keys in order
   with a warning, skips blank and `#` lines with a warning, and reports every broken rule in the key table with
   its line (including a Pushover user without a token), without a Pushover value in any message. →
   `TestReadSettings` (table)
2. A missing `abyssctl.env` gives `ErrNotSetUp` (exit 2); writing then reading gives back the same `Settings`,
   and two writes give the same bytes in the fixed key order. → `TestReadSettings_NotSetUp`,
   `TestSettings_RoundTrip`
3. `HEALTH_INTERVAL` accepts `1m` and `60m` and refuses `0m`, `61m`, `90s` and `5`; `DISK_MIN_FREE` accepts `1%`
   and `90%` and refuses `0%`, `91%` and `5G`; a bad `--health-interval` or `--backup-time` is `op.Refused`
   wrapping `render.CheckSchedule`'s error, and the same hand-edited values are `SettingsError` problems. →
   `TestSettings_Values` (table)
4. `--data-root` refuses a relative path, an unclean one, a character outside `[A-Za-z0-9._/-]`, `/` and a path
   inside `/etc`, `/proc`, `/tmp` or `/var/lib/docker`. → `TestPlan_DataRootRules` (table)
5. A first run without a Base domain is refused; a re-run with a different `--data-root` or `--base-domain` is
   refused (exit 2) and plans nothing; an omitted flag keeps the file's value and a given one changes only its
   key. → `TestPlan_FixedAndKeptSettings`
6. Each unsupported OS and architecture is refused, and passes with a warning under `--unsupported`; Debian 13,
   Ubuntu 24.04 and 26.04 and a TurnKey Core 19 `os-release` pass. → `TestPlan_SupportedHosts` (table)
7. With no `abyssctl.env`, a non-empty Data root, port 80 or 443 in use, an existing `abyssctl` network or an
   existing `abyssctl-proxy` container is refused with nothing changed; on a re-run, listeners on ports a running
   `abyssctl-proxy` publishes are not a conflict, and the same listeners with the Proxy stopped are; other
   containers give a warning. → `TestPlan_Conflicts` (table); the Release check refuses on a Host with a web
   server on 80/443 (checked by release.md's Release check)
8. Docker missing plans `apt-get update`, the key, the source and the four packages; Docker missing with an
   existing Docker apt source is refused; Docker missing on a non-apt `ID` under `--unsupported` is refused; Docker
   too old is refused with the part and versions; a stopped daemon is refused with the hint `systemctl start
   docker`; Docker that meets the minimums gets no install step; only `curl` missing plans `apt-get update` and
   one install. → `TestPlan_Docker` (table); journey 1 runs on the runner's existing Docker; the Docker install
   runs on the fresh Debian 13, Ubuntu 24.04, Ubuntu 26.04 and TurnKey Core 19 test VMs (checked by release.md's
   Release check)
9. A missing group plans only `groupadd --system`, a missing user only `useradd --system` with nologin and no home,
   and both missing plan both; a user or group with ID 0 or ≥ 1000 is refused; a wrong shell, home or primary
   group plans the matching `usermod`. → `TestPlan_Account` (table)
10. A running Instance container whose user is not the account's IDs gives the warning "<name> runs as <old> and
    cannot write its data until `abyssctl restart <name>`" in the panel, and is not recreated. →
    `TestPlan_AccountIDsChanged`
11. An installed binary newer than the running one is refused (exit 2) with nothing changed; an older one plans
    the copy and shows `from → to`; the same bytes plan nothing; the same version with different bytes plans the
    copy; one that times out after 10 s or prints no valid version plans the copy with a warning; running from
    `Binary()` itself copies nothing. → `TestPlan_Binary` (table)
12. An Instance whose `TOOL_VERSION` is newer than the binary stops the run with exit 4 and a hint, also when the
    rest of its record would not parse; an invalid record with no newer `TOOL_VERSION` is a warning; an Instance with `data.pre-restore/` or `UPGRADE_BACKUP` is not refused. →
    `TestPlan_Instances`
13. For a scripted drifted state (a file at 0644, a folder owned by 1000:1000, a `data/` file owned by root, a
    stale unit, a disabled timer, a timer masked at runtime, an Instance container with a published port, one also
    on a second network, and one whose hand-edited `compose.yaml` has `ports:`), the plan lists exactly those
    fixes and the hand-edit warning; apply emits one `fixed` or `recreated` event for each fix and calls
    `ComposeUp` with `ForceRecreate` only for the two drifted containers whose files are not hand-edited. →
    `TestPlan_Drift`, `TestSetup_DriftEvents`
14. A Host with nothing drifted plans `NoOp`, and `Setup` writes no file and runs no changing command. →
    `TestSetup_NoOp`
15. `PlanSetup` and `Setup` return the same refusal for each refused case, `PlanSetup` changes nothing, and
    `Setup` runs the steps the plan listed in the apply order. → `TestPlanMatchesSetup` (table)
16. Apply writes `abyssctl.env` before creating any folder under the Data root and calls `Options.Lock` right
    after step 4; a run cut off after the settings, with no `state/` yet, is finished by a re-run through the hook
    instead of refused. → `TestSetup_SettingsFirst`, `TestSetup_LockHook`
17. The walk leaves the contents of `caddy-data/` and `caddy-config/` alone, never follows a symlink and never
    changes a mode inside `data/` or `data.pre-restore/`. → `TestSetup_WalkRules`
18. Units, the MOTD script and the logrotate rule are written only when their bytes change; a changed timer gets
    `daemon-reload` and a restart; a changed `HEALTH_INTERVAL` rewrites the health timer and the MOTD script. →
    `TestSetup_Units`
19. The Proxy's steps and warnings from `PlanEnsure` appear in the panel, including the recreate step, before the y/N. →
    `TestPlan_ProxySteps`
20. The summary panel and every event show Pushover values only as `…abcd`; `SetPushover` changes only the two
    keys and refuses a value that is not 30 characters of `[A-Za-z0-9]`. → `TestSummary_MasksSecrets`,
    `TestSetPushover`
21. `Account` returns the account's IDs and `ErrNoAccount` when it is missing. → `TestAccount`
22. Journey 1: `host setup --base-domain …` on a fresh Host exits 0, the Proxy runs ready with no site files and
    an HTTPS request to it fails (connection closed or handshake error), and both timers are enabled and active; the activity log holds the run's lines from its first one. →
    journey 1
23. Journey 1: every path in the Host layout has its owner and mode; the account is a system account with
    nologin; after a mode and an owner are broken by hand, a re-run fixes both and logs one `fixed` line each;
    then another re-run is a no-op (exit 0, "Nothing to fix"). → journey 1
24. Journey 1: `systemd-analyze verify` passes on the installed units, `logrotate -d` on the installed rule, and
    the installed MOTD script, run after `systemctl stop` and `systemctl disable` of the health timer and before
    any Health check, prints "Health check not running"; a re-run enables and starts the timer again. → journey 1
25. Journey 1: `host setup --dry-run` on a set-up Host with a broken mode prints the panel, exits 0 and changes
    nothing; without a TTY and without `--yes`, a plan with steps refuses with exit 2. → journey 1
26. Journey 2: running `host setup` from the build under test on a Host set up with stamped v0.0.1 replaces the
    binary and the units hold the build's bytes, and the Proxy container keeps its ID when the Proxy `compose.yaml` bytes did not
    change; running the v0.0.1 binary's `host setup` afterwards is refused with exit 2 and changes nothing. →
    journey 2 (checked by lifecycle.md's journey slice)
27. Journey 2: the Instance containers keep their IDs across the update, and a changing command on an Instance
    whose `TOOL_VERSION` is newer than the binary exits 4 → journey 2 (checked by lifecycle.md's journey slice);
    `status` flags the Instances' files as out of date → journey 2 (checked by lifecycle.md's journey slice)
28. Journey 3: a file in an Instance's `data/` changed to root:root by hand is owned by `abyssctl:abyssctl` again
    after a `host setup` re-run, with its mode unchanged. → journey 3 (checked by lifecycle.md's journey slice)
29. `host setup --cert/--key` installs the Certificate through the Proxy with the Instances' Hostnames. →
    `TestSetup_Certificate`; journey 4's `host setup --cert/--key` (checked by lifecycle.md's journey slice)
30. `internal/host`'s direct imports are only those in the import rule, and only `internal/cli` imports it. →
    `TestImportRules` in `internal/archtest`
31. Docs: the README sections, `security.md`, `alerts.md`, `backup-and-restore.md`, `teardown.md`,
    `docs/guides/certificates.md` (`host setup --cert/--key`) and the `host setup` reference page say what [Docs it changes](#8-docs-it-changes) lists. → (review)
