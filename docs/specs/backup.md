# Spec: backup

Build step 8 of 16. The [overview](README.md) has the module map, the call graph and the Host layout. Shared
packages (`internal/hostfs`, `internal/op`, `internal/buildinfo`) are in [foundation.md](foundation.md); the Docker
adapter and locks are in [docker-and-locks.md](docker-and-locks.md); the Instance record and folder are in
[store.md](store.md); generated files are in [render.md](render.md); the account and Settings are in
[host.md](host.md); `remove`'s final Backup is in [lifecycle.md](lifecycle.md). Capitalised terms are defined in
[GLOSSARY.md](../../GLOSSARY.md).

## 1. Purpose

Build the Backup module (`internal/backup`) and its verbs: `backup <name>`, `backup --all`,
`backup --all --scheduled`, `backup list`, `backup prune` and `restore`. Backup owns:

- taking a [Backup](../../GLOSSARY.md) of one [Instance](../../GLOSSARY.md): stop, copy, start again;
- the archive, its `manifest.json` and its `.sha256` sidecar, and the Backup id;
- verification, right after a Backup is written and before any Restore uses it;
- [Backup kinds](../../GLOSSARY.md) and [Retention](../../GLOSSARY.md) of scheduled Backups, and `backup prune`;
- [Restore](../../GLOSSARY.md): a `pre-restore` Backup, the data swap, the Backup's Actual version, the
  [Regroup](../../GLOSSARY.md), and finishing an interrupted Restore;
- `Revert`, the data swap without the `pre-restore` Backup, which Upgrade's [Rollback](../../GLOSSARY.md) uses.

[lifecycle.md](lifecycle.md) (build step 7) builds the part of `Create` that `remove` needs, `KindFinal`, to this
spec's interface. This step adds the other kinds, `List`, `Prune`, `Restore`, `Revert`, `Finish` and the Regroup.
When it is done, an [Operator](../../GLOSSARY.md) can back up every Instance by hand and on schedule, list and
prune Backups, and restore any of an Instance's own Backups, with devices told the budget was reset.

## 2. Interface

Signature sketches, not final code.

```go
// Backup kinds: why a Backup was made. The value is the last part of the Backup id.
type Kind string

const (
    KindManual     Kind = "manual"      // backup <name>, backup --all
    KindScheduled  Kind = "scheduled"   // backup --all --scheduled (the timer)
    KindPreUpgrade Kind = "pre-upgrade" // Upgrade, before it switches versions (upgrade.md)
    KindPreRestore Kind = "pre-restore" // Restore, before it swaps the data
    KindFinal      Kind = "final"       // remove (lifecycle.md)
)

func ParseKind(s string) (Kind, error) // one of the five, or op.Refused

// Info describes one Backup. Everything but Size, Path and Problem comes from its manifest.
type Info struct {
    ID            string    // "<name>-<stamp>-<kind>", e.g. "kids-20261117T080004Z-scheduled"
    Instance      string
    Kind          Kind
    CreatedAt     time.Time // UTC, whole seconds; the stamp in the id
    ActualVersion string    // "26.11.0"
    ActualDigest  string    // "sha256:<64 hex>"
    ToolVersion   string    // the Abyss version that made it
    Size          int64     // the archive's size in bytes
    Path          string    // absolute: <data root>/backups/<name>/<id>.tar.gz
    Problem       string    // List only: "" or one sentence, e.g. "the manifest can't be read"
}

// ParseID parses from the right: a known kind suffix, then the 16-character stamp; the rest is the name.
func ParseID(id string) (name string, at time.Time, kind Kind, err error) // op.Refused if malformed

type Deps struct {
    Paths      hostfs.Paths                    // with the Data root
    Store      *store.Store
    Docker     docker.Client
    Account    func() (store.Owner, error)     // host.Account; called by Restore, Revert and Finish only
    FreeSpace  func(path string) (uint64, error) // bytes free for root on path's filesystem; statfs in production
    Emit       op.Emitter
    Clock      op.Clock
    Version    string                          // buildinfo.Version
    ActualRepo string                          // buildinfo.ActualRepo
    Keep       int                             // Settings.BackupKeep (BACKUP_KEEP), 1–1000
}

func New(d Deps) *Backup

// --- taking a Backup ---

// Create takes a verified Backup of the Instance. Precondition: the caller holds the Instance lock.
// It stops a running container and starts it again; it never starts one that was not running. For
// KindScheduled it also records the outcome and applies Retention before it returns.
func (b *Backup) PlanCreate(ctx context.Context, name string, kind Kind) (op.Summary, error)
func (b *Backup) Create(ctx context.Context, name string, kind Kind) (Info, error)

// --- reading ---

// List returns the Instance's Backups, newest first. No lock; reads only the manifests.
func (b *Backup) List(name string) ([]Info, error)

// --- removing ---

type Policy struct {
    Keep int  // keep this many newest, >= 0
    Kind Kind // "" means every kind, counted together
}

// Prune deletes Backups beyond the policy, never the Instance's newest Backup. Caller holds the Instance lock.
func (b *Backup) PlanPrune(ctx context.Context, name string, p Policy) (op.Summary, error)
func (b *Backup) Prune(ctx context.Context, name string, p Policy) ([]Info, error) // the deleted ones

// --- Restore ---

type Selector struct {
    ID     string // a Backup id in backups/<name>/
    Latest bool   // the newest Backup of any kind that passes verification
}

// Restore puts a Backup back: verify, pull, pre-restore Backup, swap with Regroup. Caller holds the Instance lock.
func (b *Backup) PlanRestore(ctx context.Context, name string, s Selector) (op.Summary, error)
func (b *Backup) Restore(ctx context.Context, name string, s Selector) (Info, error) // the Backup restored

// Interrupted reports whether instances/<name>/data.pre-restore/ exists. No lock.
func (b *Backup) Interrupted(name string) (bool, error)

// Finish puts the Instance back as it was before an interrupted Restore. Caller holds the Instance lock.
func (b *Backup) PlanFinish(ctx context.Context, name string) (op.Summary, error)
func (b *Backup) Finish(ctx context.Context, name string) error

// --- Revert: the data swap, for Restore and Upgrade's Rollback ---

type RevertOpts struct {
    Regroup         bool // Restore: always; Rollback: only when UPGRADE_HEALTHY is set
    UndoFailedStart bool // Restore: true; Rollback: false (a failed start leaves the Backup's data in place)
}

// Revert replaces data/ with the Backup's, sets the record's version and digest to the Backup's, regenerates
// compose.yaml and starts the Instance if STATE=running. It takes no Backup of its own. Caller holds the
// Instance lock.
func (b *Backup) Revert(ctx context.Context, name, backupID string, o RevertOpts) error

// Errors.
var (
    ErrNoBackup     = errors.New("no such Backup")            // matched together with op.ErrRefused
    ErrVerify       = errors.New("Backup failed verification") // the archive, sidecar or manifest disagree
    ErrNoGroupFile  = errors.New("group file missing")        // Regroup: a files row has no group-<id>.sqlite
)
```

