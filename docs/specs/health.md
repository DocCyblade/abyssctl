# Spec: health

Build step 12 of 16. The [overview](README.md) has the module map, the call graph and the Host layout. Shared
packages (`internal/hostfs`, `internal/op`, `internal/buildinfo`) are in [foundation.md](foundation.md); the Docker
adapter, Docker names and locks are in [docker-and-locks.md](docker-and-locks.md); the Instance record and
`outcomes.json` are in [store.md](store.md); `Status`, `Certificates` and `Probe` are in [proxy.md](proxy.md);
`Latest` is in [versions.md](versions.md); the `Alert` value, `Sender` and Pushover are in [alerts.md](alerts.md);
the units and the MOTD script are in [render.md](render.md); `abyssctl.env` and `ReadSettings` are in
[host.md](host.md); `status` is in [lifecycle.md](lifecycle.md). Capitalised terms are defined in
[GLOSSARY.md](../../GLOSSARY.md).

## 1. Purpose

Build the Health module (`internal/health`) and the verbs `health check` and `health clear`. Health owns:

- **the [Health check](../../GLOSSARY.md)**: one run looks at Docker, the [Proxy](../../GLOSSARY.md), every
  [Instance](../../GLOSSARY.md), the Certificates in use and the [Data root](../../GLOSSARY.md)'s free space, and
  turns what it finds into [Problems](../../GLOSSARY.md);
- **the Problem kinds**: which are [Live Problems](../../GLOSSARY.md) and which
  [Event Problems](../../GLOSSARY.md), when each starts and ends, the debounce, and each kind's wording and hint;
- **sending [Alerts](../../GLOSSARY.md)**: Health is the only module that sends one. It builds every Alert, sends
  it through the `Sender` from [alerts.md](alerts.md), records what was delivered and tries again next run;
- **`health.json`**: the results `status` reads, the latest Actual and Caddy versions, the debounce counts and the
  delivery state;
- **`motd.txt`**: the pre-rendered [MOTD](../../GLOSSARY.md), rewritten on every run;
- **`health clear <name>`**, which ends an Instance's Event Problems.

When this spec is done, the timer runs `abyssctl health check --scheduled` every `HEALTH_INTERVAL`; a stopped
container, a Proxy that does not answer, a Certificate near expiry, low disk space or a failed scheduled Backup
shows on the MOTD and in `status`, and reaches the Operator's phone through Pushover once, with one recovery
message when it ends. The Health part of journey 7 passes.

## 2. Interface

Signature sketches, not final code.

```go
package health

// The narrow seams Health uses. *store.Store, *proxy.Proxy and *versions.Versions implement them.
type Store interface {
    List() ([]store.Entry, error)
    Get(name string) (store.Record, error)
    Outcomes(name string) (store.Outcomes, error)
}

type Proxy interface {
    Status(ctx context.Context) (proxy.Status, error)
    Certificates(hostnames []string) ([]proxy.Certificate, []string, error)
    Probe(ctx context.Context, hostname, path string) (proxy.Response, error)
}

type Versions interface {
    Latest(ctx context.Context, repo string) (buildinfo.ImagePin, error)
}

// Settings are the abyssctl.env values Health uses (host.Settings).
type Settings struct {
    DiskMinFree  int    // DISK_MIN_FREE, percent, 1–90
    CertWarnDays int    // CERT_WARN_DAYS, 1–90
    BaseDomain   string // BASE_DOMAIN: names the Host in every Alert
}

// Disk is the Data root's file system: what statfs gives an unprivileged writer.
type Disk struct {
    Total, Avail uint64 // bytes
}

type Deps struct {
    Paths      hostfs.Paths // with the Data root (Paths.WithDataRoot)
    Store      Store
    Docker     docker.Client
    Proxy      Proxy
    Versions   Versions
    Locker     *locks.Locker // Check calls only TryAcquire on Instance locks with it
    Sender     alerts.Sender // nil: no channel; Problems still reach health.json and the MOTD
    SenderNote string        // "" or why there is no channel although Pushover is half set (a lone key)
    Settings   Settings
    DiskFree   func(path string) (Disk, error) // nil: statfs(2) on Paths.DataRoot()
    Emit       op.Emitter
    Clock      op.Clock
    Zone       *time.Location // Host local time for the MOTD and Alerts; the CLI passes time.Local
    Version    string         // buildinfo.Version
    CaddyPin   string         // buildinfo.CompiledPins().Caddy.Version, for the "pinned by Abyss" line
    ActualRepo string         // buildinfo.ActualRepo
    CaddyRepo  string         // buildinfo.CaddyRepo
}

func New(d Deps) *Health

// Check runs one Health check: looks, updates the Problems, sends Alerts, and writes health.json and motd.txt.
// Precondition: the caller holds the health lock. It returns an error only when the check could not run or its
// results could not be written; a Problem, a failed Send or a failed version lookup is in the Result.
func (h *Health) Check(ctx context.Context) (Result, error)

type Result struct {
    CheckedAt time.Time // UTC
    Instances int       // Instance folders, valid or not
    Busy      []string  // Instances skipped because a command held their lock
    Problems  []Problem // current, in display order
    Started   []Problem // started in this run
    Ended     []Problem // ended in this run
    Sent      int       // Alerts delivered in this run
    Unsent    int       // Alerts still waiting after this run
    SendError string    // the last Send error, "" when none failed in this run
    Warnings  []string  // things the Operator should see that are not Problems; empty when none
}

// Clear ends name's current Event Problems, rewrites health.json and motd.txt, and sends nothing. It returns the
// kinds it ended, empty when there were none. Precondition: the caller holds the health lock.
func (h *Health) Clear(ctx context.Context, name string) ([]Kind, error)

// Read returns what the last Check or Clear wrote. No lock. ErrNoResults when health.json does not exist.
func (h *Health) Read() (View, error)

var ErrNoResults = errors.New("no Health check results yet")

type View struct {
    CheckedAt         time.Time // UTC
    Problems          []Problem // current, Live and Event, in display order
    LatestActual      string    // "" when never fetched
    LatestCaddy       string    // "" when never fetched
    VersionsFetchedAt time.Time // UTC; zero when never fetched
}

type Kind string // the kinds are in Behaviour

type Problem struct {
    Kind    Kind
    Subject string    // identity: an Instance name, a Certificate id, an Orphan's name, or "host"
    Label   string    // what people see: the Instance or Orphan name, "proxy", "disk", "docker" or "host"
    Text    string    // one line, no subject, no time, no Secret, no file path
    Hint    string    // a full command line, or ""
    Event   bool      // an Event Problem
    Since   time.Time // UTC
}
```

### Invariants and error modes

- **Identity is kind + subject.** Two Problems with the same kind and subject are one Problem. A Problem's
  `Text` may change from run to run (a Certificate goes from expiring to expired) without it being a new Problem.
- **Every run rewrites both files.** Every `Check` writes `health.json` and then `motd.txt`, even when nothing
  changed and even when Docker is down, so the MOTD script's age check ([render.md](render.md)) means "the timer
  stopped". Both go through `hostfs.WriteFile` at root:root 0600.
