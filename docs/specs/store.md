# Spec: store

Build step 3 of 16. The [overview](README.md) has the module map, the call graph and the Host layout. Shared
packages (`internal/hostfs`, `internal/op`, `internal/buildinfo`) are in [foundation.md](foundation.md); locks are
in [docker-and-locks.md](docker-and-locks.md). Capitalised terms are defined in [GLOSSARY.md](../../GLOSSARY.md).

## 1. Purpose

Build the Instance store (`internal/store`): the one module that knows what an
[Instance](../../GLOSSARY.md) is on disk. It owns:

- the [Instance record](../../GLOSSARY.md) (`instances/<name>/instance.env`): its `KEY=value` format, every key,
  parsing, validation and writing;
- the [Instance name](../../GLOSSARY.md) rules and the [Hostname](../../GLOSSARY.md) rules, including
  uniqueness across Instances;
- the Instance folder: which files sit in it and who writes each one, and the writing of the Instance's
  `compose.yaml` (content from the [Renderer](render.md));
- building a new Instance in `instances/.<name>.creating/` and committing it into place, so `create` is
  all-or-nothing;
- deleting an Instance folder on `remove`, and moving its [Backups](../../GLOSSARY.md) aside to
  `backups/removed/`;
- listing Instances, including folders whose record is invalid;
- `outcomes.json`: the last scheduled Backup's result and the last [Upgrade](../../GLOSSARY.md)'s result, which
  the [Health check](../../GLOSSARY.md) turns into Event [Problems](../../GLOSSARY.md).

Store is a leaf: it calls no other module, never runs Docker, never takes a lock and never emits events. It is the
only writer of `instance.env`: every other module changes a record through `Put`. The
disk is the record ([ADR 0002](../adr/0002-instance-folder-is-the-record.md)): an Instance exists exactly when
`instances/<name>/instance.env` holds a valid Instance record. When this spec is done, later modules can create,
read, change, list and delete Instance folders, all testable under `t.TempDir()` without root.

## 2. Interface

Signature sketches, not final code.

```go
// One Store per process, built by the CLI. toolVersion is buildinfo.Version, written as TOOL_VERSION by Stage.
// The Store keeps no state between calls and is safe for concurrent use.
func New(p hostfs.Paths, clock op.Clock, toolVersion string) *Store

// Names and Hostnames.
func ValidName(name string) error         // nil, or op.Refused naming the rule broken
func ValidHostname(hostname string) error // nil, or op.Refused naming the rule broken

// The record.
type State string

const (
    Running State = "running"
    Stopped State = "stopped"
)

type Record struct {
    Name           string    // the folder name; never a key in the file
    Hostname       string    // HOSTNAME
    ActualVersion  string    // ACTUAL_VERSION, "26.11.0"
    ActualDigest   string    // ACTUAL_IMAGE_DIGEST, "sha256:<64 hex>"
    State          State     // STATE: what the Operator wants, not what Docker reports
    Internet       bool      // INTERNET: the Instance has Internet access; false leaves the key out
    CreatedAt      time.Time // CREATED_AT, UTC, whole seconds
    ToolVersion    string    // TOOL_VERSION, SemVer without "v"
    UpgradeBackup  string    // UPGRADE_BACKUP: the pre-upgrade Backup id, "" when no Upgrade is under way
    UpgradeHealthy bool      // UPGRADE_HEALTHY: the new version has passed /health during this Upgrade
    Extra          []KV      // unknown keys, in file order; written back after the known keys
    Warnings       []Problem // from parsing (an unknown key, a comment); never written
}

type KV struct{ Key, Value string }

type Problem struct {
    Line int    // 1-based; 0 when the problem is not on one line (a missing key, a missing file)
    Text string // one sentence, e.g. "STATE must be running or stopped, not \"up\""
}

func Parse(name string, data []byte) (Record, error) // *RecordError if invalid; checks no other Instance
func Format(r Record) ([]byte, error)               // validates first; the bytes Put would write
func (r Record) Image(repo string) string           // "<repo>:<ActualVersion>@<ActualDigest>"

// Reading.
func (s *Store) Get(name string) (Record, error)
func (s *Store) List() ([]Entry, error)

type Entry struct {
    Name   string
    Record *Record      // nil when the record is invalid
    Err    *RecordError // nil when the record is valid
}

// Changing an existing Instance. Caller holds the Instance lock.
func (s *Store) Put(r Record) (changed bool, err error)
func (s *Store) WriteCompose(name string, data []byte) (changed bool, err error)
func (s *Store) RecordBackup(name string, ok bool, errText string) error // the scheduled Backup's result
func (s *Store) RecordUpgrade(name string, result UpgradeResult) error
func (s *Store) Outcomes(name string) (Outcomes, error)                  // reading needs no lock

// Creating. Caller holds the Host lock from CheckNew through Commit (or Discard).
func (s *Store) CheckNew(spec NewInstance) error
func (s *Store) OldBackups(name string) (bool, error) // whether backups/<name>/ exists, which Stage moves aside
func (s *Store) Stage(spec NewInstance) (*Staged, error)

type NewInstance struct {
    Name, Hostname, ActualVersion, ActualDigest string
    Internet                                    bool  // create --internet: written as INTERNET=1
    DataOwner                                   Owner // the abyssctl account's actual UID:GID
}

type Owner struct{ UID, GID int }

type Staged struct{ /* unexported */ }

func (st *Staged) Record() Record
func (st *Staged) LockPath() string                    // instances/.<name>.creating/.lock
func (st *Staged) BackupsMovedTo() string              // backups/removed/<name>-<stamp>/, or "" when none moved
func (st *Staged) WriteCompose(data []byte) error
func (st *Staged) Commit() error                       // caller holds the flock on LockPath()
func (st *Staged) Discard() error                      // before or after Commit; safe to call twice

// Removing. Caller holds the Host lock and the Instance lock.
func (s *Store) Remove(name string) (Removed, error)

type Removed struct {
    BackupsMovedTo string // backups/removed/<name>-<stamp>/, or "" when the Instance had no Backups
}

// Cleaning up. Caller holds the Host lock.
func (s *Store) Sweep() ([]string, error) // the leftover folders it deleted

// Errors.
var (
    ErrNoInstance    = fmt.Errorf("no such Instance: %w", fs.ErrNotExist) // matches fs.ErrNotExist
    ErrInvalidRecord = errors.New("invalid Instance record")              // matched by every *RecordError
)

type RecordError struct {
    Path        string    // the record's full path
    Problems    []Problem // every problem found, in line order
    ToolVersion string    // the TOOL_VERSION= line's value when it is a valid version, else ""
}
func (e *RecordError) Error() string   // "<path>:<line>: <text>" for the first, then "(and N more)"
// Unwrap gives ErrInvalidRecord and op.NeedsOperator(e.Error(), "fix <path> by hand"), so a *RecordError is
// exit 4 and op.Hint finds its hint.
func (e *RecordError) Unwrap() []error

// outcomes.json.
type UpgradeResult string

const (
    UpgradeOK             UpgradeResult = "ok"
    UpgradeRolledBack     UpgradeResult = "rolled_back"
    UpgradeRollbackFailed UpgradeResult = "rollback_failed"
)

type Outcomes struct {
    ScheduledBackup *BackupOutcome  // nil until the first scheduled Backup of this Instance
    Upgrade         *UpgradeOutcome // nil until the first Upgrade of this Instance
}

type BackupOutcome struct {
    At    time.Time // UTC
    OK    bool
    Error string    // "" when OK
}

type UpgradeOutcome struct {
    At     time.Time // UTC
    Result UpgradeResult
}
```

