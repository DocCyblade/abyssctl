# Spec: render

Build step 4 of 16. The [overview](README.md) has the module map, the call graph and the Host layout. Shared
packages (`internal/hostfs`, `internal/op`, `internal/buildinfo`) are in [foundation.md](foundation.md); the Docker
names are in [docker-and-locks.md](docker-and-locks.md). Capitalised terms are defined in
[GLOSSARY.md](../../GLOSSARY.md).

## 1. Purpose

Build the Renderer (`internal/render`): the one module that knows what every generated file says. It renders:

- an [Instance](../../GLOSSARY.md)'s `compose.yaml`;
- an Instance's site file for the [Proxy](../../GLOSSARY.md) (`proxy/sites/<name>.caddy`);
- the Proxy's `Caddyfile` and `compose.yaml`;
- the four systemd units: the [Health check](../../GLOSSARY.md) service and timer, and the nightly
  [Backup](../../GLOSSARY.md) service and timer;
- the [MOTD](../../GLOSSARY.md) script;
- the logrotate rule for the activity log.

Render is pure. It does no I/O of any kind: no file, network, clock, environment or Docker call. Each function
takes plain values and returns bytes or an error. Render never writes a file. Its callers do that:

| File | Rendered by | Written by |
|---|---|---|
| `instances/<name>/compose.yaml` | `InstanceCompose` | Store (`Store.WriteCompose`, `Staged.WriteCompose`) |
| `proxy/sites/<name>.caddy` | `Site` | Proxy |
| `proxy/Caddyfile`, `proxy/compose.yaml` | `Caddyfile`, `ProxyCompose` | Proxy |
| `abyssctl-{health,backup}.{service,timer}` | `Units` | Host |
| `/etc/update-motd.d/90-abyssctl` | `MotdScript` | Host |
| `/etc/logrotate.d/abyssctl` | `LogrotateRule` | Host |

Owners and modes belong to the writers and are in the overview's Host layout. Render calls no other module. Its
one `internal/` import is `internal/docker/names`, for the project, container, network and label names it writes.
When this spec is done, every generated file exists as a golden file, and later modules write bytes they get from
one function call.

## 2. Interface

Signature sketches, not final code.

```go
// Instance compose.yaml.
type Instance struct {
    Name           string         // a valid Instance name
    Image          string         // full reference: "<repo>:<X.Y.Z>@sha256:<64 hex>"
    UID, GID       int            // the abyssctl account's actual IDs; both > 0
    Internet       bool           // the record's INTERNET: also join abyssctl-external
    TrustedProxies []netip.Prefix // the subnets of each network the Instance joins, read back by the caller:
                                  // abyssctl's, plus abyssctl-external's when Internet; at least one
}

func InstanceCompose(in Instance) ([]byte, error)

// One Instance's site file.
type SiteInput struct {
    Name     string // the Instance name: the upstream is names.InstanceProject(Name) on port 5006
    Hostname string // a valid Hostname
    CertID   string // the chosen Certificate's id: 12 lowercase hex digits
}

func Site(in SiteInput) ([]byte, error)

// ParseSite reads back the SiteInput a site file was rendered from: Name from the header comment, Hostname from
// the server line, CertID from the tls line. An error wrapping ErrInput for anything Site would not have written.
// Proxy uses it to re-render site files when the chosen Certificate changes (proxy.md).
func ParseSite(b []byte) (SiteInput, error)

// The Proxy.
func Caddyfile() []byte                         // takes no input; always the same bytes
func ProxyCompose(image string) ([]byte, error) // full reference to the Caddy image, built from proxy.env

// systemd units.
const (
    HealthService = "abyssctl-health.service"
    HealthTimer   = "abyssctl-health.timer"
    BackupService = "abyssctl-backup.service"
    BackupTimer   = "abyssctl-backup.timer"
)

type Schedule struct {
    Binary         string        // absolute path of the binary, hostfs.Paths.Binary()
    HealthInterval time.Duration // HEALTH_INTERVAL
    BackupTime     string        // BACKUP_TIME, "HH:MM", Host local time
}

type File struct {
    Name string // a file name with no folder, e.g. "abyssctl-health.timer"
    Data []byte
}

func CheckSchedule(s Schedule) error   // the rules Units applies; Host checks settings with it
func Units(s Schedule) ([]File, error) // the four units, sorted by Name

// The MOTD script.
type Motd struct {
    MotdText       string        // absolute path of state/motd.txt, hostfs.Paths.MotdText()
    HealthInterval time.Duration // the same value as in Schedule
}

func MotdScript(in Motd) ([]byte, error)

// The logrotate rule.
func LogrotateRule(logFile string) ([]byte, error) // absolute path, hostfs.Paths.LogFile()

// Every error from Render wraps ErrInput.
var ErrInput = errors.New("render: invalid input")
```

