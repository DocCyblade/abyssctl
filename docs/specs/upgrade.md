# Spec: upgrade

Build step 10 of 16. The [overview](README.md) has the module map, the call graph and the Host layout. Shared
packages (`internal/hostfs`, `internal/op`, `internal/buildinfo`) are in [foundation.md](foundation.md); the Docker
adapter, image names and locks are in [docker-and-locks.md](docker-and-locks.md); the Instance record and
`outcomes.json` are in [store.md](store.md); the compose file is in [render.md](render.md); `Probe` and `Status`
are in [proxy.md](proxy.md); the guards and `CheckVersion` are in [lifecycle.md](lifecycle.md); `Create` and
`Revert` are in [backup.md](backup.md); `Latest` and `Resolve` are in [versions.md](versions.md). Capitalised terms
are defined in [GLOSSARY.md](../../GLOSSARY.md).

## 1. Purpose

Build the Upgrade module (`internal/upgrade`) and the `upgrade` verb. Upgrade owns:

- **moving one [Instance](../../GLOSSARY.md) to a newer stable Actual version**: pre-flight → pull → stop →
  `pre-upgrade` [Backup](../../GLOSSARY.md) → switch → verify;
- **the automatic [Rollback](../../GLOSSARY.md)**: when the new version fails to start or to verify, the Instance
  goes back to the `pre-upgrade` Backup's data and version, Regrouped only when the new version ever passed
  `/health`;
- **finishing an interrupted Upgrade**: an Instance whose record still holds `UPGRADE_BACKUP` while no command
  holds its lock is rolled back by the next `upgrade <name>`;
- **the Upgrade outcome**: one `RecordUpgrade` call per Upgrade, which the next [Health check](../../GLOSSARY.md)
  turns into a [Problem](../../GLOSSARY.md) when it was not `ok`.

Actual migrates its data forward on first start and cannot run on migrated data after a downgrade. So the only
way back from a failed Upgrade is the `pre-upgrade` Backup, restored with its own version: the Rollback is a
Restore without the `pre-restore` Backup ([ADR 0004](../adr/0004-restore-regroups-offline.md)). There
is no `rollback` command and no downgrade: an older version comes back only through `restore <name> <id>`.

When this spec is done, `sudo abyssctl upgrade kids` moves `kids` to the latest stable Actual, a version that
fails is rolled back with exit 3, and journey 6 passes.

## 2. Interface

Signature sketches, not final code.

```go
package upgrade

// The seams, so module tests use fakes. The real values are *versions.Versions, *proxy.Proxy and
// *backup.Backup.
type Versions interface {
    Latest(ctx context.Context, repo string) (buildinfo.ImagePin, error)
    Resolve(ctx context.Context, repo, version string) (buildinfo.ImagePin, error)
}
type Proxy interface {
    Status(ctx context.Context) (proxy.Status, error)
    Probe(ctx context.Context, hostname, path string) (proxy.Response, error)
}
type Backup interface {
    PlanCreate(ctx context.Context, name string, kind backup.Kind) (op.Summary, error)
    Create(ctx context.Context, name string, kind backup.Kind) (backup.Info, error) // caller holds the Instance lock
    List(name string) ([]backup.Info, error)                                      // the finish panel's version
    Revert(ctx context.Context, name, backupID string, o backup.RevertOpts) error  // caller holds the Instance lock
}

type Deps struct {
    Paths    hostfs.Paths // with the Data root
    Store    *store.Store
    Docker   docker.Client
    Versions Versions
    Proxy    Proxy
    Backup   Backup
    Version  string        // buildinfo.Version: written as TOOL_VERSION, compared by guard 1
    Repo     string        // buildinfo.ActualRepo
    Emit     op.Emitter
    Clock    op.Clock
}

func New(d Deps) *Upgrader

// Target is what one `upgrade` run aims at. The CLI gets it before any lock.
type Target struct {
    Pin    buildinfo.ImagePin // zero when Finish is set
    Latest bool               // no version was typed: Pin is Latest(Repo)
    Finish bool               // UPGRADE_BACKUP is set: this run finishes the interrupted Upgrade
}

// Target reads the record without a lock. With UPGRADE_BACKUP set it returns Finish and makes no lookup; a typed
// version is then refused. Otherwise it asks Versions afresh. The CLI has already run lifecycle.CheckVersion on a
// typed version. A newer TOOL_VERSION or an invalid record is refused as guards 1 and 2 refuse it (exit 4), before
// any lookup.
func (u *Upgrader) Target(ctx context.Context, name, version string) (Target, error)

type Options struct {
    Timeout time.Duration // how long the new version may take to become healthy; must be > 0; the CLI's default is 5 min
}

// PlanUpgrade is Upgrade's twin, with the same arguments: it runs every check Upgrade runs, takes no lock, pulls
// nothing and changes nothing. The panel shows o.Timeout as the wait.
func (u *Upgrader) PlanUpgrade(ctx context.Context, name string, t Target, o Options) (op.Summary, error)

// Upgrade moves the Instance to t.Pin, or finishes an interrupted Upgrade when t.Finish is set.
// Caller holds the Instance lock (and no Host lock).
func (u *Upgrader) Upgrade(ctx context.Context, name string, t Target, o Options) error
```