- **Check fails only for its own reasons.** A Problem, a failed probe, a failed `Send` or a failed version lookup
  never makes `Check` return an error. It returns one for a cancelled context or a failed write of `health.json`
  or `motd.txt`.
- **Health never changes an Instance.** It reads the record, `outcomes.json`, the Instance folder, Docker and the
  Proxy, and writes only the two files in `state/`. It makes no Docker call that changes anything (no Compose
  call, no start, stop, remove or pull) and never alerts about anything outside its kinds.
- **The caller holds the health lock** ([docker-and-locks.md](docker-and-locks.md)), around both `Check` and
  `Clear`. Health is one of the two named exceptions to "modules never lock": inside `Check` it calls `TryAcquire`
  on each Instance lock in turn and holds at most one at a time. It never `Acquire`s a lock, never takes the Host
  lock and never takes the health lock itself.
- **Every call outside the process has a deadline.** Each Docker call and each `Proxy.Status` call runs under its
  own 30-second context; probes and version lookups have theirs below. A Docker call that runs out of time is
  Docker not answering.
- **No Secret and no file path leaves in an Alert or the MOTD.** Every `Title`, `Text` and MOTD line is built
  from Instance names, Hostnames, Certificate ids and names, versions, times in `Zone`, percentages and the fixed
  sentences below. The only text taken from elsewhere is a failed scheduled Backup's error (from `outcomes.json`)
  and an invalid record's problem text (from Store): each has every occurrence of the Data root's path replaced by
  "the Data root" and is cut to 200 characters. Any other error (Docker's, a file system's, a probe's) goes only
  into an event, masked the same way. The one hint that names a file (`upgrade_rolled_back`) is left out of its
  Alert. The Pushover values are never in `Deps`.
- **Health is the only Alert sender.** `Send` is called only from `Check`, at most once per Alert per run.
- **`Read` is pure.** It takes no lock and writes nothing; a `health.json` that does not parse, or whose `schema`
  is not `1`, is an error naming the file.

## 3. Behaviour

### The Problem kinds

Each kind is a Live Problem (it lasts while the condition holds) or an Event Problem (it comes from a result in
`outcomes.json` and lasts until the same operation succeeds or the Operator runs `health clear <name>`).
"Twice" means the condition was seen on two runs in a row that looked at it; such a Problem's `Since` is the first
of those runs. "At once" means the first run that sees it starts it.

| Kind | Subject / Label | Live or Event | Starts | Ends | Hint |
|---|---|---|---|---|---|
| `docker_down` | `host` / `docker` | Live | twice | first run in which every Docker call answers | `systemctl status docker` |
| `instances_unreadable` | `host` / `host` | Live | at once | first run `Store.List` works | `abyssctl host setup` |
| `proxy_down` | `host` / `proxy` | Live | twice | first run the Proxy runs and answers | `abyssctl logs proxy`, or `abyssctl host setup` when it is not set up or its settings are invalid |
| `instance_down` | name / name | Live | twice | first run it runs, or `STATE=stopped` | `abyssctl start <name>` |
| `instance_unhealthy` | name / name | Live | twice | first good probe, or `STATE=stopped` | `abyssctl logs <name>` |
| `interrupted_upgrade` | name / name | Live | at once | `UPGRADE_BACKUP` cleared, or `rollback_failed` starts | `abyssctl upgrade <name>` |
| `interrupted_restore` | name / name | Live | at once | `data.pre-restore/` gone | `abyssctl restore <name>` |
| `invalid_record` | name / name | Live | at once | the record is valid | `abyssctl status` |
| `outcomes_unreadable` | name / name | Live | at once | `outcomes.json` reads | none |
| `orphan` | label value / label value | Live | at once | no labelled container left; no Ended Alert | `abyssctl remove --orphan <name>` |
| `certificate_expiry` | Certificate id / `proxy` | Live | at once, within `CERT_WARN_DAYS` | replaced, removed, no longer in use, or outside the window | see below |
| `low_disk` | `host` / `disk` | Live | at once | first run at or above `DISK_MIN_FREE` | `abyssctl backup list` |
| `backup_failed` | name / name | Event | a newer failed scheduled Backup | a newer successful one, or `health clear` | `abyssctl health clear <name>` |
| `upgrade_rolled_back` | name / name | Event | a newer `rolled_back` Upgrade | a newer `ok` Upgrade, or `health clear` | `less <file>`, see below |
| `rollback_failed` | name / name | Event | a newer `rollback_failed` Upgrade | a newer `ok` or `rolled_back` Upgrade, or `health clear` | `abyssctl upgrade <name>` |

The display order is this table's order, then subject. `View.Problems` and the CLI's output use it, and the MOTD
lists its `!` lines in it.

- `outcomes_unreadable` has no hint: no command fixes the file, and the next scheduled Backup or Upgrade
  rewrites it.
- `backup_failed`'s fix is the cause the text names; once it is fixed, `health clear <name>` ends the Problem, and
  so does the next scheduled Backup that succeeds. A Backup run by hand does not end it, because only the
  scheduled Backup writes `outcomes.json` ([backup.md](backup.md)).
- `upgrade_rolled_back`'s hint is `less <file>` with the newest `upgrade-failed-*.log` in the Instance folder
  ([upgrade.md](upgrade.md)), or none when there is no such file.
- An Orphan ends without an Ended Alert: `remove --orphan` is a deliberate act, and the Operator already knows.

Each kind's `Text`, with its variants, and the phrase its Ended Alert uses:

| Kind | `Text` | Ended as |
|---|---|---|
| `docker_down` | `Docker is not reachable` | `Docker reachable` |
| `instances_unreadable` | `the Instance folders could not be read` | `Instance folders readable` |
| `proxy_down` | `the Proxy is not set up` · `the Proxy's settings are invalid` · `the Proxy container is not running` · `nothing answers on port 443` | `Proxy up` |
| `instance_down` | `down: no container` · `down: container <state>` (`exited (code 1)`, `restarting`, `paused`, `created`, `removing`, `dead`) | `down` |
| `instance_unhealthy` | `not healthy: Docker reports its healthcheck failing` · `not healthy: /health through the Proxy answered <status>` · `not healthy: /health through the Proxy did not report "UP"` · `not healthy: the Proxy has no Certificate for <hostname>` · `not healthy: the Proxy served a Certificate that does not cover <hostname>` · `not healthy: /health through the Proxy did not answer in time` · `not healthy: /health through the Proxy failed` | `not healthy` |
| `interrupted_upgrade` | `an Upgrade was interrupted; the Instance needs it finished` | `interrupted Upgrade` |
| `interrupted_restore` | `a Restore was interrupted; the Instance needs it finished` | `interrupted Restore` |
| `invalid_record` | `the Instance record is invalid: line <n>: <text>`, or without `line <n>: ` for a problem on no line (line 0); `(and <m> more)` added for more problems | `invalid record` |
| `outcomes_unreadable` | `the Backup and Upgrade results could not be read` | `results readable` |
| `orphan` | `container <container> has no Instance folder` (several: `containers <a>, <b> have …`) | none |
| `certificate_expiry` | `Certificate <id> (<names>) expires on <date> (in <n> days), used by <instances>` · `Certificate <id> (<names>) expired on <date>, used by <instances>` | `Certificate <id> <reason>` |
| `low_disk` | `<n>% free on the Data root (minimum <m>%)` · `the Data root's free space could not be read` | `low disk space` |
| `backup_failed` | `the scheduled Backup failed: <outcome error>` | `failed scheduled Backup` |
| `upgrade_rolled_back` | `an Upgrade failed and was rolled back; still on <ACTUAL_VERSION>` | `rolled-back Upgrade` |
| `rollback_failed` | `an Upgrade failed and its Rollback failed too` | `failed Rollback` |

Dates are `2006-01-02` in `Zone`. Percentages are whole numbers, rounded down.

### One Check

The CLI has read the settings, built the `Sender`, wired `Deps` and taken the health lock
([What the CLI does](#what-the-cli-does)). `Check` then:

1. **State.** `now := Clock.Now()`. Read `health.json`. A missing file starts from empty state. A file that does
   not parse, or has another `schema`, starts from empty state too, with the event `warning state=unreadable` and
   a line in `Result.Warnings`: the check still runs, so the MOTD never goes stale over a broken file.
2. **Docker.** `Docker.ListContainers(names.InstanceLabel)`. A failure (a timeout included) is a `docker_down`
   sighting, and Docker is down for the rest of the run. While Docker is down, steps 3, 5.6, 5.7 and 6 are not
   made, and the Problems and counts they decide (`proxy_down`, `instance_down`, `instance_unhealthy`, `orphan`)
   stay as they are; everything else runs. `docker_down` ends when every Docker call of the run answered.
3. **Proxy.** `Proxy.Status`. An error matching `op.ErrNeedsOperator` (an invalid `proxy.env`), `SetUp` false or `Running` false is a
   `proxy_down` sighting with the matching text. Any other error, a timeout included, is a `docker_down` sighting
   (the Proxy's state comes from Docker) and Docker is down from here on. Otherwise the Proxy is "up so far";
   step 5.7 can still turn it down.
4. **Instances.** `Store.List`. A failure is an `instances_unreadable` sighting, and steps 5, 6 and 7 are not made,
   with every Instance, Orphan and Certificate Problem kept as it is. An entry with `Err` is an `invalid_record`
   sighting and gets no lock, no probe and no Event Problems.
5. **Each valid Instance, in name order:**
   1. **Outcomes** (no lock, [Event Problems](#event-problems)). A read error is an `outcomes_unreadable` sighting,
      and that Instance's Event Problems stay as they are.
   2. **Lock.** `TryAcquire(Paths.InstanceLock(name))`.
      - Busy: the name goes in `Busy`, the event `skipped instance=<name> reason=busy holder=<pid>
        purpose=<purpose>` is emitted, and the Instance's Live Problems and counts stay as they are: a held lock
        means a command is working on it (a Backup holds it while the Instance is stopped).
      - An error wrapping `fs.ErrNotExist`: the Instance was removed after step 4. It is gone for this run, as in
        step 9.
      - Any other error: the event `skipped instance=<name> reason=lock error=<error>`, and the Instance is treated
        as busy (in `Busy`).
      - Otherwise the rest runs under the lock.
   3. **Record.** `Store.Get(name)` again, so the record is the current one. `ErrNoInstance` means the Instance
      is gone for this run, as in step 9. An error matching `store.ErrInvalidRecord` (`errors.Is`; `errors.As` gives
      the `*store.RecordError` and its problems) is an `invalid_record` sighting. Any other error is the
      event `skipped instance=<name> reason=record error=<error>` and the Instance is treated as busy.
   4. **Interrupted.** `UPGRADE_BACKUP` set is an `interrupted_upgrade` sighting, unless a `rollback_failed`
      Problem is current for the Instance, including one started in step 5.1 of this run (it names the same fix).
      Otherwise a `data.pre-restore/` folder (`Paths.InstancePreRestore(name)`) is an `interrupted_restore`
      sighting. An interrupted Instance gets no down or unhealthy judgement; its counts for those are reset.
   5. **Wanted state.** `STATE=stopped` ends `instance_down` and `instance_unhealthy` and resets their counts: the
      Operator stopped it.
   6. **Running.** With `STATE=running` and Docker up: `Docker.Container(ctx, names.InstanceProject(name))`, asked
      now, under the lock, not taken from step 2's list (a container a Backup stopped and started between the two
      would otherwise count as down). No container, or a `State` other than `running`, is an `instance_down`
      sighting. Running ends `instance_down`. An error is a `docker_down` sighting, and Docker is down from here
      on.
   7. **Probe.** A running container whose Docker `Health` is `unhealthy` is an `instance_unhealthy` sighting.
      Otherwise, while the Proxy is still up this run, `Proxy.Probe(ctx, rec.Hostname, "/health")`:
      `ErrUnreachable` is a `proxy_down` sighting ("nothing answers on port 443"), the Proxy is down for the rest
      of the run, and this Instance's unhealthy count is left as it is; a 200 whose body contains `"status":"UP"`
      is good and ends `instance_unhealthy`; anything else (another status, an empty 200, `ErrHandshake`,
      `ErrWrongCertificate`, a timeout, another error) is an `instance_unhealthy` sighting with its text. While the
      Proxy is down, no probe is made and unhealthy counts stay as they are, so a Proxy outage raises one Problem,
      not one per Instance.
   8. **Release** the Instance lock.
6. **Orphans.** With Docker up: each `io.abyssctl.instance` value in step 2's list with no Instance folder (an
   invalid record's folder counts as a folder) is an `orphan` sighting listing its containers.
7. **Certificates.** `Proxy.Certificates(<every valid Instance's Hostname>)`, whether or not Docker is up: it reads
   only files. A warning is an event; an error is an event and keeps the `certificate_expiry` Problems as they
   are. See [Certificates](#certificates).
8. **Disk.** `DiskFree(Paths.DataRoot())`; free is `Avail × 100 / Total`, rounded down. Below `DISK_MIN_FREE` is a
   `low_disk` sighting; at or above ends it. An error is a `low_disk` sighting with the second text: a Data root
   that cannot be measured is worth a look.
9. **Reconcile.** Apply every sighting and every end to the Problems: a kind marked "twice" starts when its count
   reaches 2; "at once" starts at 1; any Live Problem whose look ran this run and found nothing ends; a Live
   Problem whose look did not run (busy, Docker down, Proxy down for unhealthy, Store unreadable) stays. The
   Problems of an Instance or Orphan that no longer exists, or that steps 5.2 and 5.3 found gone, are dropped
   without an Ended Alert, with their counts, outcome marks and queued Alerts: it was removed on purpose.
   `interrupted_upgrade` ended because `rollback_failed` started is dropped the same way.
10. **Versions**, when due ([Versions](#versions)).
11. **Alerts** ([Sending Alerts](#sending-alerts)).
12. **Write** `health.json`, then `motd.txt` ([Files and formats](#4-files-and-formats-it-owns)).

Events, one per change, every value masked as in the invariants: `look kind=docker_down subject=host
error=<error>` for each failed look whose error is not in the Problem's text, `problem kind=instance_down
subject=kids result=started`, `… result=ended`, `… result=dropped`, `alert kind=instance_down subject=kids
phase=started result=sent`, `… result=failed error=…`, `… result=invalid error=…`, `versions actual=26.11.0
caddy=2.10.2`, `versions result=failed error=…`, and finally `checked instances=3 problems=1 busy=0 sent=1
unsent=0`. No event holds a Secret.

### Event Problems

From `Store.Outcomes(name)`. For each section, Health remembers in `health.json` the `at` of the outcome it last
acted on and the clear mark of the last `health clear`:

- `scheduled_backup` with `ok` false and an `at` newer than the clear mark starts `backup_failed`, `Since` the
  outcome's `at`. A later failure keeps it and updates its text. A newer `ok` ends it. Every scheduled failure
  counts, including a guard refusal and a free-space refusal ([backup.md](backup.md)), so an Instance the timer
  can never back up shows.
- `upgrade` with `rolled_back` starts `upgrade_rolled_back`; `rollback_failed` starts `rollback_failed`. A newer
  `ok` ends either. A newer `rolled_back` ends `rollback_failed` and starts `upgrade_rolled_back`, which is what
  finishing a failed Rollback with `upgrade <name>` records ([upgrade.md](upgrade.md)): the Instance is back on its
  old version and the Upgrade still did not happen.
- When `rollback_failed` starts while `interrupted_upgrade` is current for the Instance, `interrupted_upgrade`
  ends silently: no Ended Alert, and its undelivered Started Alert is dropped. `rollback_failed` names the same fix.
- An outcome no newer than the clear mark of its section starts nothing.

Event Problems start at once and are not debounced. A busy Instance lock does not stop them: `outcomes.json` is
read without one.

### `Clear`

`Clear(ctx, name)` reads `health.json` and refuses a name that is neither a current Problem's subject nor an
Instance folder: it returns Store's `ErrNoInstance` from `Store.Get` as it is, which [cli.md](cli.md) shows as
"no such Instance: <name>" (exit 2, hint `abyssctl list`). It ends every current Event Problem of `name` and sets each section's clear mark to the outcome `at` that
`health.json` says it last acted on (it does not read `outcomes.json` again), so only a newer outcome can start
the Problem again. It writes `health.json` and `motd.txt`, keeping the delivery state (`channel`, `sender_note`,
the failure) as the last Check wrote it. Live Problems are untouched: their condition decides. No Alert is sent
and no Ended Alert is queued: the Operator ended it. A name with no Event Problem is not an error; it returns no
kinds and writes nothing.

Clearing `rollback_failed` while `UPGRADE_BACKUP` is still set lets the next Check raise `interrupted_upgrade`,
which names the same fix.

### Certificates

Only Certificates that are not `Unused` count: one that covers no Instance and no site file names raises nothing,
so a spare or retired Certificate does not page anyone. For each in-use Certificate:

- `NotAfter` within `CERT_WARN_DAYS` of `now` starts `certificate_expiry` (the "expires on … (in n days)" text).
- `NotAfter` at or before `now` changes the text to "expired on …". The first run that sees it expired sends one
  more Started Alert for the same Problem, so the Operator hears both "expires soon" and "has expired"; this is
  the only re-alert of a running Problem. A Certificate first seen already expired starts the Problem with the
  re-alert counted as sent (`expired_sent`), so it gets one Started Alert. So does one whose "expires soon" Alert
  was still undelivered when it expired: the one Alert it gets says "expired".
- Each run stores the Certificate's `UsedBy` with its Problem in `health.json`.
- The Problem ends when the Certificate is no longer listed, when it becomes `Unused`, or when its `NotAfter` is
  beyond the window (a lowered `CERT_WARN_DAYS`). The Ended Alert's `<reason>`: `replaced` when it is no longer
  listed and every Hostname in its stored `UsedBy` is now in the `UsedBy` of another in-use Certificate (a
  renewal's `cert install`); `removed` when it is no longer listed otherwise; `no longer in use` when it is
  `Unused`; `outside the warning window` for the last.

The hint is `abyssctl cert remove <id>` when another installed Certificate also covers every Hostname in its
`UsedBy`, because Proxy keeps choosing an exact-name Certificate over a wildcard even after it has expired
([proxy.md](proxy.md)). Otherwise it is `abyssctl cert install <cert.pem> <key.pem>`.

### Versions

`health.json` holds the latest stable Actual and Caddy versions. They are fetched when they are due: never fetched,
or fetched 24 hours or more before `now`, and no attempt in the last hour. Then `Versions.Latest(ActualRepo)` and
`Latest(CaddyRepo)`, each bounded by a 30-second context. When both succeed, both values and `fetched_at` are
updated. When either fails, the old values and `fetched_at` stay, the error and `tried_at` are recorded, and the
event `versions result=failed` is emitted. A failed lookup is never a Problem and never an Alert. `create`,
`upgrade` and `proxy upgrade` never read these values; they look up afresh ([versions.md](versions.md)).

### Sending Alerts

Health builds an `alerts.Alert` for each change and keeps a delivery flag per Problem in `health.json`:

- A Problem that starts is due a **Started** Alert. With a `Sender`, every current Problem whose Started Alert has
  not been delivered is due, so Problems that began before Pushover was set up are sent the first time it is.
- A Problem that ends, whose Started Alert was delivered, is due an **Ended** Alert. It is queued in `health.json`
  until delivered, at most 50 (the oldest are dropped). One whose Started Alert was never delivered is dropped:
  the Operator never heard it start.
- `health clear`, a removed Instance, an Orphan's end and `interrupted_upgrade` giving way to `rollback_failed`
  queue nothing.

With a `Sender`, the run sends the queued Ended Alerts oldest first, then the due Started Alerts in display order,
each once. At the first failed `Send` it stops for the rest of the run and keeps everything left unsent; the
failure time starts (or continues) "failing since" and its error becomes the last error. This holds for
`ErrRejected` too: a rejection of one particular message keeps that message first in line and holds back the ones
behind it, and the MOTD shows Pushover's reason until the cause is fixed. `ErrInvalid` is a bug in Health, not a
channel failure: that Alert is dropped (counted as delivered), the event `alert … result=invalid` is emitted, and
sending goes on. A run whose sends all succeed clears the failure. No `Sender` sends nothing and keeps the flags
as they are. A failed `Send`, `ErrLimited` included, never fails the run.

Alerts are sent before `health.json` is written, so a run that dies between the two sends them again next run:
delivery is at least once.

The Alert:

- `Kind` the Problem kind, `Subject` its identity, `Since` its `Since`, `At` this run's `now`.
- **Started**: `Title` `<label>: <Text>`; `Text` three lines: the `Text` with "since <HH:MM zone>" (or
  "since <date> <HH:MM zone>" when not today), `Run: <hint>` (left out without a hint, and for
  `upgrade_rolled_back`, whose hint names a file), and `Host: <BASE_DOMAIN>`.
- **Ended**: `Title` `<label>: resolved: <Ended as>` (for example `kids: resolved: down`, `proxy: resolved:
  Certificate 3f9a1c0b7d2e replaced`); `Text` "<Text>, from <since> to <at>" in the same time form, and `Host:
  <BASE_DOMAIN>`.

Times are `Zone` with its abbreviation (`14:05 EST`). A Title over Pushover's limit is cut by the Adapter
([alerts.md](alerts.md)). The Adapter sets the priority: a normal notification for Started, a quiet one for Ended.

### The MOTD

`motd.txt` is a function of `health.json`, `Zone`, and this binary's version and Caddy pin, so `Check` and
`Clear` render the same bytes from the same state. The MOTD script prints it while it is fresh
([render.md](render.md)).

```
Abyss — 3 Instances, checked 14:10 EST
  ! kids         down: container exited (code 1) since 14:05 EST
  ! disk         8% free on the Data root (minimum 10%) since 09:12 EST
  ! Alerts not delivered since 14:10 EST: Pushover is not reachable: dial tcp: i/o timeout
  ↑ ann          26.10.0 → 26.11.0 available
  ↑ ann          files out of date (Abyss 0.2.0 → 0.3.0): abyssctl restart ann
  ↑ proxy        caddy 2.10.0 → 2.10.2 available
Run: abyssctl status
```

- **Header.** `Abyss — <n> Instances, checked <HH:MM zone>` (`1 Instance`, `no Instances`). With no `!` line it
  is `Abyss — <n> Instances, all healthy (checked <HH:MM zone>)`. The time is the check's, not "n minutes ago":
  the file is written once and printed for up to three intervals.
- **`!` lines**, one per current Problem in display order: the label padded to 12, the `Text`, and "since …"
  (the date is added when `Since` is not on the check's day).
- **`! Alerts not delivered since <time>: <last error>`** while `delivery.channel` is `pushover` and any Alert is
  unsent; the date is added when the failure began on another day. **`! Alerts not sent: <sender note>`** while
  `delivery.sender_note` is set (Pushover half set).
- **`↑` lines**, after the `!` lines:
  - each valid Instance whose `ACTUAL_VERSION` is older than `LatestActual`: `<old> → <latest> available`;
  - each valid Instance whose `TOOL_VERSION` is older than this binary: `files out of date (Abyss <old> →
    <new>): abyssctl restart <name>`. The MOTD compares only `TOOL_VERSION`; `status` also compares
    `compose.yaml` with what Abyss renders ([lifecycle.md](lifecycle.md));
  - the Proxy, when `Status.Behind`: `caddy <current> → <CaddyPin> pinned by Abyss <version>: abyssctl proxy
    upgrade`; otherwise, when `LatestCaddy` is newer than the Proxy's version: `caddy <current> → <latest>
    available`;
  - `versions` when `VersionsFetchedAt` is more than 7 days old: `version info stale (last fetched <date>)`, the
    same rule as `status`.
- **At most 10 lines** between the header and the footer; past that, the last is `  … and <n> more`.
- **Footer** `Run: abyssctl status` whenever any line follows the header. The clean MOTD is the header alone.

Versions are compared with `buildinfo.CompareVersions`; an Instance's `↑` lines use the record as the Check read
it (a busy Instance keeps last run's). The Proxy's version and `Behind` are last run's while Docker is down.

### What the CLI does

| Verb | Locks | Prompt | `--dry-run` | `--json` | Activity log | Exit |
|---|---|---|---|---|---|---|
| `health check [--scheduled] [--json]` | the health lock (`TryAcquire` with `--scheduled`, else 30 s); Instance locks by `TryAcquire` inside `Check` | none | no | yes | only for a run that changed something (below) | by hand 0, 1, 2; `--scheduled` 0, 1, 2 |
| `health clear <name>` | the health lock (30 s) | none | no | no | yes | 0, 1, 2 |

Neither verb shows a panel, so neither takes `--dry-run` or `--yes`. Progress lines go to stderr and everything
else to stdout; under the timer both reach the journal.

**Wiring, for both verbs.** `host.ReadSettings` (a Host that is not set up, or an `abyssctl.env` that does not
parse, is refused, exit 2), then `Paths`, `store.New`, `proxy.New`, `versions.New`, `locks.New` as for every verb,
`Zone` `time.Local`, and for `health check` the `Sender`: `alerts.New(Config{…})` with the settings' pair and
`buildinfo.PushoverURL`. `ErrNotConfigured` gives no `Sender` and no note. A lone key gives no `Sender`, a warning
line, and `SenderNote` set to the refusal's sentence; the run goes on. `health clear` builds no `Sender`.

**The health lock.** The CLI takes `Paths.HealthLock()` while holding no other lock, calls the module, and
releases it ([docker-and-locks.md](docker-and-locks.md)). With `--scheduled` it uses `TryAcquire`: a busy lock
emits `skipped reason=busy holder=<pid>`, prints "skipped: another Health check is running (pid <pid>)", reads and
writes nothing (the run that holds it writes the files) and exits 0. By hand, and for `health clear`, it uses
`Acquire` with `locks.DefaultWait`; a timeout is exit 1 with [cli.md](cli.md)'s lock-timeout wording, which names
the holder.

**`health check`.** Calls `Check`. It prints the events as progress lines, each of `Result.Warnings`, then each
current Problem as `! <label>  <Text> (since …)` with its hint (`→ abyssctl start kids`), then the outcome line:
"✓ No Problems (3 Instances checked)" or "✗ 2 Problems (3 Instances checked)", with "; 1 Alert not delivered:
<error>" added while any is unsent. Each `Busy` Instance is listed as "kids skipped (busy)".

- By hand: exit 1 when any Problem is current, else 0; a `Check` error is exit 1; a refusal is exit 2.
- `--scheduled`: exit 0 whatever it found, a skipped run included; exit 1 for a `Check` error or a lock error
  other than busy; exit 2 for a refusal before `Check` (a Host that is not set up), exit 4 for a broken `abyssctl.env`. A
  Problem is not a failed timer run.
- `--json` prints the `health check` document in [cli.md](cli.md#--json-schemas). `skipped` is true only for a
  `--scheduled` run that found the health lock busy and did nothing. The exit code is the same as without
  `--json`.
- **Activity log.** A run writes its log lines and outcome line to the activity log only when it started, ended
  or dropped a Problem, sent an Alert or failed to, or fetched versions (whether or not the fetch worked), or when
  the run fails. A quiet
  run, and a skipped one, logs nothing, so the timer's runs every few minutes do not fill the log.

**`health clear <name>`.** Exactly one argument, else refused (exit 2). Calls `Clear`. Outcome: "✓ Cleared 1 Event
Problem of kids: the scheduled Backup failed", or "✓ kids has no Event Problems to clear" (exit 0). A lock timeout
is exit 1; an unknown name is "no such Instance: <name>" (exit 2, hint `abyssctl list`). Every run is in the activity log.

**`status`.** The CLI gives Lifecycle a `HealthReader` that calls `Read` and maps `View` to
`lifecycle.HealthView`: `ErrNoResults` → `lifecycle.ErrNoHealthResults`, each `Problem` →
`HealthProblem{Label, Text, Hint, Since}` with the values as they are, and the versions as they are.

## 4. Files and formats it owns

### `state/health.json`

Root:root 0600, written with `hostfs.WriteFile` by `Check` and `Clear` while the caller holds the health lock.
Machine-facing, so every time is RFC 3339 UTC.

```json
{
  "schema": 1,
  "checked_at": "2026-11-18T19:10:02Z",
  "instances": [
    { "name": "ann", "valid": true, "actual_version": "26.10.0", "tool_version": "0.2.0" },
    { "name": "kids", "valid": true, "actual_version": "26.11.0", "tool_version": "0.3.0" },
    { "name": "old", "valid": false }
  ],
  "proxy": { "set_up": true, "version": "2.10.0", "behind": false },
  "versions": {
    "actual": "26.11.0", "caddy": "2.10.2",
    "fetched_at": "2026-11-18T08:00:31Z", "tried_at": "2026-11-18T08:00:31Z", "error": ""
  },
  "problems": [
    { "kind": "instance_down", "subject": "kids", "label": "kids", "text": "down: container exited (code 1)",
      "hint": "abyssctl start kids", "event": false, "since": "2026-11-18T19:05:01Z",
      "delivered": true, "expired_sent": false, "used_by": [] }
  ],
  "counts": [
    { "kind": "instance_unhealthy", "subject": "ann", "failures": 1, "first_at": "2026-11-18T19:10:02Z" }
  ],
  "outcomes": [
    { "name": "kids", "backup_at": "2026-11-18T08:00:04Z", "backup_cleared_at": "",
      "upgrade_at": "2026-11-16T14:21:37Z", "upgrade_cleared_at": "2026-11-16T14:21:37Z" }
  ],
  "queued": [
    { "kind": "low_disk", "subject": "host", "phase": "ended", "title": "disk: resolved: low disk space",
      "text": "…", "since": "2026-11-18T09:12:00Z", "at": "2026-11-18T13:40:00Z" }
  ],
  "delivery": { "channel": "pushover", "sender_note": "", "failing_since": "", "last_error": "" }
}
```

- `schema` is `1`; `Read` refuses any other. Unknown fields are ignored and not written back.
- `instances` is the summary the MOTD's header and `↑` lines need; a busy Instance keeps last run's entry.
  `proxy` is last run's when Docker is down.
- `problems` is every current Problem; `delivered` is its Started Alert's flag, `expired_sent` the Certificate
  re-alert's, and `used_by` a Certificate's `UsedBy` as last seen (empty for other kinds). `counts` holds the
  debounce counts below 2 and the first sighting's time. `outcomes` holds, per Instance and section, the outcome
  `at` last acted on and the clear mark. `queued` is the Ended Alerts not yet delivered, as `alerts.Alert` fields.
- `delivery.channel` is `pushover` when `Check` had a `Sender`, `none` otherwise; `sender_note` is
  `Deps.SenderNote`. `Check` sets both every run and `Clear` keeps them. `failing_since` and `last_error` are
  empty while nothing is failing; `last_error` is the `Send` error's text, which [alerts.md](alerts.md) keeps free
  of Secrets.

### `state/motd.txt`

Root:root 0600, UTF-8, `\n` line ends, the format in [The MOTD](#the-motd). Rewritten by every `Check` and by
every `Clear` that ends something, even when its bytes do not change.

### `state/health.lock`

The health lock: an empty file locked with `flock` by the [Locker](docker-and-locks.md), holding the holder line
like every other lock. The CLI takes it around `health check` and `health clear` ([What the CLI
does](#what-the-cli-does)), so a by-hand `health check`, the timer's run and a `health clear` never write
`health.json` over each other or send an Alert twice. It is taken while holding no other lock, and the only lock
taken under it is an Instance lock by `TryAcquire`, inside `Check`.

### Package layout

```
internal/health/
  health.go     Health, Deps, New, Check, Clear, Read, View, Problem
  kinds.go      the Problem kinds, their order, texts, Ended phrases and hints
  check.go      one Check: the looks, the debounce, the Event Problems
  alert.go      building and sending Alerts, the queue
  state.go      health.json
  motd.go       motd.txt
internal/cli/
  health.go     health check, health clear, the health lock, the HealthReader adapter for status
```

## 5. Depends on

- [foundation](foundation.md): `hostfs.Paths` (`DataRoot`, `HealthState`, `MotdText`, `InstanceDir`,
  `InstanceLock`, `InstancePreRestore`; `HealthLock` in the CLI), `hostfs.WriteFile`, `op.Emitter`, `op.Clock`,
  `op.Refused`, `op.ErrNeedsOperator`, `buildinfo.ImagePin`, `buildinfo.CompareVersions`, and in the CLI `buildinfo.CompiledPins`,
  `buildinfo.Version`, `buildinfo.ActualRepo`, `buildinfo.CaddyRepo` and `buildinfo.PushoverURL`.
- [docker-and-locks](docker-and-locks.md): `docker.Client.ListContainers` and `Container`, `Container`'s `State`,
  `Health` and `ExitCode`, `names.InstanceLabel`, `names.InstanceProject`; `locks.Locker` (`TryAcquire`,
  `BusyError.Holder`), and in the CLI `Acquire` and `DefaultWait` for the health lock.
- [store](store.md): `List`, `Get`, `Outcomes`, `ErrNoInstance`, the record's `STATE`, `HOSTNAME`,
  `ACTUAL_VERSION`, `TOOL_VERSION` and `UPGRADE_BACKUP`, and `RecordError`'s problems.
- [proxy](proxy.md): `Status`, `Certificates`, `Probe` and its errors.
- [versions](versions.md): `Latest`, through the `Versions` seam (the CLI passes `*versions.Versions`), and
  `registrytest` in tests.
- [alerts](alerts.md): `Alert`, `Started`, `Ended`, `Sender`, `ErrRejected`, `ErrLimited`, `ErrInvalid`, and in
  the CLI `New` and `ErrNotConfigured`; `alertsfake` and `pushovertest` in tests.
- [host](host.md): `ReadSettings` (`DiskMinFree`, `CertWarnDays`, `BaseDomain`, the Pushover pair), called by the
  CLI.
- [render](render.md): nothing at build time; the MOTD script reads `motd.txt` and the units run `health check
  --scheduled`.
- [cli.md](cli.md): the output contract, the `--json` style, the lock-timeout wording and exit codes.

Import rules this spec adds to `internal/archtest`:

- `internal/health` imports only the standard library, `internal/hostfs`, `internal/op`, `internal/buildinfo`,
  `internal/docker`, `internal/docker/names`, `internal/locks`, `internal/store`, `internal/proxy` and
  `internal/alerts`. It does not import `internal/versions`: the seam returns a `buildinfo.ImagePin`.
- Only `internal/cli` imports `internal/health`.

## 6. Decisions

- **One Alert sender.** Backup and Upgrade write `outcomes.json`; only the Health check sends. Every Problem,
  however it arises, reaches the Operator the same way, on the same schedule.
- **Debounce only what flaps.** Instance down, Instance unhealthy, Proxy down and Docker down need two runs in a
  row, so a restart or an Upgrade's few seconds of downtime between runs does not page anyone. Low disk,
  Certificates, interrupted operations, invalid records, Orphans and Event Problems start at once: they do not fix
  themselves in five minutes.
- **A held lock means "working on it".** A busy Instance is skipped for the run, not judged, so a Backup's
  stopped container or an Upgrade's switch never raises "down". The container is looked at under the lock, so
  what Health judges is what the lock protects.
- **Only `STATE=running` can be down.** An Instance the Operator stopped is not a Problem.
- **The Proxy outage is one Problem.** When the Proxy is down, no Instance is judged unhealthy that run.
- **Docker down judges nothing that needs Docker.** The Problems Docker decides stay as they were; files
  (outcomes, records, Certificates, disk) are still read.
- **`/health` through the Proxy, body checked.** The probe uses the Hostname as SNI and Host header, so it checks
  the path a user takes. An empty 200 (a covered Hostname with no site) is not healthy.
- **Problem identity is kind + subject**, so a changing text is the same Problem and a Certificate is tracked by
  its id.
- **Deliver at least once, retry next run, stop at the first failure.** A broken channel costs one request per
  interval, and Pushover never sees a burst of failing requests.
- **Fixed sentences off the Host.** Alerts and the MOTD carry no foreign error text except a Backup's own reason,
  so no path or Secret leaks through a Docker or file system message.
- **Pre-rendered MOTD, rewritten every run.** A login never runs `abyssctl` or Docker, and the file's age alone
  says whether the timer runs.
- **Versions once a day, never alarming.** The registry is asked at most daily (and at most hourly after a
  failure), and a failed lookup only ages the "version info" line.
- **`--scheduled` exits 0 on Problems.** systemd shows a failed unit only when the check itself could not run.
- **A health lock of its own, taken by the CLI.** The Host lock is held for minutes by `create` and `host setup`;
  the health lock is held only by `health check` and `health clear`, so the timer is never blocked by an unrelated
  command, and Health stays a module that does not take its own outer lock.
- **Quiet runs leave no activity log.** The timer runs every few minutes; only a run that changed something is
  worth a line.

## 7. Later

- Roadmap Next: a notice on the MOTD and in `status` when a newer Abyss version is released.
- Roadmap Later: an Alert when an Actual or Caddy update becomes available.

## 8. Docs it changes

- `docs/guides/alerts.md` (the Health part; [alerts.md](alerts.md) covers Pushover and [host.md](host.md) the
  settings): what the Health check looks at and how often; each Problem kind, what it means and its fix (the
  kinds table); Live and Event Problems, and that `health clear <name>` ends an Instance's Event Problems; for a
  failed scheduled Backup, fix the cause, then `health clear <name>`, and that the next scheduled Backup that
  succeeds also ends it while a Backup by hand does not; the two-run wait for down, unhealthy, Proxy down and
  Docker down; that a busy or stopped Instance is not judged; that an Orphan's Problem ends without a recovery
  message; that a Certificate in use warns `CERT_WARN_DAYS` before expiry and again when expired, and that an
  expired exact-name Certificate must be removed with `cert remove`; reading the MOTD (`!` and `↑` lines,
  "Alerts not delivered", "Alerts not sent", "version info stale"); that an undelivered Alert is sent at the next
  run, and an Alert may rarely arrive twice; and `health check` by hand, its exit codes, `--json`, and which runs
  reach the activity log.
- Reference: the `health check` and `health clear` pages, from their help text.

## 9. Acceptance list

Module tests are in `internal/health`, with real `Store` and `Locker` on `hostfs.Under(t.TempDir())`,
`dockerfake`, a fake `Proxy`, `registrytest`, `alertsfake`, a fake `DiskFree`, an `op.ManualClock`, an
`op.Recorder` and two `Zone`s. CLI tests are in `internal/cli`. This spec's journey slice is journey 7's `health`
steps ([testing.md](testing.md)).

1. Each Live Problem kind starts on the run its rule says, with its label, text, hint and `Since`: Docker down,
   Instance folders unreadable, Proxy not set up / invalid settings / not running / unreachable, no container and
   each non-running state (`removing` included), Docker healthcheck failing, interrupted Upgrade, interrupted
   Restore, invalid record (with and without a line number), unreadable `outcomes.json` (no hint), Orphan, low disk
   and unreadable disk; texts that come from a failed look are the fixed sentences, and the look's error is only in
   the `look` event. → `TestCheck_LiveProblems` (table)
2. Instance down, Instance unhealthy, Proxy down and Docker down start on the second failing run in a row with
   `Since` the first; one good run ends each and resets its count; the other kinds start on the first. →
   `TestCheck_Debounce` (table)
3. The probe goes to the record's Hostname and `/health`; a 200 with `"status":"UP"` is good; an empty 200, a 502,
   a 200 without `"UP"`, `ErrHandshake`, `ErrWrongCertificate`, a timeout and another error are unhealthy
   sightings with their texts. → `TestCheck_Probe` (table)
4. A Proxy not running, or a probe answering `ErrUnreachable`, raises `proxy_down` and no `instance_unhealthy`
   that run; no further probe is made and the unhealthy counts are unchanged. → `TestCheck_ProxyDownSuppresses`
5. Docker down (a failed `ListContainers`, a `ListContainers` or `Container` failing with
   `context.DeadlineExceeded`, or a `Proxy.Status` error that is not a refusal) raises `docker_down` on the second
   such run; it makes no `Proxy.Status`, `Container` or probe call after it; it keeps every `proxy_down`,
   `instance_down`, `instance_unhealthy` and `orphan` Problem and count as it was; it still reads outcomes, records,
   Certificates and disk, and still writes both files. → `TestCheck_DockerDown` (table)
6. `STATE=stopped` raises neither down nor unhealthy and ends them; an invalid record raises only
   `invalid_record`, with no lock, probe or Event Problem. → `TestCheck_WantedState` (table)
7. With a test holding an Instance lock, that Instance is in `Busy`, its Live Problems and counts are unchanged,
   the `skipped` event names the holder, and the others are checked; Check never holds two Instance locks and holds
   none when it returns. → `TestCheck_SkipsBusy`
8. The container is judged from `Docker.Container` under the Instance lock: a container stopped in step 2's list
   but running by the time its lock is held is not a down sighting. → `TestCheck_JudgesUnderLock`
9. An Instance removed after `Store.List` (its lock or `Get` gives `fs.ErrNotExist`) has its Problems dropped with
   no Ended Alert; a lock error other than busy or not-exist emits the `skipped` event and counts the Instance as
   busy. → `TestCheck_InstanceGone` (table)
10. An interrupted Upgrade or Restore raises its Problem and no down or unhealthy judgement; a current
    `rollback_failed`, or one started earlier in the same run, keeps `interrupted_upgrade` from starting, and
    clearing it lets it start; `rollback_failed` starting while `interrupted_upgrade` is current ends
    `interrupted_upgrade` with no Ended Alert. → `TestCheck_Interrupted` (table)
11. Event Problems follow the outcome sequences: failed scheduled Backup (guard and free-space refusals included)
    starts `backup_failed` with the hint `abyssctl health clear <name>`, a newer `ok` ends it; `rolled_back` and
    `rollback_failed` start theirs, `upgrade_rolled_back` with the hint `less <newest upgrade-failed log>` or none
    without one; a newer `ok` ends both; `rolled_back` after `rollback_failed` swaps them; an outcome no newer than
    a clear starts nothing; a busy lock does not stop them. → `TestCheck_EventProblems` (table)
12. An unparseable `outcomes.json` raises `outcomes_unreadable`, keeps that Instance's Event Problems, and the
    check succeeds. → `TestCheck_OutcomesUnreadable`
13. An Orphan's Problem ends, with no Ended Alert queued or sent, once no labelled container is left. →
    `TestCheck_OrphanEnds`
14. `Clear` ends only the named Instance's Event Problems, sets each clear mark to the `at` in `health.json` (an
    `outcomes.json` changed since is not read), keeps the delivery state, writes both files, sends and queues no
    Alert, returns no kinds and writes nothing when there are none, and returns `ErrNoInstance` for an unknown
    name. → `TestClear` (table)
15. Certificates: an unused Certificate raises nothing; one in use raises `certificate_expiry` within
    `CERT_WARN_DAYS` and not before; expiry changes the text and sends one more Started Alert; one first seen
    expired, or expiring while its first Alert is undelivered, gets exactly one Started Alert, saying "expired";
    the Ended reason is `replaced` (another in-use Certificate now has every Hostname of the stored `UsedBy`),
    `removed`, `no longer in use` or `outside the warning window`; the hint is `cert remove <id>` when another
    Certificate covers every Hostname in `UsedBy` and `cert install` otherwise; a `Certificates` error keeps the
    Problems. → `TestCheck_Certificates` (table)
16. Low disk compares `Avail × 100 / Total`, rounded down, with `DISK_MIN_FREE`: below starts, equal ends. →
    `TestCheck_Disk` (table)
17. With `alertsfake`: a started Problem gives one Started Alert and later one Ended Alert, never repeated; at the
    first failed `Send` (`ErrRejected` and `ErrLimited` included) the rest are kept, `Unsent` and `SendError` set,
    and the next run sends them, Ended first; an `ErrInvalid` Alert is dropped and sending goes on; an Ended Alert
    whose Started was never delivered is dropped; the queue keeps at most 50; the run never fails because of a
    `Send`. → `TestCheck_Delivery` (table)
18. With no `Sender`, Problems reach `health.json` and the MOTD and nothing is sent, and `delivery.channel` is
    `none`; when a `Sender` is given later, the current Problems' Started Alerts are sent; `SenderNote` reaches
    `delivery.sender_note` and the "Alerts not sent" line. → `TestCheck_NoSender`
19. Every kind's Started and Ended Alert matches its golden file in two zones, with the zone abbreviation, the
    `Host:` line, the Ended phrase, and no `Run:` line for `upgrade_rolled_back`; no Alert holds the Data root's
    path (an outcome error holding it shows "the Data root"), any other file path, or any value from the settings'
    Pushover pair. → `TestAlertText` (golden)
20. Versions: the first run fetches both, a run within 24 hours fetches nothing, a run after 24 hours fetches again;
    a failure keeps the old values and `fetched_at`, records the error, raises no Problem and no Alert, and is
    retried after an hour, not before. → `TestCheck_Versions` (with `registrytest`)
21. `motd.txt` matches its golden file for: clean, no Instances, each `!` kind, "since" with and without a date,
    "Alerts not delivered" with and without a date, "Alerts not sent", an Actual update, files out of date, the
    Proxy pin behind, a Caddy update, "version info stale", and more than 10 lines; `Clear` and `Check` render the
    same bytes from the same `health.json`. → `TestMotd` (golden)
22. Every Check writes `health.json` and `motd.txt` through `hostfs.WriteFile` at 0600, even when nothing changed
    and when Docker is down. → `TestCheck_WritesEveryRun`
23. `health.json` round-trips; `Read` gives `ErrNoResults` with no file and an error naming the file for bad JSON or
    `schema` 2; Check over a broken file starts fresh, puts the warning in `Result.Warnings` and succeeds. →
    `TestState` (table)
24. Check emits the `look`, `problem`, `skipped`, `alert`, `versions` and `checked` events with the documented keys,
    the Data root's path masked and no Secret. → `TestCheck_Events`
25. Check never changes an Instance: over a run with every kind of Problem, `dockerfake.Calls()` holds only
    `container.ls` and `container.inspect`, and every file outside `state/` keeps its bytes and mtime. →
    `TestCheck_ReadOnly`
26. The health lock, taken by the CLI: `health check --scheduled` with it held prints the skip line, writes neither
    file and exits 0; `health check` by hand waits and, when a running Check holds the lock longer than the wait,
    exits 1 with the lock-timeout wording; `health clear` waits for a running Check and gets the lock when it ends,
    or exits 1 after the wait. → `TestHealthLock` (table) in `internal/cli`
27. `health check` by hand prints the Problems with hints, exits 1 with any Problem and 0 without; `--scheduled`
    exits 0 with Problems and when skipped, 1 when `Check` fails, 2 when the Host is not set up or `abyssctl.env`
    is broken; `--json` gives the documented object (`warnings` included) with the same exit code; progress goes to
    stderr; the activity log gets a run that started, ended or dropped a Problem, sent or failed an Alert or fetched
    versions, and nothing from a quiet or skipped run. → `TestHealthCheckCmd` (table) in `internal/cli`
28. `health check` with no Pushover pair runs with no `Sender`; with one key it warns, sets `SenderNote` and still
    runs; neither changes the exit code. → `TestHealthCheckCmd_Sender` (table) in `internal/cli`
29. `health clear` with no or two arguments is exit 2; it prints the cleared kinds or "no Event Problems to clear",
    and writes the activity log. → `TestHealthClearCmd` (table) in `internal/cli`
30. `status` gets the Problems with label, text, hint and `Since`, `CheckedAt` and versions from `Read` through the
    CLI's `HealthReader`, with `ErrNoResults` shown as "no Health check results yet". → `TestStatusCmd_Health` in
    `internal/cli`
31. `internal/health` imports only the packages in [Depends on](#5-depends-on) (not `internal/versions`), and only
    `internal/cli` imports it. → `TestImportRules` in `internal/archtest`
32. Journey 7, with the health timer stopped first and a running Instance `kids`: `health check` exits 0, and the
    MOTD shows the all-healthy header and `↑ kids <version> → 99.0.2 available`, `99.0.2` being the highest tag in
    the journey registry. `docker stop abyssctl-kids`; one `systemctl start abyssctl-health.service` sends nothing;
    a second sends the Instance-down Alert to `pushovertest` at priority 0, and the MOTD shows `! kids down`.
    `health check` by hand exits 1. `docker start abyssctl-kids`, wait until healthy; the next run sends the
    recovery at priority -1 and the MOTD shows the all-healthy header again. One more run leaves `motd.txt`'s bytes
    unchanged with a newer mtime; `alerts test` leaves `health.json` unchanged. → journey 7
33. Journey 7: the harness writes an `outcomes.json` for `kids` with a failed scheduled Backup and sets
    `pushovertest` to answer 500 once; the next run raises `backup_failed`, its Started Alert is not delivered and
    the MOTD shows "Alerts not delivered since"; the run after delivers it and the line goes. → journey 7
34. Journey 7: `health clear kids` exits 0 and the MOTD no longer shows `backup_failed`; `health clear nosuch`
    exits 2. → journey 7
35. Docs: the Health part of `docs/guides/alerts.md` and the `health check` and `health clear` reference pages say
    what [Docs it changes](#8-docs-it-changes) lists. → (review); the reference pages also by the
    generated-reference check in CI