Invariants:

- **Every path comes from `hostfs.Paths`.** Store builds no path itself: the record is `InstanceRecord(name)`, the
  staging folder `CreatingDir(name)` and its lock `CreatingLock(name)`, the folder being deleted
  `RemovingDir(name)`, the Backups folder `InstanceBackups(name)` and the removed Backups
  `RemovedBackups(name, at)`.
- **Every file goes through `hostfs.WriteFile`**, and every folder through `hostfs.EnsureDir`, with the modes in
  the overview's [Host layout](README.md#host-layout): folders 0700, files 0600. A reader never sees part of a
  record, a `compose.yaml` or an `outcomes.json`.
- **Names are checked before any path is built.** Every method that takes a name calls `ValidName` first, so a
  name such as `../x` or `.kids.creating` never reaches the disk.
- **Store never locks.** Each method states the lock its caller holds. The one lock-related thing Store does is
  create the staged Instance's lock file, so the caller can lock it before `Commit` (below).
- **No Secret passes through Store.** The record holds none ([Secret](../../GLOSSARY.md)); `outcomes.json` holds
  only times, results and the error sentence its caller gives. Store never reads inside `data/`.
- **Errors.** `Refused` (exit 2) for an invalid name or Hostname and a name or Hostname already taken. An invalid
  record is a `*RecordError` (`ErrInvalidRecord`): `op.NeedsOperator`, exit 4, its message naming the file, line
  and problem, hint `fix <file> by hand`. `ErrNoInstance` (exit 2) when the Instance folder is missing;
  [cli.md](cli.md) owns how it is shown. Anything else (disk, permissions) is a plain wrapped error (exit 1).

### `Get` and `List`

- `Get(name)` returns the record, or `ErrNoInstance` when `instances/<name>/` does not exist, or a `*RecordError`
  when the folder exists but its record is missing or invalid. A valid record may carry `Warnings`; the caller
  shows them.
- `List()` reads every entry in `instances/` and returns one `Entry` per Instance folder, sorted by name. It skips
  every entry whose name starts with `.` (the `.creating` and `.removing` folders) and every entry that is not a
  folder. A folder whose name is not a valid Instance name is listed with an `Err` ("not a valid Instance name"),
  so `status` and the Health check report it, and nothing acts on it.
- `Get(name)` returns exactly what `List()` returns for that name, Hostname uniqueness included.
- Neither takes a lock or writes anything. Because every write is atomic, a read sees each file whole, old or new.