- Render has no state and no constructor. Every function is safe for concurrent use.
- The input types hold only what the files need. Render does not import `internal/store`: callers fill
  `Instance` and `SiteInput` from a `store.Record` (`Name`, `Record.Image(buildinfo.ActualRepo)`, `Hostname`) and
  from what they read elsewhere (the account's IDs, the subnets of the networks the Instance joins, the chosen
  Certificate). `Internet` comes from the record's `INTERNET` ([store.md](store.md)).
- The Proxy's image reference is built by the Proxy from `PROXY_VERSION` and `PROXY_DIGEST` in `proxy.env` and
  `buildinfo.CaddyRepo`, not from the binary's compiled-in pin ([proxy.md](proxy.md)).
- An error means the caller passed a value it should have checked already, so it is a bug. Errors are plain
  errors wrapping `ErrInput`, not `op` errors, and give exit 1. The one exception is `CheckSchedule`: Host checks
  `--health-interval` and `--backup-time` with it and refuses with `op.Refused(msg).WithCause(err)`, and
  `host.ReadSettings()` checks hand-edited `HEALTH_INTERVAL` and `BACKUP_TIME` values in `abyssctl.env` with it
  ([host.md](host.md)).

## 3. Behaviour

### Checking input

Render writes every value literally and escapes nothing. Instead it refuses, with an error naming the field and
the rule, any value that could break the syntax of the file it goes into:

| Input | Rule |
|---|---|
| Instance name | 1–32 characters of `[a-z0-9-]` starting with a letter (Store checks the full rules) |
| [Hostname](../../GLOSSARY.md) | 1–253 characters of `[a-z0-9.-]`, at least one dot (Store checks the full rules) |
| Image (`Instance.Image` and `ProxyCompose`'s `image`) | `[<registry>/]<repo>:<tag>@sha256:<64 lowercase hex>`: an optional registry host with an optional port (`[a-z0-9.-]+(:[0-9]{1,5})?/`, e.g. `127.0.0.1:5000/` for the journeys' local registry), a repo of `[a-z0-9._/-]+`, and a plain `X.Y.Z` tag: three decimal numbers with no leading zeros. As one pattern: `^([a-z0-9.-]+(:[0-9]{1,5})?/)?[a-z0-9._/-]+:(0\|[1-9][0-9]*)\.(0\|[1-9][0-9]*)\.(0\|[1-9][0-9]*)@sha256:[0-9a-f]{64}$`. The tag rule is `buildinfo.PlainVersion`'s ([foundation.md](foundation.md)); render can't import `buildinfo`, so it keeps its own unexported copy, tested against the same table |
| UID, GID | greater than 0 |
| `TrustedProxies` | at least one; each a valid prefix with no host bits set |
| `CertID` | exactly 12 characters of `[0-9a-f]` |
| `Binary`, `MotdText`, `logFile` | absolute and clean (`filepath.Clean` gives it back unchanged), only `[A-Za-z0-9._/-]` |
| `HealthInterval` | a whole number of minutes from 1 to 60 |
| `BackupTime` | `HH:MM`, 00–23 and 00–59, two digits each |

The path rule keeps every path safe inside single quotes in the MOTD script and unquoted in a systemd
`ExecStart=` line. Host refuses a `--data-root` that breaks it, so a valid Host never hits this.

### Same input, same bytes