### Invariants and error modes

- **Paths.** Every path comes from `hostfs.Paths`: `InstanceBackups(name)`, `InstanceData(name)`,
  `InstancePreRestore(name)`, `InstanceRecord(name)`. The file names inside `backups/<name>/` and the entry
  names inside an archive are this spec's ([Files and formats](#4-files-and-formats-it-owns)).
- **Names first.** Every method calls `store.ValidName(name)` before it builds a path, and `ParseID` checks that
  a given id names that Instance, so `restore kids bob-…` and `restore kids ../x` are refused (exit 2).
- **Locks.** Backup never takes a lock. Every changing method's precondition is the Instance lock; `List`,
  `Interrupted` and every `Plan…` take none. The CLI's lock table is below ([What the CLI does](#what-the-cli-does-around-backup)).
- **Guards.** `Create`, `Prune` and `Restore` (and their plans) start with Lifecycle's four guards, in
  Lifecycle's order ([lifecycle.md](lifecycle.md)): 1. a `TOOL_VERSION` newer than the binary (exit 4, hint
  `install abyssctl <v> or newer`), read from the `TOOL_VERSION=` line even when the record is otherwise invalid;
  2. an invalid record (exit 4, hint `fix <file> by hand`); 3. `UPGRADE_BACKUP` set (exit 4, hint
  `abyssctl upgrade <name>`); 4. a `data.pre-restore/` folder (exit 4, hint `abyssctl restore <name>`). `Revert`
  runs only guards 1 and 2: Upgrade calls it with `UPGRADE_BACKUP` set, and its own step 2 handles a leftover
  `data.pre-restore/`. `Finish` runs guards 1 and 2 and then its own checks
  ([Finishing an interrupted Restore](#finishing-an-interrupted-restore)).
- **Stop and start again.** "Stop the container if it is running" means: `Container(abyssctl-<name>)` exists with
  State `running`, `restarting` (a crash loop) or `paused`, and Backup calls `ComposeStop`. "Start it again"
  applies to a container that was `running` or `restarting` when the method began, and, for `Revert`, `Restore`
  and `Finish` once they have swapped the data, to an Instance whose record says `STATE=running`. Every start is
  `ComposeUp` with `Wait` and a 5-minute `WaitTimeout`, on `context.WithoutCancel(ctx)`, so a cancelled command
  still brings back what it stopped.
- **`TOOL_VERSION`.** `Create` never changes the record. `Restore`, `Revert` and `Finish` write `TOOL_VERSION` =
  `Deps.Version` only after their `ComposeUp` succeeds; when they start nothing, it stays as it was.
- **Free space.** A Backup's need is the sizes of `instance.env` and every regular file under `data/`, plus 10%.
  An extract's need is the sizes of the Backup's `data/` files in its manifest, plus 10%. `FreeSpace` is asked for
  the path the bytes go to, or its nearest existing parent when that folder does not exist yet (a `PlanCreate`
  before the first Backup). [lifecycle.md](lifecycle.md) relies on this rule for the final Backup.
- **Secrets.** A manifest holds paths, sizes and checksums, never file contents. No event field, `Summary`,
  `Info` or error holds anything read from inside `data/`, and Backup never logs the record's Extra values.
  Backups are not encrypted; their protection is root-only modes ([Decisions](#6-decisions)).
- **Errors.** The guards' refusals are exit 4, as above. `op.Refused` (exit 2) only before anything changed: an
  unknown or malformed id, a Backup
  of another Instance, a failed verification before a Restore, a failed pull before a Restore or Revert, not
  enough free space, a manifest schema this binary does not know. A failure once work has started is a plain
  wrapped error (exit 1); a Restore that failed and was undone is exit 1. An undo that itself fails is
  `op.NeedsOperator` (exit 4), hint `abyssctl restore <name>`.
- **Plans** run every refusal check of their operation, take no lock, write nothing, pull nothing and stop
  nothing. They may read manifests, hash archives (a `restore --dry-run` verifies the Backup) and inspect Docker.

## 3. Behaviour

### What the CLI does around Backup

| Verb | Locks | Prompt | `--dry-run` | Exit |
|---|---|---|---|---|
| `backup <name>` | Instance lock, `Acquire` with `locks.DefaultWait` | none | no | 0, 1, 2, 4 |
| `backup --all` | each Instance's lock in name order, one at a time, `DefaultWait` | none | no | 0, 1 if any failed, 2 if refused before any Backup |
| `backup --all --scheduled` | each Instance's lock, `TryAcquire`; a busy one is skipped | none | no | 0, 1 if any failed, 2 if refused before any Backup |
| `backup list [<name>]` | none | none | n/a | 0, 1, 2 |
| `backup prune <name> --keep N [--kind K]` | Instance lock, `DefaultWait` | y/N | yes | 0, 1, 2, 4 |
| `restore <name> <id>`, `--latest`, or `<name>` alone | Instance lock, `DefaultWait` | typed name | yes | 0, 1, 2, 4 |

- Every command here first reads Settings ([host.md](host.md)) and builds `Deps` with `Keep` =
  `Settings.BackupKeep` and `Account` = `host.Account`.
- `backup <name>` and `backup --all` take a `manual` Backup; only `--scheduled` takes a `scheduled` one.
  `--scheduled` without `--all` is refused (exit 2). Neither shows a panel or asks: a Backup changes nothing the
  Operator could lose, and the stop is brief.
- `backup --all` and `--scheduled` act on every Instance `Store.List` returns, valid record or not, one at a time,
  each under its own Instance lock released before the next. A failure in one is logged and does not stop the
  others. The outcome line counts them: "✓ 3 Backups made" or "✗ 2 of 3 Backups made; bob failed".
  `--scheduled` logs a busy Instance as `skipped instance=bob reason=busy`, records nothing for it and names it
  in the outcome line ("✓ 1 Backup made; bob skipped (busy)"); a busy skip is not a failure (exit 0).
- `backup list` with no name lists every Instance's Backups (every folder `Store.List` returns), not
  `backups/removed/`. Columns: id, kind, made (local time), Actual version, size; a `Problem` is shown on the
  row. `--json` gives the `backup list` document in [cli.md](cli.md#--json-schemas).
- `backup prune` shows the panel (rows Instance, policy, "deletes N Backups"; one step per Backup deleted; the
  warning "deleted Backups can't be brought back"), asks y/N, and `--yes` skips it; no TTY and no `--yes` is
  refused (exit 2). `--keep` is required and an integer ≥ 0; `--kind` takes one of the five kinds.
- `restore <name>` with neither an id nor `--latest`:
  - if `Interrupted(name)` is true, it finishes the interrupted Restore (`Finish`): it shows `PlanFinish`'s panel
    and asks the Operator to type the name, with no pick list; `--yes` skips the prompt; no TTY and no `--yes`
    is refused (exit 2);
  - otherwise, on a TTY it shows a pick list of `List(name)` (newest first, numbered), then the panel;
  - otherwise (no TTY) it is refused (exit 2), naming `restore <name> <backup-id>` and `--latest`.
- `restore` with an id or `--latest` while `data.pre-restore/` exists is refused (exit 4, hint
  `abyssctl restore <name>`): finish the interrupted Restore first.
- `restore` shows the panel, then asks the Operator to type the name; `--yes` skips it; no TTY and no `--yes` is
  refused (exit 2). `--dry-run` prints the panel only.

### `Create`: taking a Backup

1. **Guards** (above). Then `kind` must be one of the five.
2. **Sweep.** `EnsureDir(backups/<name>/, 0700)`. Delete every `*.partial` file in it (a crash's leftover, or an
   archive that never got its sidecar) and every `<id>.tar.gz.sha256` with no `<id>.tar.gz`. Event
   `swept instance=kids files=2` when anything went.
3. **Free space.** If `FreeSpace(backups/<name>/)` is below the Backup's need (the
   [rule](#invariants-and-error-modes)), refuse (exit 2): "not enough free space for the Backup: needs 1.2 GB,
   0.8 GB free".
4. **Stop if running** (the [rule](#invariants-and-error-modes): `running`, `restarting` or `paused`), event
   `stop instance=kids`, and remember to start it again if it was `running` or `restarting`. An exited container,
   or no container at all (a `remove` re-run after `ComposeDown`), is copied as it is and not started. `STATE`
   plays no part: a `stopped` record with a running container is stopped and started again.
5. **Copy.** `CreatedAt` = the `Clock`'s now in UTC, to the second; the id is `<name>-<Stamp(CreatedAt)>-<kind>`.
   If `<id>.tar.gz` already exists (two Backups of one kind within a second), wait until the `Clock` reaches the
   next second and take the stamp again. Then:
   1. pass one walks `data/` and hashes every regular file and `instance.env` (SHA-256, sizes);
   2. `hostfs.Create(<id>.tar.gz, 0600)` and write the gzip'd tar: `manifest.json` first, then `instance.env`,
      then `data/` and everything under it, in lexical path order. Pass two hashes each file again as it is
      written; a file whose size or hash differs from pass one fails the Backup ("data changed during the
      Backup"). A symlink, device, socket or FIFO under `data/` fails the Backup, naming the path.
   Event `copy instance=kids files=14 bytes=…`.
6. **Verify** the written archive through `Pending.Path()` ([Verification](#verification)). On a mismatch, abort.
7. **Sidecar, then archive.** Write `<id>.tar.gz.sha256` with `hostfs.WriteFile(…, 0600)`, then `Commit` the
   archive. A crash between the two leaves a sidecar and a `.partial`, which the next step 2 deletes. Event
   `backup instance=kids id=kids-20261117T080004Z-manual size=…`.
8. **Start again** if step 4 stopped a `running` or `restarting` container (event `start instance=kids`).

On any failure in steps 5–7 (the context cancelled included) the `.partial` is aborted, step 8 still runs, and the
error is returned. If step 8 itself fails (`ErrNotHealthy` included) after a good Backup, `Create` returns the
`Info` and the error: the Backup stands, the command exits 1, and the hint is `abyssctl logs <name>`.

`PlanCreate` runs steps 1 and 3 and says whether the Instance will be stopped; the plans of `upgrade` and
`restore` use it for their panels, and Lifecycle's `remove` calls it before it stops the Instance, so a Host short
of space refuses the final Backup with the Instance still running.

**Scheduled Backups.** For `KindScheduled`, before it returns, `Create` itself calls
`Store.RecordBackup(name, ok, errText)`: `ok` when the Backup was made and verified (whether or not the start
after it succeeded; the Health check sees a down Instance on its own), otherwise the error's sentence. After a
successful one it applies Retention: `Prune(name, Policy{Keep: Deps.Keep, Kind: KindScheduled})`. A Retention
failure records `ok=false`, "Backup made, but Retention failed: …", and is exit 1. Every other kind leaves
`outcomes.json` alone. A guard refusal is recorded too, so an Instance the timer can never back up raises an
Event Problem ([health.md](health.md)).

### Verification

One routine, run on every new archive before its `Commit` and on every Backup before a Restore or Revert uses it:

1. The sidecar exists and names `<id>.tar.gz`; its SHA-256 equals the archive's (skipped for a new archive,
   whose sidecar is written from this pass).
2. The archive is a gzip'd tar whose first entry is `manifest.json`; the manifest parses, its `schema` is 1, and
   its `id`, `instance` and `kind` agree with the file name. Keys the manifest has beyond those in
   [Files and formats](#manifestjson) are ignored.
3. Every other entry is a folder or a regular file with a relative, clean name under `instance.env` or `data/`
   (no `..`, no absolute path), and no name appears twice; the set of regular files equals the manifest's
   `files`, and each one's size and SHA-256 match.

Any failure is `ErrVerify`, naming the first thing that disagreed. There is no `backup verify` verb.

### `List`

Reads every `<id>.tar.gz` in `backups/<name>/` (a missing folder is an empty list), opens it, and decodes only
`manifest.json`. An archive whose manifest can't be read, or that has no sidecar, is listed with its `Problem`
and the fields its file name gives. `.partial` and sidecar files are not listed. Sorted by `CreatedAt`, newest
first, then by id. `List` never hashes a whole archive, so it is quick; a Backup it lists can still fail
verification at Restore.

### Retention and `Prune`

- `Prune(name, Policy{Keep, Kind})` takes the Instance's Backups of that kind (every kind when `Kind` is ""),
  newest first, and deletes all but the first `Keep`, except that it never deletes the Instance's newest Backup of
  any kind. Each deletion removes the archive, then its sidecar (event `pruned instance=kids id=…`).
- Retention is that call with the `scheduled` kind and `BACKUP_KEEP` (default 14), run only after a successful
  scheduled Backup. No other kind is ever removed automatically.
- A Backup with a `Problem` counts like any other, by the time in its id.

### Restore

`Restore(ctx, name, Selector)`:

1. **Guards.** All four; guard 4 (a `data.pre-restore/` folder) is refused with hint `abyssctl restore <name>`,
   which finishes the interrupted Restore first.
2. **Choose and verify.** With an `ID`, `ParseID` must name this Instance and `<id>.tar.gz` must exist
   (`ErrNoBackup`, exit 2), and the Backup must pass verification (`ErrVerify`, exit 2). With `Latest`, take
   `List` newest first and use the first that passes; each that fails is an event `skipped id=… reason=…`, and
   none passing is exit 2.
3. **Checks.** `Account()`; `Network(names.Network)` exists (else exit 2, hint `abyssctl host setup`); free space
   on the Data root's filesystem is at least the `pre-restore` Backup's need plus the extract's need (the
   [rule](#invariants-and-error-modes)).
4. **Pull.** If `<repo>@<digest>` from the manifest is not local, `Pull` it; a failure is refused (exit 2),
   nothing changed.
5. **Stop** the container if it is running, and remember whether it was `running` or `restarting`.
6. **`pre-restore` Backup**: `Create(ctx, name, KindPreRestore)`. It finds the Instance stopped, so it does not
   start it. If it fails, start the container again if step 5 stopped a `running` or `restarting` one, and return
   its error.
7. **Swap**: steps 1–8 of the shared swap below, called inside Backup (not through the public `Revert`, so
   nothing is verified or pulled twice), with `Regroup` and `UndoFailedStart` set, and with whether step 5
   found the container running, so an undo starts it again.

The panel: title "Restore kids"; rows Backup (id), made (local time), kind, Actual "26.11.0 → 26.10.0" (or "26.11.0,
unchanged"), downtime; the steps above; warnings "changes made after <local time> are lost from the server",
"devices that synced since are told the budget was reset and must revert" and "a pre-restore Backup is taken
first". The outcome line names the `pre-restore` Backup's id.

### The shared swap (`Revert`)

`Revert(ctx, name, backupID, RevertOpts)` first runs guards 1 and 2 (exit 4) and these checks, each refusing (exit 2)
with nothing changed: Restore's step 2 (the id and verification); `Account()`; the `abyssctl` network; free space
for the extract (the [rule](#invariants-and-error-modes)); then Restore's step 4 (pull). Then the swap:

1. **Stop** the container if it is running, and remember whether it was `running` or `restarting`.
2. **Move aside.** Rename `data/` to `data.pre-restore/`. If `data.pre-restore/` already exists (an interrupted
   Rollback being finished by Upgrade), it already holds the data the interrupted swap moved aside: delete the
   partial `data/` and keep `data.pre-restore/`.
3. **Extract** the Backup's `data/` into a new `data/`, owner the account's `UID:GID`, each file's mode from
   the archive, checking each file's hash as it is written. `data/` itself gets mode 0700.
4. **Regroup**, if asked ([Regroup](#regroup)).
5. **Record.** `Get` the record, set `ACTUAL_VERSION` and `ACTUAL_IMAGE_DIGEST` from the Backup's manifest, and
   `Put` it. Every other key stays as it is: `HOSTNAME`, `STATE`, `CREATED_AT`, `UPGRADE_*` and Extra keys.
6. **Compose.** `render.InstanceCompose` with the new image, the account's IDs and the `abyssctl` network's
   subnets; `Store.WriteCompose`.
7. **Start** if `STATE=running`: `ComposeUp` (`Wait`, 5 min), and once it succeeds write `TOOL_VERSION` =
   `Deps.Version`. With `STATE=stopped` nothing starts and `TOOL_VERSION` stays.
8. **Clean up.** Delete `data.pre-restore/`. Event `restored instance=kids id=… regrouped=true`.

**Undo.** A failure in steps 2–7, including `ErrNoGroupFile` from the Regroup, `ErrNotHealthy` and a cancelled
context, undoes on `context.WithoutCancel(ctx)`: stop the container, delete the new `data/`, rename
`data.pre-restore/` back to `data/`, `Put` the record as it was before step 5, write back the old `compose.yaml`,
and start the container if it was `running` or `restarting` when Restore or `Revert` began. The error returned
is the failure (exit 1). If the undo fails too, the error is `op.NeedsOperator("the Restore of kids could not be
undone", "abyssctl restore kids")` with both errors as causes, and `data.pre-restore/` stays, so every changing
command refuses until it is finished.

**With `UndoFailedStart` false** (Rollback), a failure at step 7 is not undone: Backup stops the container and
returns the error, leaving the Backup's `data/`, the record from step 5 (the Backup's version and digest) and
`data.pre-restore/` in place, and deleting nothing. Failures in steps 2–6 are undone as above.

Upgrade's Rollback calls `Revert` with the `pre-upgrade` Backup, `Regroup` only when `UPGRADE_HEALTHY` is set, and
`UndoFailedStart` false; it clears `UPGRADE_BACKUP` and `UPGRADE_HEALTHY` itself afterwards
([upgrade.md](upgrade.md)). Upgrade reports a failed `Revert` as `rollback_failed`, exit 4, hint
`abyssctl upgrade <name>`.

### Regroup

With the container stopped, on the extracted `data/`, through `modernc.org/sqlite`
([ADR 0004](../adr/0004-restore-regroups-offline.md)):

1. Open `server-files/account.sqlite` and read the distinct non-null `group_id` values of `files`. A missing
   database or table fails the Restore.
2. For each, check that `user-files/group-<old>.sqlite` exists; one missing is `ErrNoGroupFile`, before any change.
3. Give each old id a new uuidv4 from `crypto/rand`, and in one transaction `UPDATE files SET group_id = new
   WHERE group_id = old` for every pair. Close the database.
4. Rename each `group-<old>.sqlite` to `group-<new>.sqlite`, with its `-wal`, `-shm` and `-journal` files when
   present. Event `regrouped instance=kids files=3`.
5. Set the owner of every path under `data/` to the account again, since the SQLite edit ran as root.

The Regroup never deletes a group file: deleting it loses sync messages that are not yet in the snapshot.

### Finishing an interrupted Restore

`data.pre-restore/` with nobody holding the Instance lock means a Restore or a Rollback stopped part-way. Every
changing command refuses (exit 4); `status` shows it ([lifecycle.md](lifecycle.md)). `restore <name>` with no id
calls `Finish`:

1. If `UPGRADE_BACKUP` is set, the swap was a Rollback's: refuse (exit 4, hint `abyssctl upgrade <name>`), which
   finishes it with the `pre-upgrade` Backup. Guard order already sends the Operator there; this is a second
   check.
2. Otherwise `data.pre-restore/` holds the data from before the Restore, and the newest `pre-restore` Backup (made
   just before the move) holds the same data and its record. Without such a Backup, refuse (exit 4) with a hint
   naming `docs/guides/backup-and-restore.md`'s by-hand steps.
3. Stop the container if it is running, delete `data/` if present, rename `data.pre-restore/` to `data/`, set
   the record's version and digest from that Backup's manifest, regenerate `compose.yaml`, and start the Instance if
   `STATE=running` (5 min), then write `TOOL_VERSION`. No Regroup: these are the data the devices last synced with.

The outcome line says the Instance is as it was before the Restore and to run `restore` again. `PlanFinish` shows
the same steps; `restore <name> --dry-run` shows it when `Interrupted` is true.

## 4. Files and formats it owns

### `backups/<name>/`

| File | Mode | What |
|---|---|---|
| `<id>.tar.gz` | 0600 | the Backup archive |
| `<id>.tar.gz.sha256` | 0600 | `<64 lowercase hex>  <id>.tar.gz\n`, the format `sha256sum -c` reads |
| `<id>.tar.gz.<8 hex>.partial` | 0600 | an archive being written (`hostfs.Create`) |

Folder 0700, owner root:root. The id is `<name>-<stamp>-<kind>` with `hostfs.Stamp` (UTC, e.g.
`kids-20261117T080004Z-pre-upgrade`); it sorts by time within a kind, and `Info.CreatedAt` is its stamp.
`UPGRADE_BACKUP` holds such an id ([store.md](store.md)). [Store](store.md) moves the whole folder on `remove`.

### The archive

A gzip'd POSIX tar (`archive/tar`, PAX), entries in this order:

```
manifest.json
instance.env
data/
data/server-files/
data/server-files/account.sqlite
data/user-files/
data/user-files/file-<id>.blob
data/user-files/group-<id>.sqlite
…
```

Only folders and regular files. Owners in the tar are numeric and ignored on extract. Modification times are kept.

### `manifest.json`

```json
{
  "schema": 1,
  "id": "kids-20261117T080004Z-scheduled",
  "instance": "kids",
  "kind": "scheduled",
  "created_at": "2026-11-17T08:00:04Z",
  "actual_version": "26.11.0",
  "actual_image_digest": "sha256:…",
  "tool_version": "0.1.0",
  "files": [
    { "path": "instance.env", "size": 212, "sha256": "…" },
    { "path": "data/server-files/account.sqlite", "size": 40960, "sha256": "…" }
  ]
}
```

Keys snake_case, times RFC 3339 UTC, `files` in archive order. A Restore refuses a schema other than 1. A
manifest written by a newer Abyss with schema 1 restores normally.

### Package layout

```
internal/backup/
  backup.go    New, Deps, Kind, Info, ParseID, errors
  create.go    Create, PlanCreate, the sweep, free space, scheduled outcome and Retention
  archive.go   tar writer and reader, manifest, extract
  verify.go    verification
  list.go      List
  prune.go     Prune, PlanPrune
  restore.go   Restore, PlanRestore, Finish, PlanFinish, Interrupted
  revert.go    Revert and its undo
  regroup.go   Regroup (the only file that imports modernc.org/sqlite)
  statfs_linux.go  the production FreeSpace
```

## 5. Depends on

- [foundation](foundation.md): `hostfs.Paths`, `hostfs.Create` (`Pending.Path`, `Commit`, `Abort`),
  `hostfs.WriteFile`, `hostfs.EnsureDir`, `hostfs.Stamp`/`ParseStamp`; `op.Emitter`, `op.Clock`, `op.Summary`,
  the typed errors; `buildinfo.Version`, `buildinfo.ActualRepo`, `buildinfo.CompareVersions`.
- [docker-and-locks](docker-and-locks.md): `docker.Client` (`Container`, `ComposeStop`, `ComposeUp`, `Image`,
  `Pull`, `Network`) and its error kinds; `docker/names.InstanceProject`. The CLI's `locks.Locker`.
- [store](store.md): `ValidName`, `Get`, `List`, `Put`, `WriteCompose`, `RecordBackup`, `Owner`,
  `Record.Image`.
- [render](render.md): `InstanceCompose`. The backup timer and service units are render's and Host's.
- [host](host.md): `Account` and `Settings.BackupKeep`, passed in by the CLI.
- Called by: [lifecycle](lifecycle.md) (`PlanCreate` and `Create`, `KindFinal`), [upgrade](upgrade.md) (`PlanCreate`,
  `Create(KindPreUpgrade)`, `List`, `Revert`), and `internal/cli`.
- New dependency: `modernc.org/sqlite`, already on foundation's allowed list.

Import rule this spec adds to `internal/archtest`: `internal/backup` imports only the standard library,
`internal/hostfs`, `internal/op`, `internal/buildinfo`, `internal/docker`, `internal/docker/names`,
`internal/store`, `internal/render` and `modernc.org/sqlite`. Only `internal/cli`, `internal/lifecycle` and
`internal/upgrade` import `internal/backup`.

## 6. Decisions

- **Stop, copy, start.** A stopped Actual can't be mid-write, so the copy is consistent without Actual's
  cooperation or a SQLite-aware copy. The Instance is down for the length of the copy; a nightly Backup at 03:00
  keeps that out of the way.
- **One archive, a manifest and a sidecar.** A `.tar.gz` per Backup is easy to copy off the Host and to check with
  `sha256sum -c`. The manifest comes first so `List` reads it without reading the archive.
- **Verify twice.** Every Backup is read back before it exists under its final name, and again before a Restore
  uses it. There is no standalone verify verb.
- **Retention only for scheduled Backups.** The other kinds mark a moment the Operator may want later; only the
  Operator removes them. The newest Backup is never pruned.
- **Restore brings back the Backup's Actual version and always Regroups**
  ([ADR 0004](../adr/0004-restore-regroups-offline.md)). It takes a `pre-restore` Backup first, so a Restore can
  itself be undone by restoring that Backup.
- **Deleted budgets Regroup like the rest.** Actual deletes a budget softly (`deleted=1` in `files`) and keeps
  its `file-<id>.blob` and `group-<id>.sqlite`, so every `group_id` the Regroup finds has its group file, deleted
  budgets included.
- **The undo is a rename.** The old `data/` is moved aside, not deleted, until the restored Instance is healthy,
  so putting it back is instant and needs no second extract.
- **Dependency: `modernc.org/sqlite`.** The Regroup edits SQLite offline; this is a pure-Go SQLite with no cgo,
  so the binary stays static ([ADR 0003](../adr/0003-single-static-go-binary.md)). Only `internal/backup` imports it.
- **Backups are not encrypted** and live on the Host under root-only modes. Encryption comes with off-host
  Backups.
- **Restore takes only the Instance lock:** it changes no site file and no Host-wide state.

## 7. Later

- Off-host Backups, and Backup encryption with them (ROADMAP Next).
- Restoring a Backup into a new Instance, and from a removed Instance's Backups, with the clone family.
- Encrypted Backups on the Host (ROADMAP Later).

## 8. Docs it changes

- `docs/guides/backup-and-restore.md`: the Backup kinds and ids; what `backup`, `backup --all`, `backup list` and
  `backup prune` do; the nightly timer, `BACKUP_TIME` and `BACKUP_KEEP`; what the nightly run does (every
  Instance, a busy one skipped, Retention of scheduled Backups only, a failure becoming a Problem); the brief stop; checking a copy with
  `sha256sum -c`; `restore` by id, `--latest` and the pick list; that a Restore brings back the Backup's Actual
  version, takes a `pre-restore` Backup and Regroups; what devices see (`file-has-reset`, revert) and that their
  later changes are lost; finishing an interrupted Restore with `restore <name>`; restoring by hand.
- `docs/guides/upgrades.md`: undoing an Upgrade later with `restore <name> <pre-upgrade id>`.
- `docs/guides/security.md`: Backups are unencrypted, root-only; copy them off the Host with care.
- Reference: the pages of `backup`, `backup list`, `backup prune` and `restore`, from their help text.

## 9. Acceptance list

Module tests are in `internal/backup`, on `hostfs.Under(t.TempDir())` with a Data root, a real Store,
`dockerfake`, a fake `FreeSpace`, a fake `Account`, an `op.Recorder` and an `op.ManualClock`, without root. Regroup
tests use a small Actual-shaped `data/` fixture built with `modernc.org/sqlite`. Ownership tests skip when not run
as root. Journey items run the real binary as root on an Ubuntu runner: this spec's journey slice is journey 5
(Backup and Restore).

1. `Create` of a `running` or `restarting` Instance calls `ComposeStop`, copies, then `ComposeUp` with `Wait` and
   5 min; of a `paused` one it stops and does not start; of an exited one or one with no container it copies and
   starts nothing. → `TestCreate_StopCopyStart` (table, every kind and container state)
2. When the copy fails, or the context is cancelled, `Create` still starts a container it stopped, on a context
   that is not cancelled, and leaves no `.partial`. → `TestCreate_CopyFails` (table)
3. `Create` deletes leftover `.partial` files and orphan sidecars first, and refuses (exit 2) with nothing written
   when free space is below the Backup's need; `PlanCreate` gives the same refusal, also before the Instance's
   first Backup, when `backups/<name>/` does not exist yet. → `TestCreate_Sweep`, `TestCreate_FreeSpace` (table)
4. The archive holds `manifest.json` first, `instance.env`, then `data/` in lexical order; the manifest has the
   record's version and digest, the binary's version and every file's size and hash; the id is
   `<name>-<stamp>-<kind>`; the sidecar is in `sha256sum -c` format; both files are 0600. → `TestCreate_Archive`
5. A second Backup of the same kind in the same second waits for the next second and gets its own id. →
   `TestCreate_SameSecond`
6. A file that changes between the two passes, or a symlink under `data/`, fails the Backup. →
   `TestCreate_DataChanged`, `TestCreate_SpecialFile`
7. A restart that is not healthy after a good Backup returns the `Info` and an error with the `logs` hint, and
   the Backup stays. → `TestCreate_StartFails`
8. `Create` of every kind refuses with the four guards, in Lifecycle's order (all exit 4; an invalid record with
   hint `fix <file> by hand`, and one whose `TOOL_VERSION=` is newer with the `install abyssctl` hint), and never
   changes the record. →
   `TestCreate_Guards` (table)
9. `Create(KindScheduled)` records `ok` or the error with `RecordBackup` before it returns, a guard refusal
   included, then applies Retention; a Retention failure records `ok=false` ("Backup made, but Retention
   failed") and is exit 1; other kinds leave `outcomes.json` alone. → `TestCreate_Scheduled_RecordsOutcome` (table)
10. With 16 scheduled Backups and `Keep` 14, Retention deletes the two oldest scheduled ones and no other kind;
    `Prune` never deletes the newest Backup; a `Policy` with no `Kind` counts every kind together. →
    `TestRetention`, `TestPrune` (table)
11. `ParseKind` accepts the five kinds only; `ParseID` parses from the right, so names with `-` work, and refuses a
    malformed id or an unknown kind. → `TestParseKind`, `TestParseID` (tables)
12. Verification catches a changed byte, a missing, extra or duplicate entry, a wrong sidecar, a `..` or absolute
    name, a non-regular entry and an unknown schema, and ignores unknown manifest keys. → `TestVerify` (table)
13. `List` returns newest first, reads only manifests, and lists a broken archive or one without a sidecar with a
    `Problem`. → `TestList`
14. `Restore` calls, in order: verify, `Pull` (only when not local), `ComposeStop`, `Create(KindPreRestore)`
    (which does not start it), rename to `data.pre-restore/`, extract, Regroup, `Put`, `WriteCompose`, `ComposeUp`
    (only if `STATE=running`), `TOOL_VERSION`, delete `data.pre-restore/`; it verifies and pulls once; the record
    keeps `HOSTNAME`, `STATE` and Extra keys and gets the Backup's version and digest; with `STATE=stopped`
    nothing starts and `TOOL_VERSION` is unchanged. → `TestRestore_Order`
15. `PlanRestore` and `Restore` refuse with nothing changed: an unknown id, another Instance's id, a Backup failing
    verification, a failed pull, no network, too little free space, and the guards. → `TestRestore_Refusals` (table)
16. When the `pre-restore` Backup fails, `Restore` starts again the container it stopped and changes nothing
    else. → `TestRestore_PreRestoreFails`
17. `--latest` picks the newest Backup of any kind that passes verification, skipping a broken newer one. →
    `TestRestore_Latest`
18. A failure at each swap step, `ErrNoGroupFile`, `ErrNotHealthy` and a cancelled context included, puts back the
    old `data/`, record and `compose.yaml` and starts the Instance if it was running when Restore began (exit 1);
    a failing undo is exit 4 with hint `abyssctl restore <name>` and leaves `data.pre-restore/`. →
    `TestRevert_Undo` (table)
19. With `UndoFailedStart` false, a failed start stops the container and leaves the Backup's `data/`, the Backup's
    version and digest in the record and `data.pre-restore/`, deleting nothing; a failure before the start is
    still undone. → `TestRevert_NoUndoFailedStart` (table)
20. Regroup gives every `group_id` a new uuidv4, renames each group file and its companions, deletes none, and
    fails before any SQLite change when a group file is missing. → `TestRegroup`, `TestRegroup_MissingGroupFile`
21. `Revert` with `Regroup: false` leaves `group_id`s and file names alone and takes no Backup; it runs with
    `UPGRADE_BACKUP` set and with a leftover `data.pre-restore/` (keeping that and discarding `data/`), and
    refuses with nothing changed on guards 1 and 2. → `TestRevert` (table, a row per guard)
22. Extracted files are owned by the account's `UID:GID` after a Restore. → `TestRestore_Owner` (root only)
23. `Finish` puts `data.pre-restore/` back with the newest `pre-restore` Backup's version and starts the Instance
    if wanted; it refuses (exit 4) with hint `abyssctl upgrade <name>` when `UPGRADE_BACKUP` is set, and (exit 4)
    when there is no `pre-restore` Backup. → `TestFinish` (table)
24. Each `Plan…` returns the same refusal as its run and takes no lock, writes, pulls or stops nothing. →
    `TestPlansMatchRuns` (table)
25. No event field, `Summary` or error holds anything from inside `data/`. → `TestEvents_NoData`
26. `internal/backup`'s imports are those in the import rule; only `internal/backup` imports SQLite;
    only cli, lifecycle and upgrade import backup. → `TestImportRules` in `internal/archtest`
27. The CLI takes the locks in the table; `--scheduled` skips a held Instance lock and logs it; `--all` goes on
    after a failure and exits 1; `--scheduled` without `--all` is refused (exit 2). → `TestBackupCmds_Locks`,
    `TestBackupAllCmd` (table) in `internal/cli`
28. `backup prune` asks y/N and `restore` the typed name, also when `restore <name>` finishes an interrupted
    Restore; `--yes` skips both; no TTY and no `--yes` is refused (exit 2). → `TestBackupCmds_Prompts` (table, with
    a finish row) in `internal/cli`
29. `restore <name>` on a TTY shows the pick list newest first and restores the one chosen; with no TTY and
    nothing interrupted it is refused (exit 2) naming `restore <name> <backup-id>` and `--latest`; with
    `data.pre-restore/` it shows the finish panel, asks the typed name with no pick list (`--yes` skips it) and calls
    `Finish`; an id given while `data.pre-restore/` exists is exit 4. → `TestRestoreCmd_Selection` (table) in `internal/cli`
30. `--dry-run` on `backup prune` and `restore` prints the panel, takes no lock and changes nothing. →
    `TestBackupCmds_DryRun` in `internal/cli`
31. `backup list` with no name lists every Instance's Backups and not `backups/removed/`; `--json` carries
    `"schema": 1`, snake_case keys and RFC 3339 UTC times. → `TestBackupListCmd_All`, `TestBackupListCmd_JSON` in
    `internal/cli`
32. Journey 5: `backup kids` and `backup --all` exit 0; `backup list --json` shows the `manual` Backups; each archive
    and sidecar is root:root 0600 and `sha256sum -c` passes. → journey 5
33. Journey 5: after a change made since the Backup, `restore kids <id> --yes` exits 0, a `pre-restore` Backup
    exists, no `data.pre-restore/` remains, an old client gets `file-has-reset`, and a fresh download holds only
    what was in the Backup. → journey 5
34. Journey 5: `systemctl start abyssctl-backup.service` with `bob`'s Instance lock held exits 0, takes a
    `scheduled` Backup of `kids`, logs `skipped instance=bob reason=busy`, and records the run in `outcomes.json`.
    → journey 5
35. Journey 6: `restore kids <pre-upgrade id> --yes` brings back the old Actual version in the record and on the
    container; `backup prune kids --keep 1 --yes` leaves one Backup. → journey 6 (checked by
    upgrade.md's journey 6 slice)
36. `docs/guides/backup-and-restore.md`, `upgrades.md` and `security.md` say what [Docs it changes](#8-docs-it-changes)
    lists. → (review)
37. The reference pages of `backup`, `backup list`, `backup prune` and `restore` match their help text. → the
    generated-reference check in CI (review)