### `Put` and `WriteCompose`

- `Put(r)` writes `Format(r)` to `instances/<r.Name>/instance.env`. Precondition: the caller holds the Instance
  lock. It refuses with `ErrNoInstance` when the folder is missing (it never creates an Instance) and with a
  `*RecordError` when `r` is invalid or its Hostname is used by another Instance. It writes only when the bytes
  differ from the file on disk and returns whether it wrote.
- `Put` writes what it is given. Callers get a record with `Get`, change fields and `Put` it back, so `Extra`
  keys survive. The rules for who changes which key are in [Behaviour](#who-changes-which-key).
- `WriteCompose(name, data)` writes `instances/<name>/compose.yaml` with exactly `data`, only when it differs, and
  returns whether it wrote. Precondition: the Instance lock. The content is the Renderer's; Store neither reads
  nor checks it.

### Creating: `CheckNew`, `Stage`, `Commit`, `Discard`

- `CheckNew(spec)` runs every check `Stage` makes (name, existing Instance, Hostname and its uniqueness,
  version, digest, `DataOwner`), writes nothing and returns the same error `Stage`
  would. Lifecycle's `PlanCreate` uses it, so `create --dry-run` refuses exactly when `create` would. The one
  difference: an empty `ActualDigest` skips the digest check in `CheckNew` (the interim `create --dry-run`, which
  knows no digest yet, [lifecycle.md](lifecycle.md)); `Stage` always refuses it.
- `OldBackups(name)` reports whether `backups/<name>/` exists, so `create`'s panel can name the move `Stage`
  makes. It reads only; no lock.
- `Stage(spec)` builds the new Instance in `instances/.<name>.creating/` and returns a `*Staged`. Precondition:
  the caller holds the Host lock, and keeps holding it until `Commit` or `Discard` returns. A `backups/<name>/`
  left from an earlier Instance of that name (one deleted by hand, without `remove`) is moved aside first, the
  same move `Remove` makes, so a new Instance never inherits Backups it did not take; `Staged.BackupsMovedTo()`
  says where, for the caller's event.
- `Staged.LockPath()` is the lock file inside the staging folder. **The caller locks it with
  `internal/locks` before `Commit`.** Because `Commit` renames the folder, the same file then sits at
  `instances/<name>/.lock` and the `flock` the caller holds is the Instance lock: a command that waits for the
  new Instance's lock waits for the whole `create`, never slips in between the rename and the first
  `compose up`.
- `Commit()` moves the staging folder into place. After it, `Get(name)` sees the Instance. Precondition: the
  caller holds the flock on `LockPath()`; Store cannot check it and relies on the caller.
- `Discard()` deletes the new Instance whatever state it is in: the staging folder before `Commit`, the whole
  Instance folder after it (the way `Remove` deletes one, without touching `backups/`). It is what `create` calls
  on any failure. It is safe to call twice and after a failed `Commit`.

### `Remove`

- `Remove(name)` moves `backups/<name>/` aside and deletes `instances/<name>/` with everything in it, `data/`
  included. Preconditions, which Lifecycle's `remove` meets first ([lifecycle.md](lifecycle.md)): the caller
  holds the Host lock and the Instance lock, the final Backup has succeeded, the site file is deleted and the
  Instance's containers are down. Store checks none of these except that the folder exists.
- It refuses with `ErrNoInstance` when the folder is missing. It does not need a valid record: deleting the folder
  of an invalid record works the same way.

### `outcomes.json`: `RecordBackup`, `RecordUpgrade`, `Outcomes`

- `RecordBackup(name, ok, errText)` sets the scheduled Backup section, and `RecordUpgrade(name, result)` the
  Upgrade section, each with `At` from the Store's `Clock`. Each reads the file, replaces its own section, keeps
  the other and writes the file back. Precondition: the caller holds the Instance lock, which is why a read and
  write is safe.
- Only [Backup](backup.md) calls `RecordBackup`, and only for a scheduled Backup, including a refused one (any other kind of Backup leaves
  the file alone). Only [Upgrade](upgrade.md) calls `RecordUpgrade`: once per Upgrade, when it ends, including an
  interrupted Upgrade it finishes.
- `errText` is one sentence for the Operator, never a Secret (the caller's rule, as for event fields). Store
  turns line breaks into spaces and keeps at most 500 bytes, cut on a UTF-8 boundary.
- `Outcomes(name)` returns an empty `Outcomes` when the file does not exist, `ErrNoInstance` when the Instance
  folder is missing, and an error when the file does not parse. A `Record…` call on an unreadable file starts from
  an empty `Outcomes` and replaces it.

### `Sweep`

- `Sweep()` deletes every `instances/.<name>.creating/` and `instances/.<name>.removing/` folder and returns
  their paths, so the caller can log each one. Precondition: the caller holds the Host lock, so no `create` or
  `remove` is under way and every such folder is a leftover of one that crashed. `host setup` calls it
  ([host.md](host.md)); `Stage` does the same for its own name.

## 3. Behaviour

### Reading a record

1. Read `instances/<name>/instance.env`. Missing → a `*RecordError` with the problem "instance.env is missing".
   Larger than 64 KiB → "instance.env is larger than 64 KiB".
2. Before the full check, find the first line that starts `TOOL_VERSION=`. When its value is a valid version
   (`buildinfo.ValidVersion`), a `*RecordError` from the steps below carries it as `ToolVersion`, so callers can
   refuse a record a newer Abyss wrote with the "install newer" hint even when this binary can't parse the rest
   ([lifecycle.md](lifecycle.md)'s guards).
3. Parse line by line (format in [section 4](#the-instance-record-instanceenv)). A blank line, or a line whose
   first character is `#`, is skipped with a warning ("comments are not kept; the next write drops them"). Every
   other line must be `KEY=value`; anything else is a problem on that line.
4. Check each known key's value, every required key is present, no key appears twice, and the rules across keys.
5. Check Hostname uniqueness: the record's `HOSTNAME` against the `HOSTNAME=` line of every other Instance folder's
   `instance.env`, valid or not, including folders whose name is not a valid Instance name (they are listed, so
   the Operator sees them). Dot folders (`.creating`, `.removing`) do not count. A clash is a problem on both records, so neither Instance can be changed until
   the Operator fixes one.
6. Collect every problem. Any problem → `*RecordError` with all of them, in line order. None → the `Record`, with
   one warning for each unknown key ("unknown key FOO is kept").

### Writing a record

`Format` writes the known keys in the fixed order of [section 4](#the-instance-record-instanceenv), each as
`KEY=value` and a `\n`, leaving out `INTERNET`, `UPGRADE_BACKUP` and `UPGRADE_HEALTHY` when they are unset; then
each `Extra` key in its order. No comments, no blank lines, no quoting. `Format` validates first and returns the same
`*RecordError` `Parse` would, so an invalid record is never written. `Parse(name, Format(r))` gives back `r`
(without `Warnings`), and `Format(Parse(name, b))` gives back `b` for any record the tool wrote.

### Who changes which key

Store enforces the format; the callers keep to these rules, each tested in its own spec.

| Key | Set by |
|---|---|
| `HOSTNAME` | `Stage`. Only a hand edit changes it later; Store accepts a valid, unique new Hostname, and the next command that acts on the Instance also checks that a Certificate covers it ([proxy.md](proxy.md)) before it regenerates the site file. |
| `ACTUAL_VERSION`, `ACTUAL_IMAGE_DIGEST` | `Stage`; Upgrade; Restore (the Backup's version and digest, keeping `HOSTNAME`, `STATE` and `INTERNET`); Rollback, whose Revert sets the `pre-upgrade` Backup's version and digest. Restore and Revert write it through `Put` ([backup.md](backup.md)). |
| `STATE` | `Stage` (`running`); `start`, `restart`, `stop`. Restore and Upgrade keep it. |
| `INTERNET` | `Stage` (`create --internet`); `set <name> --internet=on\|off` ([lifecycle.md](lifecycle.md)). Upgrade, Restore and Revert keep the current record's value: a Backup's record never changes it. |
| `CREATED_AT` | `Stage`, from the Store's `Clock`. Never changed. |
| `TOOL_VERSION` | `Stage`; and only the commands that create or recreate the container: `start`, `restart`, `upgrade`, `restore` and Rollback. It is the tool version whose generated files the running container was created from. |
| `UPGRADE_BACKUP` | Upgrade, together with the new version and digest, once the `pre-upgrade` Backup exists; cleared together with `UPGRADE_HEALTHY` when the Upgrade or its Rollback ends. |
| `UPGRADE_HEALTHY` | Upgrade, the first time the new version passes `/health`. A Rollback Regroups only when it is set ([upgrade.md](upgrade.md)). |

`UPGRADE_BACKUP` set while nobody holds the Instance lock means an interrupted Upgrade: the Health check raises a
Problem and `upgrade <name>` finishes it ([upgrade.md](upgrade.md), [health.md](health.md)).

### `create`: staging and Commit

Lifecycle's `create` ([lifecycle.md](lifecycle.md)) holds the Host lock throughout and calls, in order:

1. **`Stage(spec)`**:
   1. `CheckNew(spec)`: `ValidName(name)`; `instances/<name>/` must not exist (valid record or not:
      "Instance kids already exists"); `ValidHostname(hostname)`; no other Instance folder's `HOSTNAME=` line
      may name it ("Hostname kids.example.org is used by Instance bob"). Then the version must be a plain
      `X.Y.Z` (`buildinfo.PlainVersion`), the digest `sha256:<64 hex>`, and `DataOwner` IDs above 0.
   2. Delete a leftover `instances/.<name>.creating/` or `instances/.<name>.removing/` of the same name.
   3. If `backups/<name>/` exists: move it to `backups/removed/<name>-<Stamp(now)>/` exactly as `Remove`'s step 2
      does ([below](#remove-moving-backups-aside-and-deleting-the-folder)); a target that already exists fails
      `Stage` with nothing moved. `Staged.BackupsMovedTo()` returns the new path, and Lifecycle emits it as an
      event, so it reaches the activity log. `Discard` does not move them back.
   4. `EnsureDir(instances/)` and `EnsureDir(instances/.<name>.creating/)`, 0700.
   5. Write `.lock` (empty, 0600) and `instance.env` (0600) with `HOSTNAME`, the version and digest,
      `STATE=running`, `INTERNET=1` when `spec.Internet`, `CREATED_AT` = the `Clock`'s now in UTC to the second,
      and `TOOL_VERSION` = the Store's `toolVersion`.
   6. `EnsureDir(data/)`, 0700, then change its owner to `DataOwner`. `data/` starts empty.
2. **Lifecycle's `Create` locks `Staged.LockPath()`** (`p.CreatingLock(name)`) with `internal/locks`, one of the two
   named exceptions to "modules never lock" ([docker-and-locks.md](docker-and-locks.md)).
3. **`Staged.WriteCompose(render.InstanceCompose(…))`**: the Renderer's file, from the staged record.
4. **`Staged.Commit()`**: check again that `instances/<name>/` does not exist, rename the staging folder to it,
   and fsync `instances/`. The lock file moves with the folder; the caller's `flock` holds on.
5. The caller writes the site file and reloads the Proxy (`AddSite`), then starts the container. On any failure
   from step 1 on, it calls `Staged.Discard()` (after tearing down whatever it started), so nothing of the new
   Instance is left.

A crash leaves at most a `.creating` folder, which `List` ignores and the next `Stage` of that name or `Sweep`
deletes; or a committed Instance whose container never started, which `status` shows as not running and `start`
brings up.

### `remove`: moving Backups aside and deleting the folder

`Remove(name)`:

1. Check `instances/<name>/` exists, else `ErrNoInstance`.
2. If `backups/<name>/` exists: `EnsureDir(backups/removed/)`, 0700; rename `backups/<name>/` to
   `backups/removed/<name>-<Stamp(now)>/`; fsync both parents. The stamp is to the second, so if that target
   already exists `Remove` fails and nothing moves. These Backups include the `final` Backup Lifecycle's `remove`
   has just taken into `backups/<name>/` ([lifecycle.md](lifecycle.md)). The Backups are kept, never pruned automatically
   and not offered to `restore` in v1, so the name can be reused at once.
3. Rename `instances/<name>/` to `instances/.<name>.removing/` and fsync `instances/`. From here the Instance no
   longer exists: `Get` returns `ErrNoInstance` and `List` skips it.
4. Delete `instances/.<name>.removing/` with everything in it.

The order keeps every crash safe. A crash before step 3 leaves the Instance in place with its Backups already
moved; `remove` run again takes a new final Backup and moves that too. A crash in step 4 leaves a `.removing`
folder that nothing reads, deleted by `Sweep` or the next `Stage` of that name. A command waiting for the
Instance lock gets `fs.ErrNotExist` once it is released ([docker-and-locks.md](docker-and-locks.md)).

`Staged.Discard()` after `Commit` runs steps 3 and 4 only.

## 4. Files and formats it owns

```
internal/store/
  store.go     Store, New, Get, List, Put, WriteCompose
  record.go    Record, Parse, Format, the key rules
  names.go     ValidName, ValidHostname
  create.go    CheckNew, Stage, Staged, Remove, Sweep
  outcomes.go  Outcomes, RecordBackup, RecordUpgrade
  *_test.go
```

### Instance names

- 2 to 32 characters of `[a-z0-9-]`, starting with a letter and not ending with `-`.
- Reserved: `all` (`backup --all`), `proxy` (the Proxy's Compose project `abyssctl-proxy` and `logs proxy`) and
  `removed` (its Backups would land in `backups/removed/`).
- Fixed for the Instance's life: the name is the folder name and never a key in the record. It also names the
  Compose project, container and network alias (`abyssctl-<name>`, [docker-and-locks.md](docker-and-locks.md))
  and the site file.

### Hostnames

- Lowercase and valid by RFC 1123: at most 253 characters; at least two labels separated by `.`; each label 1 to
  63 characters of `[a-z0-9-]`, not starting or ending with `-`; no trailing dot.
- Never a wildcard: no `*` anywhere.
- Unique across Instances. Whether a [Certificate](../../GLOSSARY.md) covers it is the Proxy's check
  ([proxy.md](proxy.md)), not Store's. The default, `<name>.<base domain>`, is built by Lifecycle from the
  [Base domain](../../GLOSSARY.md).

### The Instance record (`instance.env`)

```
HOSTNAME=kids.example.org
ACTUAL_VERSION=26.11.0
ACTUAL_IMAGE_DIGEST=sha256:4f0c…(64 hex)
STATE=running
INTERNET=1
CREATED_AT=2026-11-17T08:00:04Z
TOOL_VERSION=0.1.0
UPGRADE_BACKUP=kids-20261117T080004Z-pre-upgrade
UPGRADE_HEALTHY=1
```

- **Lines.** UTF-8, `\n` line ends, one `KEY=value` per line, the final `\n` optional on read. `KEY` is
  `[A-Z][A-Z0-9_]*` and ends at the first `=`; the value is the rest of the line, taken literally: no quotes, no
  escapes, no trimming. A control character anywhere (a `\r` included) is a problem. Blank lines and `#` lines are
  skipped on read with a warning and never written.
- **Known keys, in this order:**

  | Key | Required | Value |
  |---|---|---|
  | `HOSTNAME` | yes | a Hostname by the rules above |
  | `ACTUAL_VERSION` | yes | a plain stable `X.Y.Z` (`buildinfo.PlainVersion`) |
  | `ACTUAL_IMAGE_DIGEST` | yes | `sha256:` and 64 lowercase hex digits: the digest the image is pinned to |
  | `STATE` | yes | `running` or `stopped` |
  | `INTERNET` | no | `1`: the Instance has [Internet access](../../GLOSSARY.md) ([ADR 0005](../adr/0005-internet-access-per-instance.md)); left out when it has none |
  | `CREATED_AT` | yes | RFC 3339 in UTC, whole seconds, with `Z` (`2026-11-17T08:00:04Z`) |
  | `TOOL_VERSION` | yes | valid SemVer without `v` (`buildinfo.ValidVersion`) |
  | `UPGRADE_BACKUP` | no | a `pre-upgrade` [Backup](../../GLOSSARY.md) id of this Instance: `<name>-<stamp>-pre-upgrade`, with `<name>` this Instance's name and `<stamp>` a `hostfs.Stamp` ([backup.md](backup.md) owns the id format) |
  | `UPGRADE_HEALTHY` | no | `1`; allowed only when `UPGRADE_BACKUP` is set |

- **Unknown keys** are kept, in their order, after the known keys, with a warning on every read. An unknown
  (`Extra`) key is valid when it matches `[A-Z][A-Z0-9_]*`, is not a known key and its value holds no control
  character; `Format` refuses any other. They are never
  handed to the container: the record is not an env file for Docker, and the Renderer writes literal values into
  `compose.yaml`.
- **Problems** (each a `*RecordError` line): a line that is not `KEY=value`, a duplicate key, a missing required
  key, a known key's value that breaks its rule, `UPGRADE_HEALTHY` without `UPGRADE_BACKUP`, a Hostname another
  Instance uses, a file over 64 KiB, a missing file.
- The image repo is not in the record: it is `buildinfo.ActualRepo`, and `Record.Image(repo)` builds the full
  reference the Renderer writes.
- root:root 0600. It holds no Secrets.

### `outcomes.json`

```json
{
  "schema": 1,
  "scheduled_backup": { "at": "2026-11-17T08:00:04Z", "ok": false, "error": "not enough free space for the Backup" },
  "upgrade": { "at": "2026-11-16T14:21:37Z", "result": "rolled_back" }
}
```

- `schema` is `1`; any other value is an error on read. Each section is absent until its first result.
- `scheduled_backup`: `at` (RFC 3339 UTC), `ok` (boolean), `error` (omitted when `ok`). `upgrade`: `at`,
  `result` (`ok`, `rolled_back` or `rollback_failed`).
- Unknown fields are ignored on read and not written back. The file is written with `hostfs.WriteFile`, root:root
  0600, and is not part of any Backup.
- What a section means as a Problem, and when that Problem ends, is [health.md](health.md)'s.

### The Instance folder

| Path in `instances/<name>/` | Written by | Notes |
|---|---|---|
| `instance.env` | Store | the record |
| `compose.yaml` | Store (`WriteCompose`), content from the [Renderer](render.md) | generated; a hand edit is overwritten |
| `outcomes.json` | Store, for Backup and Upgrade | above |
| `.lock` | created empty by `Stage`; held and written by `internal/locks` | the Instance lock |
| `data/` | created empty by `Stage`, owned by the `abyssctl` account; then Actual's | Backup archives and restores it |
| `data.pre-restore/` | [Backup](backup.md), during a Restore | |
| `upgrade-failed-<stamp>.log` | [Upgrade](upgrade.md) | |

Nothing else belongs in the folder. Anything else found there is left alone and deleted with the folder on
`remove`. Next to the Instance folders, in `instances/`, sit only the `.<name>.creating/` and `.<name>.removing/`
folders above, root:root 0700.

## 5. Depends on

- [foundation](foundation.md): `hostfs.Paths`, `hostfs.WriteFile`, `hostfs.EnsureDir`, `hostfs.Stamp` and
  `ParseStamp`; `op.Clock` and the typed errors; `buildinfo.ValidVersion`, `buildinfo.PlainVersion`.
- No other module. [docker-and-locks](docker-and-locks.md) is not imported: Store only creates the lock file its
  caller locks.

Import rule this spec adds to `internal/archtest`: `internal/store` imports only the standard library,
`internal/hostfs`, `internal/op` and `internal/buildinfo`.

## 6. Decisions

- The Instance folder is the only record; the disk wins over Docker; `compose.yaml` and the site file are
  generated from the record: [ADR 0002](../adr/0002-instance-folder-is-the-record.md).
- Plain `KEY=value` with a fixed key order, no comments and no quoting, so the file is readable from any language
  and two writes of the same record give the same bytes. Unknown keys are kept, not dropped, so a hand edit or a
  newer key is never lost by an older write.
- The record holds no Secrets and is never handed to a container.
- `create` builds in a hidden `.creating` folder and commits with one rename, so a half-built Instance is never
  listed. `remove` and `Discard` rename the folder to `.removing` before deleting it, so a half-deleted one is
  never listed either.
- The staged Instance's lock file is created by `Stage` and locked by the caller before `Commit`. A `flock` is on
  the open file, not the path, so it survives the rename, and the new Instance is locked from the moment it is
  visible.
- `data/` is a bind-mounted folder in the Instance folder, not a named volume: Backups are plain file work and
  `docker volume prune` can't touch it. `Stage` sets its owner because the container needs it before its first
  start.
- `removed` is reserved with `all` and `proxy`, because an Instance's Backups live in `backups/<name>/`.
- `Stage` moves a `backups/<name>/` it finds aside, as `remove` does, so a reused name never inherits Backups
  `restore` would then offer as its own.
- An invalid record is exit 4, not 2: only a hand edit fixes it, so the tool stops and says which line to fix.
  `TOOL_VERSION=` is read before the rest, so a record written by a newer Abyss points at the newer binary
  instead.
- A Hostname clash makes both records invalid rather than letting one win: the Proxy can route a Hostname to only
  one Instance, and the tool can't know which the Operator meant.
- No new dependency: `encoding/json`, `os` and `strings` from the standard library.

## 7. Later

- `set-hostname` and renaming an Instance.
- Per-Instance Actual settings as an allow-list of keys in the record (with Actual settings and OpenID).
- `doctor`.

## 8. Docs it changes

- `docs/guides/security.md`: the Instance record holds no Secrets; `instance.env` and `outcomes.json` are
  root-only (0600).
- Reference: the `create` page states the Instance name rules (including the reserved `all`, `proxy` and
  `removed`) and the Hostname rules, through the command's help text ([cli.md](cli.md)).
- `docs/guides/teardown.md`: an Instance's folder is `instances/<name>/`, and a removed Instance's Backups are in
  `backups/removed/<name>-<stamp>/` until deleted by hand.

## 9. Acceptance list

All tests are module tests in `internal/store`, on `hostfs.Under(t.TempDir())` with a Data root inside it and an
`op.ManualClock`. Ownership tests skip when not run as root; journey 3 covers them on a Host.

1. `ValidName` accepts 2–32 characters of `[a-z0-9-]` starting with a letter and not ending with `-`, and refuses
   (exit 2) everything else, including `all`, `proxy`, `removed`, a leading `.` and a `/`. →
   `TestValidName` (table)
2. `ValidHostname` accepts a lowercase RFC 1123 name of two or more labels and refuses an uppercase letter, a
   wildcard, a single label, a trailing dot, a label over 63 characters, a name over 253 and a label starting or
   ending with `-`. → `TestValidHostname` (table)
3. `Parse` reads a record with every known key into every `Record` field. → `TestParse_AllKeys`
4. `Parse` reports each broken rule with the right line: not `KEY=value`, a duplicate key, each missing required
   key, each known key's bad value (`INTERNET=0`, `INTERNET=yes` and `INTERNET=` among them), `UPGRADE_HEALTHY`
   without `UPGRADE_BACKUP`, an `UPGRADE_BACKUP` for another
   Instance or another kind, a control character, a file over 64 KiB; and reports every problem, not just the
   first. → `TestParse_Problems` (table)
5. An unknown key and a comment line each give a warning, the unknown key is kept in `Extra` in file order, and
   the comment is dropped. → `TestParse_UnknownKeysAndComments`
6. `Format` writes the known keys in the fixed order, leaves out unset optional keys (`INTERNET` when `Internet`
   is false), puts `Extra` keys after them, writes no comments, and refuses an invalid record. →
   `TestFormat_OrderAndExtras`, `TestFormat_RefusesInvalid`
7. `Format(Parse(b))` gives back `b` for a tool-written record, and `Parse(Format(r))` gives back `r`, with and
   without `INTERNET=1`. → `TestRecord_RoundTrip` (table)
8. `Get` returns the record of a valid Instance, `ErrNoInstance` (matching `fs.ErrNotExist`) for a missing folder,
   and a `*RecordError` (matching `ErrInvalidRecord` and `op.ErrNeedsOperator`, exit 4, hint `fix <file> by hand`)
   for a missing or invalid `instance.env`, with the file, line and problem in its message; the error's
   `ToolVersion` holds a valid `TOOL_VERSION=` value even when other lines fail to parse, and is "" when that line
   is missing or invalid. → `TestGet`, `TestGet_ToolVersionOfInvalid` (table)
9. Two Instances whose records name the same Hostname are both invalid, in `Get` and in `List`. →
   `TestGet_DuplicateHostname`
10. `List` returns every Instance folder sorted by name, valid ones with a record and invalid ones with an error,
    lists a folder with an invalid name as invalid, and skips `.creating` and `.removing` folders and plain
    files. → `TestList`
11. `Put` writes the record with mode 0600 only when it changed, keeps `Extra` keys, refuses an invalid record or
    a Hostname another Instance uses, and refuses `ErrNoInstance` for a missing folder without creating one. →
    `TestPut`
12. `WriteCompose` writes `compose.yaml` with mode 0600 only when the content changed. → `TestWriteCompose`
13. `CheckNew` and `Stage` refuse, with the same error and nothing written, an invalid name, an existing Instance
    folder (valid record or not), an invalid Hostname, a Hostname another Instance uses, a version that is not
    plain `X.Y.Z`, a malformed digest and a `DataOwner` ID of 0; an empty digest is refused by `Stage` and skipped
    by `CheckNew`. → `TestCheckNew_MatchesStage` (table)
14. `Stage` builds `.<name>.creating/` (0700) with an empty `.lock` (0600), `instance.env` (0600) holding the
    Hostname, version, digest, `STATE=running`, `INTERNET=1` only when `Internet` is set, the clock's `CREATED_AT`
    in UTC and the Store's `TOOL_VERSION`, and an empty `data/` (0700); `Get` and `List` do not see it. →
    `TestStage_Builds` (table: with and without Internet access)
15. `Stage` sets `data/`'s owner to `DataOwner`. → `TestStage_DataOwner` (root only); journey 3 asserts the
    container user owns `data/` (checked by lifecycle.md's journey slice)
16. `Stage` deletes a leftover `.creating` or `.removing` folder of the same name first; with an existing
    `backups/<name>/` it moves that to `backups/removed/<name>-<stamp>/` with the clock's stamp and returns that
    path from `BackupsMovedTo` (and "" when there was none); `OldBackups` reports the folder beforehand; a `Discard`
    leaves them moved; an existing target fails `Stage` with nothing moved. → `TestStage_ReplacesLeftover`,
    `TestStage_MovesOldBackups`
17. A `syscall.Flock` taken on `LockPath()` before `Commit` is still held on `instances/<name>/.lock` after it: a
    second open of that path gets `EWOULDBLOCK` from a non-blocking `syscall.Flock` until the first is released
    (the Locker's side is `TestLocker_CreatingLock` in [docker-and-locks.md](docker-and-locks.md)). →
    `TestCommit_LockSurvivesRename`
18. `Commit` makes the Instance visible to `Get` and `List` with the staged files, and fails, leaving both folders
    as they were, if `instances/<name>/` appeared meanwhile. → `TestCommit`, `TestCommit_TargetExists`
19. `Discard` before `Commit` deletes the staging folder; after `Commit` it deletes the Instance folder and leaves
    `backups/` alone; a second call is a no-op. → `TestDiscard`
20. `Remove` moves `backups/<name>/` to `backups/removed/<name>-<stamp>/` with the clock's stamp, deletes the
    Instance folder with `data/` and everything else in it, reports where the Backups went, and works for an
    Instance with no Backups and for one with an invalid record. → `TestRemove`
21. After `Remove`, `Stage` of the same name succeeds at once and finds no Backups in `backups/<name>/`. →
    `TestRemove_NameReusable`
22. A `Remove` stopped after its Backups move or after its rename leaves a state where `Get` and `List` see the
    Instance whole or not at all, and `Sweep` cleans up. → `TestRemove_CrashPoints` (table, with a failure hook)
23. `Sweep` deletes every `.creating` and `.removing` folder in `instances/`, returns their paths, and touches
    nothing else. → `TestSweep`
24. `RecordBackup` and `RecordUpgrade` each write their own section with the clock's time in UTC and keep the
    other; `error` is omitted when `ok`, flattened to one line and cut to 500 bytes on a UTF-8 boundary; the file
    is 0600. → `TestOutcomes_Record`
25. `Outcomes` returns empty for a missing file, `ErrNoInstance` for a missing Instance, an error for a bad file or
    a `schema` other than 1, and ignores unknown fields; a `Record…` call replaces a bad file. →
    `TestOutcomes_Read`
26. Every method that takes a name refuses an invalid one before touching the disk. → `TestStore_NamesCheckedFirst`
27. `internal/store` imports only the standard library, `hostfs`, `op` and `buildinfo`. → `TestImportRules`
28. A real `create` gives `instances/<name>/` with `instance.env`, `compose.yaml`, `.lock` and `data/` at the
    Host layout's owners and modes, and a real `remove` leaves no Instance folder and the Backups under
    `backups/removed/`. → journey 3 (Instance life) (checked by lifecycle.md's journey slice)
29. Docs updated: `docs/guides/security.md`, `docs/guides/teardown.md`, the `create` reference page's name and
    Hostname rules. → (review)