- Every output is a pure function of its input. Templates are embedded with `go:embed` and run with
  `text/template`, with no map iteration and no time or random value.
- `TrustedProxies` are written sorted (IPv4 before IPv6, then by address, then by prefix length), with
  duplicates dropped, so the same network always gives the same file.
- No file holds the tool's version or a timestamp. A [Tool update](../../GLOSSARY.md) that leaves a template
  unchanged gives the same bytes, so the writers' "write only when changed" finds nothing to write.
- Each file starts with a header comment saying abyssctl generated it and a hand edit is overwritten. The header
  of an Instance's files names the Instance.

### When files are rendered

Render does not decide when; its callers do. For reference:

- An Instance's `compose.yaml` is rendered by the command acting on that Instance (`create`, `set`, `start`,
  `restart`, `upgrade`, Restore, Rollback), never by `host setup`, which recreates a drifted container from its existing
  `compose.yaml`. Its site file is rendered by `create`, `start` and `restart` only: `upgrade` and Restore keep the
  Hostname and leave the site file alone. `remove` deletes the Instance's site file.
- Site files are also re-rendered whenever the Certificate chosen for a Hostname changes: a `cert install` whose
  Certificate replaces or beats the current one, a `cert remove`, and a `host setup --cert/--key`
  ([proxy.md](proxy.md)).
- The Caddyfile, the Proxy's `compose.yaml`, the units, the MOTD script and the logrotate rule are rendered by
  `host setup` (and the Proxy files also by `proxy upgrade`), so a re-run after a Tool update brings them up to
  date.

## 4. Files and formats it owns

The package holds one template per file and one golden file per tested output:

```
internal/render/
  render.go              the functions and input checks
  templates/*.tmpl       embedded with go:embed
  render_test.go
  testdata/*.golden      the expected bytes
```

Each example below is the golden file's content for the inputs shown. The golden files in `testdata/` are the
contract; this section shows them so other specs can be read without the code. A tab indents Caddyfile and
systemd examples; YAML uses two spaces.

### Instance `compose.yaml`

For `Name` `kids`, the Actual image `docker.io/actualbudget/actual-server:26.11.0@sha256:<digest>`, UID:GID
`998:998`, no [Internet access](../../GLOSSARY.md) and the subnet `172.30.0.0/16`:

```yaml
# Generated by abyssctl for Instance kids. Do not edit: changes are overwritten.
name: abyssctl-kids
services:
  actual:
    image: docker.io/actualbudget/actual-server:26.11.0@sha256:<digest>
    container_name: abyssctl-kids
    user: "998:998"
    restart: unless-stopped
    environment:
      ACTUAL_LOGIN_METHOD: password
      ACTUAL_ALLOWED_LOGIN_METHODS: password
      ACTUAL_TRUSTED_PROXIES: 172.30.0.0/16
    volumes:
      - type: bind
        source: ./data
        target: /data
    networks:
      abyssctl:
        aliases:
          - abyssctl-kids
    labels:
      io.abyssctl.instance: kids
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    healthcheck:
      test: ["CMD", "node", "/app/src/scripts/health-check.js"]
      interval: 15s
      timeout: 10s
      retries: 3
      start_period: 5m
    logging:
      driver: json-file
      options:
        max-size: 10m
        max-file: "3"
networks:
  abyssctl:
    external: true
```

With Internet access (`Internet` true) and the subnets `172.30.0.0/16` and `172.31.0.0/16`, the file differs in
three places, and nothing else changes:

```yaml
      ACTUAL_TRUSTED_PROXIES: 172.30.0.0/16,172.31.0.0/16
```

```yaml
    networks:
      abyssctl:
        aliases:
          - abyssctl-kids
      abyssctl-external: {}
```

```yaml
networks:
  abyssctl:
    external: true
  abyssctl-external:
    external: true
```

- `name:`, `container_name:` and the network alias are `names.InstanceProject(Name)`; the networks are
  `names.Network` and, with Internet access, `names.ExternalNetwork`; the label key is `names.InstanceLabel`. The Docker adapter's fake adds that label itself, so
  only the golden file and journey 3 prove the real file carries it.