### Invariants and error modes
- **Exit codes.** `Upgrade` returns nil (exit 0), an `op.Refused` (exit 2) before anything changed (with the two
  exceptions below), a plain error (exit 1) before the record changed, `op.RolledBack` (exit 3) when the Instance is
  back on its old version, or `op.NeedsOperator` (exit 4) from a guard or when the Rollback failed. A failure after
  the record changes never ends in exit 1: it is always a Rollback.
- **Refusals come before any change**, with two exceptions:
  - the `ErrNotFound` refusal (exit 2) for the target image comes from the pull itself; nothing is stopped or
    written before that pull;
  - the `pre-upgrade` Backup's free-space refusal (exit 2) comes from `Backup.Create`, after the stop; Upgrade then
    starts the Instance again, so nothing is left changed.

  Pre-flight runs `Backup.PlanCreate` before the pull and the stop, so a Host already short of space is refused
  with nothing changed; `Create` checks again after the stop because the space can change in between.
- **`PlanUpgrade` agrees with `Upgrade`.** It returns the same refusals for the same Host, takes no lock, writes
  nothing, pulls nothing and starts nothing. The only refusals a run can add are the two exceptions above, which
  depend on the moment it runs, and Docker's own failures.
- **Timeout.** An `Options.Timeout` of zero or less is refused (exit 2) by `PlanUpgrade` and `Upgrade` before
  anything else; the CLI refuses such a `--timeout` before it calls `Target`.
- **Target refusals (exit 2):** a version `lifecycle.CheckVersion` rejects (the CLI checks it first);
  `versions.ErrNotStable` and `versions.ErrNoSuchVersion` ("Actual 27.0.0 is not in the registry"), returned as
  Versions gives them, already wrapped in `op.Refused`; a target older than `ACTUAL_VERSION`
  (`buildinfo.CompareVersions`): `op.Refused("Actual <v> is older than the 26.10.1 kids runs").WithHint("abyssctl
  restore <name>")`, since going back means restoring an older Backup; and a typed version while
  `UPGRADE_BACKUP` is set: `op.Refused("an interrupted Upgrade of kids must be finished first").WithHint("abyssctl
  upgrade <name>")`. Any other lookup failure is exit 1. A target equal to `ACTUAL_VERSION` is not a refusal: the
  plan is `NoOp` and `Upgrade` returns nil without acting.
- **One Instance per run.** There is no bulk Upgrade and no `--all`.
- **Lock.** The caller holds the Instance lock only. Upgrade never touches the site file: the Hostname is the
  same before and after. Every pull happens under the Instance lock, which `image prune` relies on (it takes every
  Instance lock, [versions.md](versions.md)).
- **The record is the switch.** The new `ACTUAL_VERSION` and `ACTUAL_IMAGE_DIGEST` are written in the same `Put`
  as `UPGRADE_BACKUP`, and only after the `pre-upgrade` Backup exists. So a record with the new version and no
  `UPGRADE_BACKUP` has passed verify, and a record with `UPGRADE_BACKUP` always names a Backup to go back to.
- **`STATE` is kept.** A stopped Instance is upgraded too: it is started to verify and stopped again.
- **Upgrade never prints and never sends Alerts.** It emits events through `Deps.Emit` and records the outcome
  with `Store.RecordUpgrade`.