- Both networks are external: `host setup` creates them, and a Compose up or down never creates or removes one.
  The alias is on `abyssctl` only, which is the network the Proxy reaches the Instance over; the Instance needs
  no alias on `abyssctl-external`, where it only leaves the Host. There is no gateway setting: a container on an
  internal and an ordinary bridge network takes its default route from the ordinary one
  ([ADR 0005](../adr/0005-internet-access-per-instance.md)).
- With several subnets, `ACTUAL_TRUSTED_PROXIES` lists them joined by commas. With Internet access it lists both
  networks' subnets, because the container name resolves on both and the Proxy may reach the Instance over
  either.
- `./data` is relative to the Instance folder, which the Docker adapter passes as the project directory. The long
  bind syntax does not create a missing source folder, so a missing `data/` fails the start instead of Docker
  making a root-owned one.
- The image is written in full, tag and digest, so `docker compose up --pull never` runs exactly the pinned
  image.
- There is no `ports:` (only the Proxy publishes ports), no `env_file:` (the [Instance record](../../GLOSSARY.md)
  is never handed to the container, and its unknown keys are never rendered), no memory or CPU limits, no
  `read_only` and no override file.
- The healthcheck runs `/app/src/scripts/health-check.js`, the one path every Actual version the tool can run
  has (see [Decisions](#6-decisions)). There is no `start_interval`, so the file is the same on every supported
  Engine; a 15-second interval keeps the first healthy report quick; `start_period: 5m` matches the tool's five-minute wait
  on `/health`. The fake Actual image ships its stub, and a `node` on its `PATH`, at the same path
  ([testing.md](testing.md)).

### Site file (`proxy/sites/<name>.caddy`)

For `Name` `kids`, `Hostname` `kids.budget.example.com` and `CertID` `3f2a9c0d1e4b`:

```caddyfile
# Generated by abyssctl for Instance kids. Do not edit: changes are overwritten.
kids.budget.example.com {
	tls /etc/caddy/certs/3f2a9c0d1e4b.crt /etc/caddy/certs/3f2a9c0d1e4b.key
	reverse_proxy abyssctl-kids:5006
}
```

Render does not pick the Certificate. The Proxy picks it (the most specific one covering the Hostname, then the
latest expiry) and passes its id. There is no `log` directive, so access logs stay off.

### `proxy/Caddyfile`

```caddyfile
# Generated by abyssctl. Do not edit: changes are overwritten.
{
	admin localhost:2019
	auto_https disable_certs
	servers {
		protocols h1 h2
	}
}

import /etc/caddy/sites/*.caddy
```

- `auto_https disable_certs`: Caddy never gets or renews a certificate; the Operator's
  [Certificates](../../GLOSSARY.md) are the only ones. Caddy still redirects HTTP to HTTPS for each site.
- The admin endpoint listens only inside the container, for `caddy reload` and `caddy validate` through
  `docker exec`.
- HTTP/3 is off, because the Proxy publishes only TCP ports 80 and 443.
- The import glob matching no file is valid, so a Proxy with no Instances starts.

### Proxy `compose.yaml`

For the image `docker.io/library/caddy:2.10.2@sha256:<digest>`:

```yaml
# Generated by abyssctl. Do not edit: changes are overwritten.
name: abyssctl-proxy
services:
  caddy:
    image: docker.io/library/caddy:2.10.2@sha256:<digest>
    container_name: abyssctl-proxy
    command: ["caddy", "run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"]
    restart: unless-stopped
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - type: bind
        source: .
        target: /etc/caddy
        read_only: true
      - type: bind
        source: ./caddy-data
        target: /data
      - type: bind
        source: ./caddy-config
        target: /config
    tmpfs:
      - /tmp
    networks:
      - abyssctl
      - abyssctl-external
    read_only: true
    cap_drop:
      - ALL
    cap_add:
      - NET_BIND_SERVICE
    security_opt:
      - no-new-privileges:true
    logging:
      driver: json-file
      options:
        max-size: 10m
        max-file: "3"
networks:
  abyssctl:
    external: true
  abyssctl-external:
    external: true
```

- The project and container are `names.ProxyProject`; the networks are `names.Network` and
  `names.ExternalNetwork`. The Proxy joins `abyssctl` to reach every Instance and `abyssctl-external` because
  Docker publishes ports only from a network that is not internal.
- The whole `proxy/` folder is mounted read-only at `/etc/caddy`, not each file on its own. The writers replace
  files by rename (`hostfs.WriteFile`), and a single-file bind mount keeps showing the old file after a rename. A
  folder mount shows the new one, so a reload reads what was just written. The container also sees
  `compose.yaml` and `proxy.env`, which hold no Secrets.
- `caddy-data/` and `caddy-config/` are Caddy's only writable state, and `/tmp` is a tmpfs, so the root
  filesystem stays read-only.
- Caddy runs as root inside the container with only `NET_BIND_SERVICE`.
- The Proxy image has no healthcheck: the Proxy module probes it through `docker exec` and HTTPS.

### systemd units

For `Binary` `/usr/local/bin/abyssctl`, `HealthInterval` 5 minutes and `BackupTime` `03:00`:

`abyssctl-health.service`:

```ini
# Generated by abyssctl. Do not edit: abyssctl host setup rewrites this file.
[Unit]
Description=Abyss Health check
After=docker.service

[Service]
Type=oneshot
ExecStart=/usr/local/bin/abyssctl health check --scheduled
TimeoutStartSec=10min
```

`abyssctl-health.timer`:

```ini
# Generated by abyssctl. Do not edit: abyssctl host setup rewrites this file.
[Unit]
Description=Run the Abyss Health check every 5 minutes

[Timer]
OnActiveSec=1min
OnUnitActiveSec=5min
AccuracySec=10s

[Install]
WantedBy=timers.target
```

`abyssctl-backup.service`:

```ini
# Generated by abyssctl. Do not edit: abyssctl host setup rewrites this file.
[Unit]
Description=Abyss nightly Backup of every Instance
After=docker.service

[Service]
Type=oneshot
ExecStart=/usr/local/bin/abyssctl backup --all --scheduled
```

`abyssctl-backup.timer`:

```ini
# Generated by abyssctl. Do not edit: abyssctl host setup rewrites this file.
[Unit]
Description=Run the Abyss nightly Backup at 03:00

[Timer]
OnCalendar=*-*-* 03:00:00
RandomizedDelaySec=5min
Persistent=true
AccuracySec=1min

[Install]
WantedBy=timers.target
```

- The services run as root and print to stdout and stderr, so each run reaches the journal. They set no
  `Environment=` and no `EnvironmentFile=`: the binary reads `abyssctl.env` itself, so no unit holds a Secret.
- `After=docker.service` orders a run after Docker at boot without requiring it. A Health check with Docker down
  runs and reports it.
- The Health timer fires a minute after it starts (at boot, or when `host setup` starts it) and then
  `HealthInterval` after each run starts. `TimeoutStartSec=10min` stops a hung check, which would otherwise block
  every later run.
- `OnCalendar` is Host local time, as `BACKUP_TIME` is. `RandomizedDelaySec=5min` starts the Backup up to five
  minutes later; the line is fixed text, so the file stays the same for the same schedule. `Persistent=true` runs a
  Backup missed while the Host was off soon after it boots. The Backup service has no timeout: a Backup of many Instances may take long.
- A timer's `Description` and its `OnUnitActiveSec=` or `OnCalendar=` line are the only lines that change with
  the schedule. The interval is written as `OnUnitActiveSec=<N>min` for every value from 1 to 60, and the
  Description says "every <N> minutes", or "every 1 minute" for 1.

### MOTD script (`/etc/update-motd.d/90-abyssctl`)

For `MotdText` `/srv/abyssctl/state/motd.txt` and `HealthInterval` 5 minutes (so the limit is 3 × 5 minutes =
900 seconds):

```sh
#!/bin/sh
# Generated by abyssctl. Do not edit: abyssctl host setup rewrites this file.
# Prints the status the Health check leaves in motd.txt. Never runs abyssctl or Docker.
motd='/srv/abyssctl/state/motd.txt'
max_age=900
if [ ! -r "$motd" ]; then
	echo 'Abyss — Health check not running (no status yet)'
	echo 'Run: systemctl status abyssctl-health.timer'
	exit 0
fi
now=$(date +%s)
mtime=$(stat -c %Y "$motd" 2>/dev/null || echo 0)
if [ $((now - mtime)) -gt "$max_age" ]; then
	echo "Abyss — Health check not running (last ran $(date -d "@$mtime" '+%Y-%m-%d %H:%M %Z'))"
	echo 'Run: systemctl status abyssctl-health.timer'
	exit 0
fi
cat "$motd"
exit 0
```

- POSIX `sh` plus GNU `stat` and `date`, which every supported Host has. No `jq`, no Docker, no call to the
  binary, so a login never waits on Docker.
- It prints `motd.txt` unchanged when it is fresh. What that file says, including the ↑ lines for files out of
  date and a Proxy pin behind the binary, is the Health check's ([health.md](health.md)).
- The age check relies on one requirement of [health.md](health.md): every Health check run rewrites `motd.txt`,
  even when nothing changed and even when Docker is down. Otherwise a healthy, quiet Host would show "Health check
  not running".
- A stale file is not printed, because an old status reads as a current one. The warning shows when the file
  was last written, in Host local time with the zone.
- It always exits 0, so it never breaks a login.

### logrotate rule (`/etc/logrotate.d/abyssctl`)

For `logFile` `/var/log/abyssctl/abyssctl.log`:

```
# Generated by abyssctl. Do not edit: abyssctl host setup rewrites this file.
/var/log/abyssctl/abyssctl.log {
	weekly
	rotate 8
	compress
	delaycompress
	missingok
	notifempty
	create 0640 root adm
}
```

Each command opens the activity log, appends and closes it ([cli.md](cli.md)), so rotation needs no `copytruncate` and no
`postrotate` signal.

## 5. Depends on

- [foundation](foundation.md): nothing at build time. Callers pass in values from `hostfs.Paths` and
  `buildinfo`; Render imports neither.
- [docker-and-locks](docker-and-locks.md): `internal/docker/names` only (`InstanceProject`, `ProxyProject`,
  `Network`, `ExternalNetwork`, `InstanceLabel`).
- The standard library: `embed`, `text/template`, `bytes`, `errors`, `fmt`, `net/netip`, `path/filepath`,
  `regexp`, `sort`, `strings`, `time`.

Import rules this spec adds to `internal/archtest`. Both check the package's direct imports (the `Imports` field
of `go list -json`), not its dependencies: `text/template`, `embed` and `path/filepath` pull in `os` themselves.

- `internal/render` imports only the standard library and `internal/docker/names`.
- `internal/render` imports none of `os`, `os/exec`, `io/fs`, `net`, `net/http` and `syscall`, so its own code
  can't reach a file, a process or the network.

## 6. Decisions

- One module renders every generated file, so a reader finds every template in one place and every file is
  tested as bytes, with no Docker, Caddy or systemd needed.
- Render is pure and returns bytes. The module that owns a file's location writes it: Store for an Instance's
  `compose.yaml`, Proxy for the Proxy's files, Host for the Host's.
- Render does not import Store. Its input types hold only what the files need, so it stays a leaf and an Instance
  record's unknown keys can't reach a file.
- No YAML, Caddyfile or ini library: the files are small, fixed templates embedded with `go:embed` in the one
  binary ([ADR 0003](../adr/0003-single-static-go-binary.md)). Render refuses unsafe values instead of escaping
  them, which keeps the templates readable and the output exact.
- The healthcheck path is `/app/src/scripts/health-check.js`, verified on the `actualbudget/actual-server`
  Dockerfiles and official compose files at v25.1.0 (`actualbudget/actual-server` repo), and v25.6.0, v25.12.0,
  v26.5.0, v26.6.0, v26.7.0 and v26.11.0 (`actualbudget/actual` repo, `packages/sync-server/docker/` and
  `packages/sync-server/docker-compose.yml`). Up to v26.5.0 it is the real file and the official compose runs
  `node src/scripts/health-check.js`. From v26.6.0 the build is copied flat into `/app`, the script is at
  `/app/scripts/`, and the Dockerfile adds `src/scripts` as a symlink to `../scripts` "for compatibility". Only the
  `src/` path works on every version that `create --version`, Restore and Rollback can bring back.
- No version or time in any file, so the same input always gives the same bytes and nothing is rewritten without
  a change.
- Instance containers: the shared account's IDs, `cap_drop: [ALL]`, `no-new-privileges`, no published ports, json-file
  logs of 10 MB × 3, no limits, `restart: unless-stopped`, password login only, the internal network `abyssctl`
  and, with Internet access, `abyssctl-external`, and the subnets of the networks it joins as the trusted proxies
  ([ADR 0005](../adr/0005-internet-access-per-instance.md)).
- The Proxy: a tool-owned Caddy with the Operator's Certificates and no ACME
  ([ADR 0001](../adr/0001-tool-owned-caddy-proxy.md)), running root inside a read-only container with only
  `NET_BIND_SERVICE`, its folder mounted whole so renames show.
- Timers, not cron: the journal keeps each run's output, and `Persistent=true` catches up a missed Backup.
- The MOTD script reads one pre-rendered file and checks its age, so a login is never slowed by Docker or the
  binary.

## 7. Later

- A read-only root filesystem for Instances (Docker `read_only: true`): the container can write only to its
  mounted `/data` and temp space, so a buggy or compromised server can't change its own program files. Blocked on
  checking whether Actual writes outside `/data`.
- Per-Instance Actual settings as an allow-list of environment keys (with Actual settings and OpenID).
- The HSTS option in site files (ROADMAP Later).

## 8. Docs it changes

- `docs/guides/security.md`: Instance containers run as the `abyssctl` account with every capability
  dropped and no published ports, on the internal-only network `abyssctl`, and on `abyssctl-external` only with
  Internet access; the Proxy runs read-only with only `NET_BIND_SERVICE`; no generated file holds
  a Secret.
- `docs/guides/alerts.md`: the MOTD shows "Health check not running" when `motd.txt` is older than three
  Health check intervals, and how to check the timer.
- `docs/guides/backup-and-restore.md`: the nightly timer starts the Backup within five minutes after
  `BACKUP_TIME` local time, and a Backup missed while the Host was off runs soon after it boots.
- `docs/guides/teardown.md`: the generated Host files to remove: the four units, the MOTD script and the
  logrotate rule.
- README "Updating Abyss": every generated file says it is generated, and a hand edit is overwritten.

## 9. Acceptance list

All tests are module tests in `internal/render`, run without root and without Docker. Golden files are in
`internal/render/testdata/*.golden`; `go test ./internal/render -update` rewrites them, and a changed golden file
is reviewed like code. Render is done when these module tests pass. The journey checks noted on some items are
built with the module that writes the file, as part of that module's journey slice.

1. `InstanceCompose` for the inputs in [Files and formats](#instance-composeyaml) gives exactly the golden file,
   one without and one with Internet access. → `TestInstanceCompose_Golden` (table: no Internet access, Internet
   access)
2. The rendered compose carries the label `io.abyssctl.instance: <name>` under `labels:`, the container name and
   network alias `abyssctl-<name>`, and the external network `abyssctl`, built from `internal/docker/names`;
   with Internet access it also joins the external network `abyssctl-external`, with no alias there, and without
   it the file never names `abyssctl-external`. →
   `TestInstanceCompose_Names`; journey 3 asserts the label on the real container (checked by lifecycle.md's journey slice)
3. The rendered compose has `user:` set to the given UID:GID, `cap_drop: [ALL]`, `no-new-privileges:true`, no
   `ports:`, no `env_file:` and the healthcheck path `/app/src/scripts/health-check.js`. →
   `TestInstanceCompose_Hardening`; journey 3 asserts the container user matches the `abyssctl` account, the
   dropped capabilities and no published ports (checked by lifecycle.md's journey slice)
4. Several subnets are written sorted (IPv4 first, then by address, then by prefix length), without duplicates,
   joined by commas, whatever order they come in. → `TestInstanceCompose_TrustedProxies` (table)
5. `Site` gives exactly the golden file, with the Certificate paths under `/etc/caddy/certs/` and the upstream
   `abyssctl-<name>:5006`. → `TestSite_Golden`; journey 4 reaches each Hostname through the Proxy (checked by lifecycle.md's journey slice)
6. `ParseSite` of the golden site file gives back its `SiteInput`, `ParseSite(Site(in))` equals `in` for each valid
   input, and a file with a changed header, server line or tls line is refused with an error wrapping `ErrInput`. →
   `TestParseSite_RoundTrip`, `TestParseSite_Rejects` (table)
7. `Caddyfile` gives exactly the golden file. → `TestCaddyfile_Golden`; journey 1 starts the Proxy with no site
   files (checked by host.md's journey slice)
8. `ProxyCompose` gives exactly the golden file: the `proxy/` folder read-only at `/etc/caddy`, `/data` and
   `/config` writable, a `/tmp` tmpfs, ports 80 and 443, `read_only`, only `NET_BIND_SERVICE`, and the external
   networks `abyssctl` and `abyssctl-external`. →
   `TestProxyCompose_Golden`; journey 3 asserts the Proxy runs `read_only` with only `NET_BIND_SERVICE` (checked
   by lifecycle.md's journey slice)
9. `Units` returns the four units sorted by name, each exactly its golden file, for the default schedule and for
   intervals of 1 minute ("every 1 minute"), 17 minutes and 60 minutes, and a `BackupTime` of `23:45`; the Backup
   timer always has `RandomizedDelaySec=5min`. → `TestUnits_Golden` (table); journey 1 runs `systemd-analyze verify`
   on the installed units (checked by host.md's journey slice)
10. No unit has `Environment=` or `EnvironmentFile=`, and each `ExecStart=` is the given binary path with
   `health check --scheduled` or `backup --all --scheduled`. → `TestUnits_ExecAndNoEnvironment`
11. `MotdScript` gives exactly the golden file, with the limit 3 × `HealthInterval` in seconds and the given path in
    single quotes. → `TestMotdScript_Golden` (table: 5 and 60 minutes); journey 1 runs the installed script before the
    first Health check and gets "Health check not running" (checked by host.md's journey slice), and journey 7 gets
    `motd.txt` unchanged after one (checked by health.md's journey slice)
12. `LogrotateRule` gives exactly the golden file. → `TestLogrotateRule_Golden`; journey 1 runs `logrotate -d` on
    the installed rule (checked by host.md's journey slice)
13. Every function refuses each bad value in the [input rules](#checking-input) with an error wrapping `ErrInput`
    that names the field, and `CheckSchedule` refuses the same schedules `Units` does. `InstanceCompose` and
    `ProxyCompose` accept an image with a registry prefix and port (`127.0.0.1:5000/actualbudget/actual-server:…`,
    as in the journeys' local registry) and refuse one with a tag that is not a plain `X.Y.Z`, no digest or an
    uppercase digest. → `TestRender_RejectsBadInput` (table), `TestCheckSchedule_MatchesUnits`
14. Render's copy of the plain-version rule gives the expected answer for every line of
    `internal/buildinfo/testdata/plain_versions.txt`, the table `buildinfo.PlainVersion`'s test runs. →
    `TestPlainVersion_AgreesWithBuildinfo`
15. Calling each function twice with the same input gives the same bytes, and no output contains the tool version
    or the current year. → `TestRender_Deterministic`
16. Every output starts with the generated-file header comment, naming the Instance in an Instance's files. →
    `TestRender_Header`
17. `internal/render`'s direct imports are only the standard library and `internal/docker/names`, and include none
    of `os`, `os/exec`, `io/fs`, `net`, `net/http` and `syscall`. → `TestImportRules` in `internal/archtest`
18. Docs: `security.md`, `alerts.md`, `backup-and-restore.md`, `teardown.md` and the README "Updating Abyss"
    section say what [Docs it changes](#8-docs-it-changes) lists. → (review)