## 3. Behaviour

### The guards

`PlanUpgrade` and `Upgrade` read the record with `Store.Get` and check, in this order, with
[lifecycle.md](lifecycle.md)'s messages:

1. a `TOOL_VERSION` newer than `Deps.Version`: `op.NeedsOperator` (exit 4), hint `install abyssctl <v> or newer`,
   taken from the `*store.RecordError`'s `ToolVersion` when the record is otherwise invalid;
2. an invalid record: the `*store.RecordError` (`op.NeedsOperator`, exit 4, hint `fix <file> by hand`);
3. `UPGRADE_BACKUP` set: not a refusal. This run finishes the interrupted Upgrade ([below](#finishing-an-interrupted-upgrade));
4. an `instances/<name>/data.pre-restore/` folder without `UPGRADE_BACKUP`: `op.NeedsOperator("a Restore of <name>
   was interrupted", "abyssctl restore <name>")` (exit 4).

`Upgrade` checks `t.Finish` against the record again: a record that gained or lost `UPGRADE_BACKUP` since
`Target` ran is refused (exit 2, "the Instance changed; run the command again").

### Pre-flight

After the guards and the target check, and before anything changes:

1. `Proxy.Status`: not set up → refused, hint `abyssctl host setup`; not running → refused, hint
   `abyssctl host setup`. Verify reaches the Instance through the Proxy, so it must be up.
2. `Docker.Network` of each of the networks the record says the Instance joins (`abyssctl`, plus `abyssctl-external` when the record has [Internet access](../../GLOSSARY.md)): a missing one → refused, hint `abyssctl host setup`.
   Their subnets, in that order, become `render.Instance.TrustedProxies`.
3. The current version works: with the container running, `Proxy.Probe(ctx, hostname, "/health")` must answer 200
   with a body containing `"status":"UP"` (a covered Hostname with no site answers an empty 200, so the status
   alone proves nothing). Otherwise refused ("kids is not healthy on 26.10.1; an Upgrade would hide why"), hint
   `abyssctl status kids`. A stopped Instance skips this check.
4. `Backup.PlanCreate(ctx, name, backup.KindPreUpgrade)`, before the pull and the stop: its refusals, the
   free-space one included, pass through, so the plan shows them and a run refuses with nothing changed.
5. `Docker.Image(ctx, "<repo>:<version>@<digest>")` for the target pin: whether a pull is needed.

A finish of an interrupted Upgrade runs none of these steps ([below](#finishing-an-interrupted-upgrade)).

### The panel

`PlanUpgrade` returns:

- title "Upgrade kids";
- rows: From `26.10.1`; To `26.11.0` (with "(latest)" when `t.Latest`); Hostname; Notes
  `https://actualbudget.org/docs/releases`; Downtime "kids is unreachable until it passes its checks (about a
  minute)"; On failure "automatic Rollback to 26.10.1";
- steps: "pull docker.io/actualbudget/actual-server:26.11.0" (only when not local), "stop kids", "take a
  pre-upgrade Backup", "start kids on 26.11.0 and wait up to 5 min for it to be healthy" (the wait is
  `o.Timeout`), "check /health, /info and
  the Proxy", and for a stopped Instance "stop kids again";
- warnings: the downtime, and that a `pre-upgrade` Backup is taken first and kept;
- `NoOp` when the target equals `ACTUAL_VERSION` ("kids is already on 26.11.0").

For `t.Finish` the title is "Finish the interrupted Upgrade of kids", with rows Backup (the `UPGRADE_BACKUP` id),
Back to (that Backup's `ActualVersion`, from `Backup.List`), Regroup (`yes` when `UPGRADE_HEALTHY` is set, else
`no`), and the step "roll back to the pre-upgrade Backup".

### `Upgrade`

Events are shown as `name field=value`.

1. **Timeout, guards, target and pre-flight**, as above, under the lock. Nothing has changed if any refuses.
2. **Pull** `<repo>:<version>@<digest>` if it is not local, while the old version still serves. `ErrNotFound` →
   `op.Refused("Actual 26.11.0 with that digest is not in the registry").WithCause(err)`; any other failure →
   exit 1. Event `pull image=…`, then `pulled digest=… took=12s`.
3. **Stop.** Note whether the container is `running`, `restarting` or `paused` (`Docker.Container`); if so,
   `ComposeStop(Project{names.InstanceProject(name), InstanceDir})`. Event `stop instance=kids`.
4. **`pre-upgrade` Backup.** `Backup.Create(ctx, name, KindPreUpgrade)`. It finds the Instance stopped, so it
   neither stops nor starts it. Event `backup instance=kids kind=pre-upgrade id=… size=48MB`. On any error,
   including its free-space refusal, Upgrade starts the Instance again if step 3 stopped it (`ComposeUp` with
   `Wait`) and returns the Backup's error as it is. There is no Rollback: nothing has switched.
5. **Switch the record.** `Store.Put` with `UPGRADE_BACKUP=<Info.ID>`, the target's `ACTUAL_VERSION` and
   `ACTUAL_IMAGE_DIGEST`; every other key stays. A failed `Put` changed nothing: the Instance is started again as in
   step 4 and the error is returned (exit 1). From here on, every failure goes to the Rollback.
6. **Compose file.** `Store.WriteCompose(name, render.InstanceCompose(Instance{Name, Image:
   rec.Image(Repo), UID, GID, Internet: rec.Internet, TrustedProxies}))`. `INTERNET` is not changed.
7. **Start.** `ComposeUp(Project, UpOptions{Wait: true, WaitTimeout: o.Timeout})`. Event
   `start instance=kids version=26.11.0`. A new version that never becomes healthy, a crash in migrations
   included, fails with `ErrNotHealthy` within `o.Timeout`: the restart policy keeps restarting a crashing
   container, so the wait may run to the end of the timeout.
8. **Healthy once.** When `ComposeUp` returns without error, or returns an error while `Docker.Container` shows
   `Health` `healthy`, `Put` `UPGRADE_HEALTHY=1`. Actual answers `/health` only after its migrations finish, so
   from here on the data may be migrated and a Rollback Regroups. Event `verify check=health result=up`.
9. **Verify**, each through the Proxy with `Proxy.Probe`, once, with no stability window:
   1. `/health`: 200 and a body containing `"status":"UP"`. This also proves the Proxy reaches the new
      container by its Hostname. Event `verify check=proxy host=… status=200`.
   2. `/info`: 200 and a JSON body whose `build.version` is the target version. Event
      `verify check=info version=26.11.0`.
   A Probe error, another status or another body fails verify with its reason.
10. **Done.** For an Instance that was not running before step 3, `ComposeStop` again; `STATE` stays `stopped`.
    Then `Put` with `UPGRADE_BACKUP` and `UPGRADE_HEALTHY` cleared and `TOOL_VERSION=Deps.Version`, and
    `Store.RecordUpgrade(name, store.UpgradeOK)`. Event `upgraded instance=kids version=26.11.0`. The CLI's outcome
    line is "✓ kids is on 26.11.0 · Backup <id> kept".

A failure in steps 6–10 (including a failed final stop or `Put`) runs the Rollback.

### Rollback

`rollback(rec, reason)` runs with the Instance lock still held:

1. Event `fail check=… reason="…"`, then `rollback instance=kids to=26.10.1 regroup=no|yes`.
2. **Stop** the container if it is running, restarting or paused. A failed stop goes on to step 3: `Revert`
   stops it too.
3. **Save the logs.** `hostfs.Create(Paths.UpgradeFailedLog(name, Clock.Now()), 0600)`, then
   `Docker.Logs(ctx, names.InstanceProject(name), LogOptions{}, IO{Stdout: f, Stderr: f})`, then `Commit`. The
   file is `instances/<name>/upgrade-failed-<stamp>.log`, root:root 0600. Event `logs file=…`. If the container is
   gone or the save fails, the event says so and the Rollback goes on: the logs help, the data matters.
4. **Revert.** `Backup.Revert(ctx, name, rec.UpgradeBackup, backup.RevertOpts{Regroup: rec.UpgradeHealthy,
   UndoFailedStart: false})`. `Revert` brings back the Backup's data, version and digest, regenerates
   `compose.yaml`, starts the Instance only when `STATE=running` (and then writes `TOOL_VERSION`), and deletes
   `data.pre-restore/`, which holds the new version's data. Upgrade does none of that itself. Event
   `revert backup=…`.
5. **Clear** `UPGRADE_BACKUP` and `UPGRADE_HEALTHY` with `Put`.
6. `Store.RecordUpgrade(name, store.UpgradeRolledBack)`.
7. Return `op.RolledBack("the Upgrade of kids to 26.11.0 failed and kids is back on 26.10.1 with Backup <id>
   kept").WithCause(<reason>).WithHint("less <file>")` (exit 3). Without a saved log the hint is
   `abyssctl logs kids`.

No `pre-restore` Backup is taken: the data being replaced is the failed version's, and the `pre-upgrade` Backup is
the Operator's copy of the real data.

**When the Rollback fails.** If `Revert` returns an error, a refusal from its own checks included, or step 5
fails, Upgrade deletes nothing and leaves `UPGRADE_BACKUP` set. With `UndoFailedStart` false, a `Revert` whose
start failed leaves the Instance stopped with the Backup's data, version and digest in place. Upgrade calls
`Store.RecordUpgrade(name, store.UpgradeRollbackFailed)` and returns `op.NeedsOperator("the Rollback of kids
failed and Backup <id> and the logs in <file> are kept", "abyssctl upgrade <name>").WithCause(err)` (exit 4). A
`NeedsOperator` from `Revert` (hint `abyssctl restore <name>`) is wrapped the same way, so the outer hint wins,
because guard 3 sends every other command to `upgrade <name>` while `UPGRADE_BACKUP` is set. Running
`upgrade <name>` again repeats the Rollback; a repeated `Revert`, after a failure or a crash, is safe, because
`Revert` keeps a leftover `data.pre-restore/` and extracts the Backup afresh. A failed `RecordUpgrade` is an event
and never changes the exit code.

### Finishing an interrupted Upgrade

`UPGRADE_BACKUP` set while nobody holds the Instance lock means an Upgrade or its Rollback was cut off (power
loss, `kill -9`). The Health check raises a Problem for it ([health.md](health.md)), `status` shows it as
Interrupted `upgrade` ([lifecycle.md](lifecycle.md)), and every other changing command refuses with exit 4, hint
`abyssctl upgrade <name>`.

`upgrade <name>` (no version) then runs the Rollback above with the record as it is: event `finish instance=kids
backup=…`, logs saved, `Revert` with `Regroup` only when `UPGRADE_HEALTHY` is set (Revert keeps a leftover
`data.pre-restore/` and discards `data/`, so a Rollback cut off mid-swap finishes correctly), keys cleared,
`RecordUpgrade(UpgradeRolledBack)`. It returns `op.RolledBack("the interrupted Upgrade of kids is finished and
kids is back on 26.10.1 with Backup <id> kept").WithHint("less <file>")` (exit 3), so scripts see the same code
as any Rollback. It never moves forward to the version the interrupted run aimed at; the Operator runs
`upgrade <name>` again for that. Upgrade never calls `backup.Finish`.

A finish runs the guards and none of the pre-flight: no Proxy status, network check, probe, `PlanCreate` or image
check, because nothing new is started and the current version is not needed to go back. `Revert`'s own checks
still apply: the Backup id and its verification, the account, the networks the record says the Instance joins,
free space for the extract,
and the pull of the Backup's image by digest when it is not local. A refusal there is a failed Rollback
([above](#rollback)): exit 4, nothing changed, `UPGRADE_BACKUP` still set.

### Undoing an Upgrade later

An Upgrade that passed verify but turns out wrong is undone with `abyssctl restore <name> <pre-upgrade id>`,
which brings back that Backup's version ([backup.md](backup.md)). The old image stays on the Host until
`abyssctl image prune` removes it ([versions.md](versions.md)).

### What the CLI does

| Verb | Lookup | Locks | Prompt | `--dry-run` | `--json` | Exit |
|---|---|---|---|---|---|---|
| `upgrade <name> [<version>] [--timeout 5m] [--yes]` | `Target` (Versions) | Instance | y/N | yes | no | 0, 1, 2, 3, 4 |

1. At most one version argument; more is refused (exit 2). A `--timeout` of zero or less is refused (exit 2).
2. `lifecycle.CheckVersion` on a typed version, then `Target(ctx, name, version)`, before any lock. Its refusals
   and errors end the run.
3. `PlanUpgrade(ctx, name, target, Options{Timeout})` → the panel. `--dry-run` prints it and exits 0, with no lock and no pull. A
   `NoOp` prints the panel and exits 0.
4. The y/N prompt; `--yes` skips it; no TTY and no `--yes` refuses (exit 2).
5. Take the Instance lock (30 s), then `Upgrade(ctx, name, target, Options{Timeout})`, which checks everything
   again.

`upgrade` is a changing verb: it writes the activity log ([cli.md](cli.md)).

## 4. Files and formats it owns

- `instances/<name>/upgrade-failed-<stamp>.log`, root:root 0600: the failed container's `docker logs` output
  (stdout and stderr interleaved as Docker returns them), written once per Rollback. The stamp is
  `hostfs.Stamp` of the Rollback's start. Nothing removes these files except `remove`, which deletes them with the Instance folder.

Upgrade writes, but does not own, `UPGRADE_BACKUP`, `UPGRADE_HEALTHY`, `ACTUAL_VERSION`, `ACTUAL_IMAGE_DIGEST` and
`TOOL_VERSION` in the Instance record, `compose.yaml` and the `upgrade` entry in `outcomes.json`
([store.md](store.md), [render.md](render.md)).

### Package layout

```
internal/upgrade/
  upgrade.go     New, Deps, the seams, Target, Options
  plan.go        guards, target check, pre-flight, PlanUpgrade
  run.go         Upgrade
  rollback.go    rollback, finishing an interrupted Upgrade, the log file
internal/cli/
  upgrade.go     the upgrade verb
```

## 5. Depends on

- [foundation](foundation.md): `hostfs.Paths.UpgradeFailedLog`, `hostfs.Create`, `op` (`Emitter`, `Refused`,
  `RolledBack`, `NeedsOperator`, `Summary`, `Clock`), `buildinfo.ImagePin`, `CompareVersions`, `Version`,
  `ActualRepo`.
- [docker-and-locks](docker-and-locks.md): `Pull`, `Image`, `Container`, `ComposeStop`, `ComposeUp` with `Wait`,
  `Logs`, `Network`, `ErrNotHealthy`, `ErrNotFound`, `names.InstanceProject`. The CLI takes the Instance lock.
- [store](store.md): `Get`, `Put`, `WriteCompose`, `RecordUpgrade`, `Record.Image`.
- [render](render.md): `InstanceCompose`.
- [lifecycle](lifecycle.md): the guard messages; the CLI calls its `CheckVersion` first.
- [proxy](proxy.md): `Status` and `Probe`, only to check and reach the Instance.
- [backup](backup.md): `PlanCreate`, `Create(KindPreUpgrade)`, `List`, `Revert`.
- [versions](versions.md): `Latest` and `Resolve` for the target, always looked up afresh, never read from
  `health.json`.

`internal/upgrade` imports only the standard library, `internal/hostfs`, `internal/op`, `internal/buildinfo`,
`internal/docker` (and `docker/names`), `internal/store`, `internal/render`, `internal/proxy`, `internal/backup`
and `internal/versions`. Only `internal/cli` imports `internal/upgrade`.

## 6. Decisions

- **Rollback is a Restore of the `pre-upgrade` Backup with its version.** Actual cannot run on data a newer
  version migrated, so going back means the Backup's data and version together
  ([ADR 0004](../adr/0004-restore-regroups-offline.md)).
- **Regroup only when the new version ever passed `/health`.** Actual answers `/health` only after migrations, so
  before that no client can have synced against the new data and the Regroup would only force every device to
  download again. `UPGRADE_HEALTHY` carries that fact across an interruption.
- **The Backup comes after the stop.** A stop-copy-start Backup of a stopped Instance is consistent without
  stopping twice; the pull happens first so the downtime is only stop → Backup → start → verify.
- **Verify is three checks, once**, after `ComposeUp`'s wait on the container's own healthcheck: `/health`
  answers UP, which proves the migrations finished; `/info` reports the target version, which proves the right
  version runs; and both answer through the Proxy at the Instance's Hostname, which proves the Hostname
  reaches the new container. No stability window: later trouble is the Health check's to report.
- **Automatic Rollback, no manual one.** The tool decides from verify; the Operator undoes a good-but-unwanted
  Upgrade with `restore`. Exit 3 means "failed, and the Instance is fine on its old version".
- **An interrupted Upgrade always goes back.** The tool cannot know how far the cut-off run got with the new
  version, and going back to a verified Backup is always safe.
- **Health is the only Alert sender.** Upgrade writes `outcomes.json`; the next Health check alerts
  ([README.md](README.md)).
- No new dependency.

## 7. Later

- Unattended and bulk Upgrade (for example `upgrade --all`, one Instance at a time, stopping at the first
  Rollback).
- Reading the release notes for breaking-change warnings and showing them in the panel.
- Nightly and edge versions as targets.

## 8. Docs it changes

- README: the Quickstart's "Updating" line (`sudo abyssctl upgrade <name>`).
- `docs/guides/upgrades.md`: what `upgrade` does and how long it is down; naming a version
  (`upgrade <name> <version>`), `--timeout`, `--dry-run`;
  what exit 3 means and where the failed logs are; finishing an interrupted Upgrade; what to do on exit 4; undoing
  an Upgrade with `restore <name> <pre-upgrade id>`; freeing old images with `image prune`.
- `docs/guides/security.md`: `upgrade-failed-*.log` files are root-only and may hold Instance data in log lines.
- Reference: the `upgrade` page, from its help text.

## 9. Acceptance list

Module tests are in `internal/upgrade`, with `dockerfake` (`SetImageHealth`, `AddRemote`, `Fail`/`FailWhen`,
`SetLogs`, `Calls`), `registrytest` behind the real Versions, the real Store on a temp Data root, a fake Proxy seam
(or the real Proxy with `ProbeAddr` on an `httptest` server), a fake Backup seam (and the real Backup for the
Rollback-with-data tests), an `op.Recorder` and an `op.ManualClock`, without root, Docker or network. CLI tests are
in `internal/cli`. This spec's journey slice is journey 6 ([testing.md](testing.md)).

1. `Target` with no version returns `Latest(Repo)` with `Latest` set; with a version it calls `Resolve`;
   `ErrNotStable` and `ErrNoSuchVersion` are returned as Versions gave them (exit 2); any other lookup error is
   exit 1. → `TestTarget` (table)
2. `Target` on a record with `UPGRADE_BACKUP` returns `Finish` with no registry request, and refuses (exit 2) a
   typed version with hint `abyssctl upgrade <name>`. → `TestTarget_Interrupted`
3. A target older than `ACTUAL_VERSION` is refused (exit 2) with hint `abyssctl restore <name>`; an equal one is a `NoOp`
   plan and `Upgrade` returns nil with no Docker call. → `TestPlanUpgrade_TargetRules` (table)
4. Guards run in order: newer `TOOL_VERSION` (4, `install abyssctl <v> or newer`, also on an otherwise invalid
   record), invalid record (4, `fix <file> by hand`),
   `data.pre-restore/` without `UPGRADE_BACKUP` (4, `abyssctl restore <name>`); `UPGRADE_BACKUP` set plans a
   finish instead of refusing. → `TestUpgrade_Guards` (table)
5. `PlanUpgrade` and `Upgrade` refuse (exit 2), with nothing pulled, stopped or written: an `Options.Timeout` of
   zero or less, Proxy not set up, Proxy not running, no `abyssctl` network, no `abyssctl-external` network for
   an Instance with Internet access, a running Instance whose `/health`
   probe fails or answers an empty 200, and a `PlanCreate` refusal (the free-space one included). A stopped
   Instance skips the probe. → `TestUpgrade_Preflight` (table)
6. `PlanUpgrade` and `Upgrade`, given the same arguments, refuse with the same errors; `PlanUpgrade` takes no
   lock, pulls nothing and makes no changing Docker call. → `TestPlanUpgrade_MirrorsUpgrade`
7. The panel has the title, the From/To/Hostname/Notes/Downtime/On failure rows, the pull step only when the image
   is not local, the start step with the wait from `Options.Timeout`, and the warnings; the finish panel names the
   Backup, the Back to version and Regroup. → `TestPlanUpgrade_Panel`
   (golden)
8. A successful Upgrade calls, in order: pull (only if not local), stop, `Create(KindPreUpgrade)`, `Put` with
   `UPGRADE_BACKUP` and the new pin, `WriteCompose`, `ComposeUp` with `Wait` and the timeout, `Put`
   `UPGRADE_HEALTHY=1`, probes of `/health` and `/info`, `Put` clearing both keys with `TOOL_VERSION`, and
   `RecordUpgrade(ok)`. The site file is untouched. → `TestUpgrade_Success`
9. A stopped Instance is started to verify and stopped again, keeps `STATE=stopped`, and its Rollback starts
   nothing. → `TestUpgrade_StoppedInstance`
10. A failed pull (exit 2 for `ErrNotFound`, before any stop), a failed `Create` (exit 2 for its free-space
    refusal after the stop) or a failed switch `Put` leaves the record unchanged, restarts an Instance that was
    running, takes no Rollback and returns that error's own code.
    → `TestUpgrade_FailsBeforeSwitch` (table)
11. A new version that never becomes healthy (`ErrNotHealthy` within the timeout) rolls back with `Regroup` false: logs saved
    to `upgrade-failed-<stamp>.log` 0600, `Revert` called once with the `pre-upgrade` id, keys cleared,
    `RecordUpgrade(rolled_back)`, exit 3 naming the Backup, with the log file in the hint. → `TestUpgrade_RollbackUnhealthy`
12. A new version that passes `/health` but fails `/info` (wrong version) or the Proxy probe rolls back with
    `Regroup` true. → `TestUpgrade_RollbackAfterHealthy` (table)
13. A Rollback with the real Backup brings back the old data and version and deletes the migrated data, with no
    `pre-restore` Backup taken. → `TestUpgrade_RollbackRestoresData`
14. A failed log save does not stop the Rollback. → `TestUpgrade_RollbackLogSaveFails`
15. A failed `Revert` (including its start and a refusal from its own checks) leaves `UPGRADE_BACKUP` set and data in place, records
    `rollback_failed` and returns exit 4 with hint `abyssctl upgrade <name>`, also when `Revert` returned its own
    `NeedsOperator`. → `TestUpgrade_RollbackFails` (table)
16. Finishing an interrupted Upgrade runs no pre-flight (no Proxy status, network check, probe, `PlanCreate` or
    image check), runs the Rollback with `Regroup` from `UPGRADE_HEALTHY`, needs no registry when the Backup's
    image is local, handles a leftover `data.pre-restore/`, records `rolled_back` once and exits 3; a `Revert`
    refusal leaves `UPGRADE_BACKUP` set and exits 4.
    → `TestUpgrade_FinishInterrupted` (table)
17. Every pull happens after the caller's Instance lock is held, and lookups happen before it. →
    `TestUpgradeCmd_LockOrder` in `internal/cli`
18. `upgrade` runs `CheckVersion` on a typed version before `Target`, asks y/N, `--yes` skips it, no TTY without
    `--yes` is exit 2, `--dry-run` prints the panel with no lock and no pull, more than one argument is exit 2, a
    `--timeout` of zero or less is exit 2 before `Target`, and `--timeout` reaches `Options` for both
    `PlanUpgrade` and `Upgrade`. →
    `TestUpgradeCmd` (table) in `internal/cli`
19. Only `internal/cli` imports `internal/upgrade`, and it imports only the packages in
    [Depends on](#5-depends-on). → `TestImportRules` in `internal/archtest`
20. Journey 6: `create kids --version <older real>`, then `upgrade kids <newer real> --yes` succeeds (exit 0, new
    version in the record and on `/info`); `upgrade kids 99.0.1 --timeout 30s --yes` (fake, never healthy) exits 3
    with the old version, `UPGRADE_*` cleared, the log file present and `outcomes.json` result `rolled_back`,
    Regroup not done; `upgrade kids 99.0.2 --yes` (fake, healthy, wrong `/info`) exits 3 with Regroup done; then
    `restore kids <pre-upgrade id> --yes`, `backup prune kids --keep 1 --yes` and `image prune --yes` as
    [backup.md](backup.md) and [versions.md](versions.md) list. Every journey `upgrade`
    passes a version, because the local registry's latest is `99.0.x`. → journey 6
21. `docs/guides/upgrades.md`, `security.md`, the README line and the `upgrade` reference page say what
    [Docs it changes](#8-docs-it-changes) lists. → (review)
