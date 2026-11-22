# Spec: testing

Build step 14 of 16. It is ticketed together with [foundation.md](foundation.md) (step 1) and grows as each
journey lands. The [overview](README.md) has the module map, the capability-to-journey table and the Host layout.

Each module spec lists its own module tests and its own slice of the journeys. This spec owns what those specs
share:

- the two test layers;
- the fakes and test helpers, and where they live;
- the temp-root rule;
- the journey harness;
- the eight journeys, each one complete and in order;
- the fake Actual image and the test image cache;
- the journey Makefile targets and the journey CI jobs.

The Release check is not here: it runs on the Operator's test VMs and is owned by [release.md](release.md).
Capitalised terms are defined in [GLOSSARY.md](../../GLOSSARY.md).

## 1. Purpose

Build the test harness that proves Abyss works on a real [Host](../../GLOSSARY.md), and write down the test rules
every module follows:

- **Module tests**: `go test` on each package through its interface, using fakes, temp paths and `httptest`
  servers. They run unprivileged on Linux or macOS.
- **Journey tests**: Go tests that run the real binary as root on a real Linux Host. They drive it through
  `os/exec` and check its output, exit codes, files on disk and `docker inspect`. They pull every image from a
  local registry filled from the test image cache.

This spec is done when:

- `make test` runs every module test;
- `make journey` runs the eight journeys and the two contract tests on a Linux Host with Docker;
- CI runs the journeys on Ubuntu amd64 and arm64 for every pull request to `main` and every push to `main`;
- the tag workflow ([release.md](release.md)) runs them again before it creates a draft
  [Release](../../GLOSSARY.md).

## 2. Interface

The harness is the only code this spec adds. It lives in package `journey`, in `_test.go` files with the `journey`
build tag. The journeys use these helpers:

```go
//go:build journey

package journey

// H is one journey's handle on the Host. Blank(t) first removes everything Abyss makes (see "A blank Host"),
// then checks that none of it is left, then returns a handle.
func Blank(t *testing.T) *H

// Run runs the staged build under test (bin/abyssctl) as root with no TTY, an empty stdin and the given
// arguments. It never runs /usr/local/bin/abyssctl, so a step always knows which build it ran. RunBin runs one
// staged binary by name ("abyssctl", "abyssctl-0.0.1"). RunStdin feeds stdin. RunAs runs the build under test as
// an unprivileged user (nobody), from a fresh root-owned 0700 folder that the user can't enter or write.
func (h *H) Run(t *testing.T, args ...string) Result
func (h *H) RunBin(t *testing.T, bin string, args ...string) Result
func (h *H) RunAs(t *testing.T, user string, args ...string) Result
func (h *H) RunStdin(t *testing.T, stdin string, args ...string) Result

type Result struct {
    Stdout, Stderr string
    Exit           int
    PID            int // the process ID, which the activity log lines carry
}

// SetUpHost runs `host setup --base-domain abyss.test --cert --key --yes` with a wildcard *.abyss.test Leaf from
// the journey's test CA, and returns the CA so later steps can trust it.
func (h *H) SetUpHost(t *testing.T) *certtest.CA

// Create runs `create <name> --version <v> --yes [--hostname <h>]` and waits until the Instance is healthy.
func (h *H) Create(t *testing.T, name, version, hostname string)

// HostListener listens on TCP on the Host's own first non-loopback IPv4 address, on a free port, for the rest of the
// test. A container with Internet access reaches it through its default route; one on the internal network only
// has no route to it. No packet leaves the Host, so the journeys stay offline.
func (h *H) HostListener(t *testing.T) (addr string)

// Reaches runs a 5-second TCP connect to addr from inside the container (`docker exec … node -e`, Actual's
// image having node) and reports whether it connected. Resolves runs a lookup of an external name inside the
// container and reports whether any answer came back.
func (h *H) Reaches(t *testing.T, container, addr string) bool
func (h *H) Resolves(t *testing.T, container, name string) bool

// Docker reads: `docker inspect` decoded, container IDs, image references.
func (h *H) Inspect(t *testing.T, container string) Container
func (h *H) ContainerID(t *testing.T, container string) string

// HTTPS fetches https://<hostname><path> through the Proxy on 127.0.0.1:443, with the hostname as SNI and the
// CA as the only root, the same as `curl --resolve <hostname>:443:127.0.0.1 --cacert <ca>`. It returns the
// status, the body and the leaf Certificate served.
func (h *H) HTTPS(t *testing.T, ca *certtest.CA, hostname, path string) Response

// HTTP fetches http://<hostname><path> from 127.0.0.1:80 with the hostname as Host, without following
// redirects, so a step can check the status and the Location header.
func (h *H) HTTP(t *testing.T, hostname, path string) Response

// Client is one Actual client (@actual-app/api) with its own data folder, run in the client container.
func (h *H) Client(t *testing.T, ca *certtest.CA, id, hostname string) *Client
func (c *Client) Do(t *testing.T, action string, args ...string) ClientResult // create, download, add, sync, list, revert, bootstrap

// Golden compares got, after Normalise(got, h.Values()), with journey/testdata/golden/<name>.golden.
func (h *H) Golden(t *testing.T, name, got string)

// Values are the run's own values that Normalise replaces.
func (h *H) Values() Values

// Mode checks a path's owner, group and mode.
func Mode(t *testing.T, path, owner, group string, mode fs.FileMode)
```

Two helpers have no build tag, so `make test` runs their tests as well:

- `journey/normalise.go` holds `Normalise(s string, v Values) string`, where `Values` carries the run's
  `JOURNEY_VERSION`, commit, Go version, platform, the Actual pin and the older, the Caddy pin, and the account's
  UID and GID;
- `journey/pins.go` holds `ReadPins(path string) (Pins, error)`.

`journey/fakeactual/` is the fake Actual image's `main` package. It imports only the standard library.

## 3. Behaviour

### The two test layers

**Module tests** are `go test` in each `internal/<module>` package. They call only the module's exported
interface, and fakes stand in at the real seams.

- **Paths:** every path comes from `hostfs.Under(t.TempDir())`, with a [Data root](../../GLOSSARY.md) inside it
  when the test needs one ([foundation.md](foundation.md)). No module test reads or writes a real Host path.
- **Network:** the registry, Pushover and the Proxy probe are reached through a base URL or address plus an
  `http.Client`, aimed at an `httptest` server: `RegistryURL`, `PushoverURL` and `ProbeAddr`. No module test
  goes on the network.
- **Time:** comes from `op.ManualClock`, and the zone is a fixed `*time.Location`.
- **Root:** tests run unprivileged. Owners and modes on disk are checked in journeys. A test that needs root (a
  `chown`) skips when it is not run as root.
- **Locks:** lock tests use real `flock` on files under `t.TempDir()`.
- **Running them:** `make test` runs `bin/gotestsum -- -race ./...` ([foundation.md](foundation.md)). Module tests
  use only portable standard-library calls, so they pass on Linux and macOS.
- **Coverage:** there is no coverage target. Each acceptance item names its test.

**CLI tests** are module tests of `internal/cli`. They call `cli.Run` with an `Env` built by `clitest.Env(t)`:

- temp Paths, a fake Terminal, an `op.ManualClock`, a fixed Zone, `Euid` 0, `Getenv` and `PID`;
- the fakes the verb needs, set in `Seams`.

The rule: every verb's panel, prompts, `--dry-run`, `--json`, locks, exit codes and activity log lines are tested
this way. Golden files are kept in `internal/cli/testdata/` and `internal/cli/out/testdata/` ([cli.md](cli.md)).
The activity log is tested here too, against a temp log path.

**Smoke tests** (`cmd/abyssctl/main_test.go`, [foundation.md](foundation.md)) build the binary, run it as the
unprivileged test user and check it from outside. The root-related ones skip when run as root, and journey 8
checks the same refusals as root.

**Journey tests** run the real binary as root on a real Host. Each module spec's acceptance list names its journey
items, and the journeys below put all of them in order. The capabilities the journeys cover are in the overview's
[capability table](README.md#capabilities-modules-and-journeys).

**Contract tests** keep the fakes honest. Each runs a single suite against both the fake and the real system:

- `TestDockerContract`: `dockertest.Contract` against the real Docker CLI ([docker-and-locks.md](docker-and-locks.md)).
- `TestRegistryContract`: Versions against the local `registry:2` ([versions.md](versions.md)).

Both run in the journey binary, before the journeys.

**`TestJourneyClientCache`** checks the `journey-client` cache round trip (start-up step 7) without touching
`testdata/cache/`. It starts a second `registry:2` on a temp copy of the registry storage with the client tag
deleted and runs step 7 against it: the log says `journey_client=built`, and the re-packed temp `registry.tar`
holds the tag. It then starts that registry again from the re-packed tar and runs step 7 once more: the log says
`journey_client=cached`, and no `npm ci` runs.

**`TestHarnessBlank`** checks `Blank` itself, before any journey relies on it. It seeds one of each item in
[A blank Host](#a-blank-host): a labelled container, the `abyssctl-proxy` container, an `abyssctl-*` Compose
project, both networks, an image of each stamped repo, the folders and files, the four units (enabled), the
binary, and the `abyssctl` user and group. It then calls `Blank` and checks that every seeded item is gone, and
that Docker, the local registry, `journey-client` and the `node` image are still there.

### Fakes and test helpers

| Package | Stands in for | Used by | Owner |
|---|---|---|---|
| `internal/docker/dockerfake` | the Docker CLI adapter: in memory, records calls, `Fail` / `FailWhen`, `SetImageHealth`, `SetExec`, `SetRun`, `SetLogs`, `AddRemote` | every module that calls Docker, CLI tests | [docker-and-locks.md](docker-and-locks.md) |
| `internal/docker/dockertest` | the contract suite: `Contract(t, c, Env)` | `TestContract_Fake`, `TestDockerContract` | [docker-and-locks.md](docker-and-locks.md) |
| `internal/docker/testdata/<engine>-<compose>/` | captured Docker CLI output, replayed by a fake command runner; refreshed by `capture.yml` or `make capture` | the adapter's own tests | [docker-and-locks.md](docker-and-locks.md) |
| `internal/host/hostfake` | `host.System`: scripted OS, listeners, accounts, apt sources, file metadata; records commands | Host, CLI tests | [host.md](host.md) |
| `internal/proxy/certtest` | a throwaway CA: `NewCA(t)`, `(*CA).Leaf(t, LeafOptions{…})`, PEM files in `t.TempDir()` | Proxy, CLI tests, journeys 2–8 | [proxy.md](proxy.md) |
| `internal/versions/registrytest` | the registry's Distribution API, an `httptest` server with `Push` and auth modes | Versions, Upgrade, Health, CLI tests | [versions.md](versions.md) |
| `internal/alerts/alertsfake` | the Alert `Sender`: `Sent`, `FailNext`, `FailAll` | Health | [alerts.md](alerts.md) |
| `internal/alerts/pushovertest` | Pushover's message API: `New`, `Listen(t, JourneyAddr)`, `RejectUser`, `ReplyNext`, `EchoCredentials`, `Delay` | Alerts, CLI tests, journey 7 | [alerts.md](alerts.md) |
| `internal/cli/clitest` | `Env(t)` and a fake `Terminal` | CLI tests | [cli.md](cli.md) |
| `op.Recorder`, `op.ManualClock` | the event sink and the clock | every module | [foundation.md](foundation.md) |
| `internal/archtest` | `TestImportRules`, with the import rules each spec adds, and `TestLintForbids` | `make test` | [foundation.md](foundation.md) |
| fake `Proxy`, `Backup`, `HealthReader`, `FreeSpace`, `Account`, `DiskFree` seams | small fakes of a module's seam interface | the module whose interface takes them | that module's spec |

A seam fake that only one module uses lives in that module's `_test.go` files. A fake that two or more modules
use gets its own package, as above. Non-test code never imports any of these packages. The journey harness is the
one exception: it imports `dockertest`, `certtest` and `pushovertest`.

### The journey harness

**How a run works.** `make journey` builds three binaries and copies them, with the test image cache, to the
target. It then runs the test binary there as root, with `-journey.wipe`, `-test.v` and `-test.timeout 60m`:

- the journey test binary, built with `go test -c -tags journey`;
- the build under test, `abyssctl`;
- a second build of the same commit stamped `0.0.1`, `abyssctl-0.0.1`.

**The stamps.** All three are `CGO_ENABLED=0` static builds for the target's architecture. Both `abyssctl` builds
pass foundation's `make build` these `LDFLAGS_EXTRA` values:

| Variable | Journey value |
|---|---|
| `ActualRepo` | `127.0.0.1:5000/actualbudget/actual-server` |
| `CaddyRepo` | `127.0.0.1:5000/library/caddy` |
| `RegistryURL` | `http://127.0.0.1:5000` |
| `PushoverURL` | `http://127.0.0.1:18089` (`pushovertest.JourneyAddr`) |

**Its version.** The build under test is stamped with `JOURNEY_VERSION`:

- on a tag run, the tag's version;
- otherwise `0.1.0-journey`.

Either way it is newer than `0.0.1`.

Docker treats `127.0.0.0/8` registries as insecure by default, so the local registry needs no daemon setting.
Docker documents this default as one that may change. If it does, the first pull in `TestMain` fails, and the
harness stops with "Docker no longer trusts 127.0.0.1:5000 over plain HTTP" and the pull's error.

**The work folder** is `/var/tmp/abyssctl-journey/` on the target:

- `bin/` holds the three binaries;
- `cache/` holds the test image cache;
- `out/` holds what a failing run leaves behind.

**Safety.** The test binary refuses to start (it fails `TestMain`) without `-journey.wipe`, because every journey
deletes the Host's Abyss state. `make journey` passes the flag. Run it only on a runner or a throwaway VM.

**Start-up (`TestMain`)**, once per run, before any test:

1. Check that `docker` and `docker compose` meet `buildinfo.Pins`' minimums. Otherwise fail with "the journeys
   need Docker on the target".
2. `docker load` `registry:2` from `cache/registry-image-<arch>.tar`, the file for the target's architecture.
3. Unpack `cache/registry.tar` into `/var/tmp/abyssctl-journey/registry/`.
4. Start `registry:2` as the container `journey-registry`, published on `127.0.0.1:5000`, with that folder as
   `/var/lib/registry`.
5. Check that every pin in both pin files is served at its digest. Otherwise fail with "test image cache out of
   date: run make image-cache". The first `docker pull` from the registry is part of this check (see the note on
   insecure registries above).
6. Build the fake Actual image once per fake tag (below). Push each tag to
   `127.0.0.1:5000/actualbudget/actual-server:<tag>`, and the healthy one also to
   `127.0.0.1:5000/abyssctl-contract/pullable:1`. Then remove the local tags.
7. Get the client image `journey-client` from `journey/client/`, `FROM 127.0.0.1:5000/library/node@<node
   digest>`. Its `package-lock.json` pins `@actual-app/api` to the journey pin's version. The image is
   `127.0.0.1:5000/abyssctl/journey-client:<hash>-<arch>`, where `<hash>` is the first 12 hex characters of the
   lock file's SHA-256. When the local registry serves that tag, the harness pulls it. When it doesn't, the
   harness builds the image (its `npm ci` downloads the client's packages from the npm registry), pushes it, and
   packs the registry folder again into `cache/registry.tar`, so the test image cache keeps it. It logs
   `journey_client=cached` or `journey_client=built`.

   **Nothing outside the local registry once the cache is warm.** A cold client build's `npm ci` is the only
   fetch a journey run can make outside the local registry, and it runs once per lock file and architecture.
   Every image comes from the local registry, and nothing comes from Docker Hub.

**Order.** The tests run in this order:

1. `TestDockerContract`
2. `TestRegistryContract`
3. `TestJourneyClientCache`
4. `TestHarnessBlank`
5. `TestJourney1Bootstrap` to `TestJourney8OutputContract`
6. `TestNoHubPulls`

`-test.run` picks one or a few. A failed journey doesn't stop the run, because each journey starts from a blank
Host.

**`TestDockerContract`** pulls `99.0.2` and `99.0.1` from the local registry. It then calls `dockertest.Contract`
against the real CLI adapter with:

- `Healthy` = `…/actual-server:99.0.2`;
- `Unhealthy` = `…/actual-server:99.0.1`;
- `Pullable` = `127.0.0.1:5000/abyssctl-contract/pullable:1`;
- `Exec` = `["node", "--version"]`;
- `Dir` = `t.TempDir()`.

**`TestNoHubPulls`** checks that every image on the Host is one of these:

- under `127.0.0.1:5000/`, which includes `127.0.0.1:5000/library/node` and `journey-client`;
- `registry:2`, loaded from the cache.

**When a journey fails**, the harness saves these into `out/<journey>/`, and the CI job uploads `out/` as an
artifact:

- `docker ps -a`;
- each Abyss container's logs;
- the activity log;
- `state/`;
- a listing of the Data root with owners and modes.

[Secrets](../../GLOSSARY.md) never go there. `abyssctl.env` is listed but not copied, and so are Certificate keys.

**General rules for every journey:**

- **Shared values.** The Base domain is `abyss.test`, and the Data root is the default `/srv/abyssctl`. "The pin"
  is the journey Actual pin, and "the older" is the Actual version before it.
- **Prompts.** No step has a TTY. Every changing command passes `--yes`, unless the step checks the refusal
  without it.
- **`create`.** Every `create` passes `--version`, because the local registry's latest Actual is `99.0.2`.
- **`upgrade`.** Every `upgrade` names its target version as the positional argument.
- **Certificates.** Every journey after the first makes its own test CA with `certtest.NewCA` and calls
  `SetUpHost`, unless the step says otherwise. Hostnames are reached with `h.HTTPS`, which is the same check as
  `curl --resolve <hostname>:443:127.0.0.1 --cacert <test CA>`.
- **Timers.** A journey that is not about timers stops `abyssctl-health.timer` and `abyssctl-backup.timer` right
  after `SetUpHost`. The health timer's `OnActiveSec=1min` would otherwise race the steps.

### A blank Host

`Blank(t)` makes the Host look as if Abyss had never run. Docker, the local registry, `journey-client`, the `node`
image stay. It removes:

- every container labelled `io.abyssctl.instance`, `abyssctl-proxy` and every `abyssctl-*` Compose project;
- the `abyssctl` and `abyssctl-external` networks;
- every local image of the two stamped repos;
- `/etc/abyssctl/`, the Data root, `/var/log/abyssctl/`, the four units (stopped and disabled first, then
  `daemon-reload`), `/etc/update-motd.d/90-abyssctl`, `/etc/logrotate.d/abyssctl` and
  `/usr/local/bin/abyssctl`;
- the `abyssctl` user and group.

It then checks that none of these is left, and fails the journey if one is. Nothing else stays running
between journeys: journey 7 starts its own `pushovertest` server.

### The fake Actual image

The fake Actual image is used for the forced Rollbacks and the contract tests. It is a small static Go HTTP server
(`journey/fakeactual`), built for the target's architecture and embedded in the journey binary with its
Dockerfile. `TestMain` builds it on the target with `docker build`:

- the image is `FROM scratch`, with the binary at `/app/server`;
- the same binary is copied to `/usr/local/bin/node`;
- the stub script is an empty file at `/app/src/scripts/health-check.js`;
- the Dockerfile sets `ENV PATH=/usr/local/bin`, because a `scratch` image has no default `PATH`, so the
  healthcheck finds `node`;
- the server listens on 5006;
- it never touches `/data`.

So the Instance compose's healthcheck `["CMD", "node", "/app/src/scripts/health-check.js"]`
([render.md](render.md)) runs the fake's own health mode.

Each failure behaviour is baked in at build time as `--build-arg MODE=…` and pushed under the Actual repo name as
its own tag:

| Tag | Mode | `/health` | Docker healthcheck | `/info` |
|---|---|---|---|---|
| `99.0.1` | `unhealthy` | 503 `{"status":"DOWN"}` | fails | 503 |
| `99.0.2` | `wrong-info` | 200 `{"status":"UP"}` | passes | 200 `{"build":{"version":"0.0.0-fake"}}` |

The `node` stub works the same in both modes:

- run as `node /app/src/scripts/health-check.js`, it GETs `http://127.0.0.1:5006/health` and exits 0 only on a 200
  with `"status":"UP"`;
- run as `node --version`, it prints `v0.0.0-fake` and exits 0;
- any other `node` command exits 0.

`99.0.2` is the highest Actual tag in the local registry. So it is the latest stable Actual that Versions finds
in every journey, and the version the MOTD and `status` offer as an update.

### The test image cache

`make image-cache` uses `crane` to copy every pinned image into a local registry's storage, keeping its digest:
it copies each multi-arch index and every platform under it. The images are:

- the pin and the older Actual, from `journey/testdata/pins.json`;
- the compiled Caddy pin, from `internal/buildinfo/pins.json`, and the older Caddy, from
  `journey/testdata/pins.json`;
- `node`, from `journey/testdata/pins.json`.

The storage is packed as `testdata/cache/registry.tar`. `registry:2` itself can't be served from that registry,
so it is saved beside it once per architecture. For each of `linux/amd64` and `linux/arm64`,
`crane pull --platform linux/<arch>` fetches it by the digest in `journey/testdata/pins.json`, tagged `registry:2`,
into `testdata/cache/registry-image-<arch>.tar`. `docker load` keeps that tag.

All these files are git-ignored. The work is done by `journey/image-cache.sh`, which `make image-cache` runs. A
cache key file `testdata/cache/key` holds the SHA-256 of the two pin files and that script together.
`make journey` refuses with "run make image-cache" when the key doesn't match. `make image-cache` doesn't build
`journey-client`: the first journey run on each architecture does, and adds it to `registry.tar` (start-up step
7). Locally, a lock file change needs no new cache, only one client build. The CI cache key also hashes the lock
file, because a saved cache entry can't be overwritten: a new key is the only way the saved cache gets the new client.

The images go into the cache's registry under the stamped names: `actualbudget/actual-server`, `library/caddy`
and `library/node`. A journey run pulls every image from the local registry, so it makes no Docker Hub pulls.
Images are never kept in VM snapshots: a fresh VM has no Docker, and `docker load` would lose the digest the tool
pins.

Moving a journey pin is a commit of its own. It changes `journey/testdata/pins.json`, `journey/client/package.json`
and its lock file, and any golden files that show the version. `TestPins_Valid` checks that these agree.

### Journey 1: Bootstrap

Owners: [host.md](host.md) 23–26, [proxy.md](proxy.md) 30, [render.md](render.md) 7, 9, 11 and 12, and
[foundation.md](foundation.md) 24.

1. `Blank`. Then `host setup --base-domain abyss.test --yes` exits 0. The Proxy is running and ready, with no file
   in `proxy/sites/`. An HTTPS request to `127.0.0.1:443` fails, either because the connection is closed or
   because the handshake fails. Both timers are enabled and active. The activity log holds the run's lines,
   starting with its `begin`. `docker network inspect` shows `abyssctl` as bridge and internal and
   `abyssctl-external` as bridge and not internal, and the Proxy on both.
2. Straight away: `systemctl stop` and `systemctl disable` `abyssctl-health.timer`. There is no `state/motd.txt`,
   and `/etc/update-motd.d/90-abyssctl` prints "Health check not running".
3. Every path in the overview's Host layout that exists now has the layout's owner and mode, which shows that the
   tool's `umask 077` holds. `abyssctl` is a system account: UID and GID below `UID_MIN`, a `nologin` shell and no
   home.
4. `systemd-analyze verify` passes on the four installed units, and `logrotate -d /etc/logrotate.d/abyssctl` passes.
5. Break a mode by hand (`chmod 0644 /etc/abyssctl/abyssctl.env`) and an owner (`chown nobody
   <data root>/proxy/Caddyfile`).
   - `host setup --dry-run` prints the panel, exits 0 and changes nothing.
   - `host setup` without `--yes` exits 2 and changes nothing.
6. `host setup --yes` fixes both and logs one `fixed` line for each. It also enables and starts the health timer
   again.
7. `host setup --yes` again exits 0 with "Nothing to fix".

### Journey 2: Tool update

Owners: [host.md](host.md) 27 and 28, and [lifecycle.md](lifecycle.md) 40.

1. `Blank`. Run the staged `abyssctl-0.0.1` for `host setup --base-domain abyss.test --cert … --key … --yes`
   and `create kids --version <pin> --yes`. Note the IDs of `abyssctl-kids` and `abyssctl-proxy`.
2. `h.Run` (the staged build under test) runs `host setup --yes`, which exits 0. Afterwards:
   - `/usr/local/bin/abyssctl` is the build under test: `version --json` shows `JOURNEY_VERSION`;
   - the four units hold the bytes the build renders;
   - the Proxy and `kids` containers keep their IDs.
3. `status --json` flags `kids`'s files as out of date. Then `restart kids --yes` clears the flag, and
   `TOOL_VERSION` is `JOURNEY_VERSION`.
4. `abyssctl-0.0.1 host setup --yes` exits 2, and `/usr/local/bin/abyssctl` is unchanged.
5. `abyssctl-0.0.1 stop kids --yes` exits 4 with the hint `install abyssctl <v> or newer`. `kids` keeps running,
   and its record is unchanged.

### Journey 3: Instance life

Owners:

- [lifecycle.md](lifecycle.md) 34–39 and 50;
- [store.md](store.md) 15 and 28;
- [render.md](render.md) 2, 3 and 8;
- [proxy.md](proxy.md) 31;
- [host.md](host.md) 29.

1. `Blank`, `SetUpHost`. `create kids --version <pin>` and `create bob --version <pin> --hostname
   money.abyss.test --internet` exit 0. `list --json` and `status --json` show both running, healthy and wanted running.
2. `docker inspect` of `abyssctl-kids` and `abyssctl-bob` shows each container:
   - with the label `io.abyssctl.instance=<name>`;
   - with `User` set to the UID:GID that `id abyssctl` reports;
   - with `CapDrop` set to `ALL` and `no-new-privileges`;
   - with no port bindings;
   - on the networks its record says: `kids` on `abyssctl` only, `bob` on `abyssctl` and `abyssctl-external`.

   `abyssctl-proxy` has `ReadonlyRootfs` true, `CapDrop` set to `ALL` and `CapAdd` set to `NET_BIND_SERVICE` only.
3. `instances/kids/` holds `instance.env`, `compose.yaml`, `.lock` and `data/` at the layout's owners and modes.
   `data/` is owned by the same UID:GID the container runs as.
4. Run the Instance verbs:
   - `stop kids --yes`: the container is exited and wanted stopped;
   - `start kids`: it is healthy again;
   - `restart kids --yes`: it has a later start time;
   - `logs kids --tail 5` prints at most five lines;
   - `exec kids -- id -u` prints the account's UID;
   - `exec kids -- sh -c 'exit 7'` exits 7.
5. [Internet access](../../GLOSSARY.md), offline: `addr := h.HostListener(t)`. `h.Reaches(abyssctl-bob, addr)` is
   true; `h.Reaches(abyssctl-kids, addr)` is false, and `h.Resolves(abyssctl-kids, "example.com")` is false.
   Then `set kids --internet=on --yes` and `set bob --internet=off --yes` exit 0: `kids` now reaches `addr`,
   `bob` does not, and `h.Resolves(abyssctl-bob, "example.com")` is false (only the no-answer side is checked,
   because the run is offline); `list
   --json` shows `kids` with `"internet": true` and `bob` with `"internet": false`.
6. `chown root:root` one file in `kids`'s `data/`. Then `host setup --yes` gives it back to `abyssctl:abyssctl`,
   with its mode unchanged.
7. `remove kids --yes` exits 0. Afterwards there is no `instances/kids/`, no container and no
   `proxy/sites/kids.caddy`. Under `backups/removed/kids-<stamp>/` there is a `final` Backup and its `.sha256`,
   and `sha256sum -c` passes.
8. `docker run -d --label io.abyssctl.instance=ghost --name ghost <pin image>`. `status` shows `ghost` as an
   [Orphan](../../GLOSSARY.md). Then `remove --orphan ghost --yes` removes the container.

### Journey 4: Proxy and Certificate

Owners:

- [lifecycle.md](lifecycle.md) 41 and 42;
- [proxy.md](proxy.md) 32–35;
- [host.md](host.md) 30;
- [render.md](render.md) 5;
- [versions.md](versions.md) 17.

1. `Blank`. Make a test CA and a wildcard `*.abyss.test`. Then `host setup --base-domain abyss.test --cert
   wild.crt --key wild.key --yes` exits 0.
2. Seed the older Caddy: write `proxy/proxy.env` with the older Caddy's version and digest (root:root 0600), then
   run `host setup --yes`. The Proxy now runs the older Caddy image.
3. `create kids --version <pin>` and `create bob --version <pin> --hostname money.abyss.test`.
   - `h.HTTPS` to `kids.abyss.test` and to `money.abyss.test` at `/health` each answers 200 with `"status":"UP"`.
   - A Hostname outside every Certificate (`x.nowhere.test`) fails the handshake.
   - A covered Hostname with no site (`nosite.abyss.test`) gets an empty 200.
   - `h.HTTP` to `kids.abyss.test` on port 80 gets a redirect to `https://kids.abyss.test/`.
4. `cert install` of a Leaf for `other.example.test` exits 0 with the warning "covers no Instance yet".
5. `create ghost --version <pin> --hostname ghost.nowhere.test` exits 2. It leaves no folder, no container and no
   site file.
6. `cert list` shows the wildcard used by `kids` and `bob`, and the other Certificate unused.
   - `cert remove <wildcard id> --yes` is refused (exit 2).
   - `cert remove <other id> --yes` exits 0 and deletes `proxy/certs/<other id>.{crt,key}`.
7. `cert install` of a renewed wildcard `*.abyss.test`, with a later `NotBefore`, replaces the old one. Its files
   are gone, and `kids.abyss.test` and `money.abyss.test` both serve the new Leaf.
8. Proxy upgrade:
   - `proxy upgrade --dry-run` prints the panel and changes nothing, and the Proxy keeps its container ID.
   - `proxy upgrade --yes` moves the Proxy to the compiled Caddy pin, and both Hostnames answer `/health`
     afterwards.
   - `image prune --yes` removes the older Caddy image and keeps the pin's and the Actual pin's.
   - A second `proxy upgrade --yes` exits 0 as a no-op.
   - `proxy upgrade <older caddy> --yes` is refused (exit 2).

### Journey 5: Backup and Restore

Owners: [backup.md](backup.md) 32–34. This journey also runs the scheduled Backup service once. It is the end-to-end
form of the Restore and [Regroup](../../GLOSSARY.md) check in [ADR 0004](../adr/0004-restore-regroups-offline.md).

1. `Blank`, `SetUpHost`, then `create kids --version <pin>` and `create bob --version <pin> --hostname
   money.abyss.test`. Client A runs `bootstrap` with the journey password.
2. Client A creates a budget and adds the account `BeforeBackup`. Client B downloads it.
3. Take Backups:
   - `backup kids` and `backup --all` exit 0;
   - `backup list --json` shows two `manual` Backups of `kids` and one of `bob`;
   - each archive and sidecar under `backups/<name>/` is root:root 0600;
   - `sha256sum -c` passes on each sidecar.
4. Client A adds `AfterBackup`. Client B syncs and sees it.
5. `restore kids <first manual id> --yes` exits 0. Afterwards there is a `pre-restore` Backup of `kids`, and no
   `data.pre-restore/` is left.
6. Check the clients:
   - client A's `sync` and client B's `sync` each get `file-has-reset`;
   - client A's `add Stale` is refused with the same code;
   - client A's `list` shows the server's files with their new `groupId`, and a new client C's `download` of that
     group id holds only `BeforeBackup`;
   - clients A and B `revert`, then client A adds `AfterRestore`, and clients B and C sync;
   - clients A, B and C each `list` `BeforeBackup` and `AfterRestore`, and none of them has `AfterBackup`, so no
     newer change is applied again.
7. The harness takes `bob`'s Instance lock (`flock` on `instances/bob/.lock`) and runs `systemctl start
   abyssctl-backup.service`. Then:
   - the service ends successfully;
   - `kids` has a new `scheduled` Backup, and `outcomes.json` records it `ok`;
   - `bob` has none;
   - the activity log has a `skipped instance=bob reason=busy` line.

   The harness then releases the lock.

### Journey 6: Upgrade and Rollback

Owners: [upgrade.md](upgrade.md) 20, [backup.md](backup.md) 35 and [versions.md](versions.md) 18.

1. `Blank`, `SetUpHost`, then `create kids --version <older>`. Client A runs `bootstrap`, then creates a budget,
   so `kids`'s `data/` holds a group file.
2. `upgrade kids <pin> --yes` exits 0. The record's `ACTUAL_VERSION` and `/info`'s `build.version` (through
   `h.HTTPS`) are the pin.
3. `upgrade kids 99.0.1 --timeout 30s --yes` exits 3. The fake never becomes healthy. Afterwards:
   - `kids` is on the pin again, healthy;
   - every `UPGRADE_*` key is cleared;
   - `upgrade-failed-<stamp>.log` exists;
   - `outcomes.json` records `rolled_back`;
   - the group file names in `data/` are the same as before the Upgrade (no Regroup).
4. `upgrade kids 99.0.2 --yes` exits 3. The fake is healthy but fails `/info`. Afterwards `kids` is on the pin
   again, its group file has a new name (Regroup done), and client A's `sync` gets `file-has-reset`.
5. `restore kids <id> --yes`, where `<id>` is the `pre-upgrade` Backup whose `actual_version` in `backup list --json`
   is the older. Afterwards the record and the container's image are the older.
6. `backup prune kids --keep 1 --yes` leaves exactly one Backup of `kids`.
7. `image prune --yes` removes the Actual images no Instance uses, which are the pin's and the two fake tags'. It
   keeps the older (used by `kids`) and the Proxy's image, and leaves every image of another repo.

### Journey 7: Health and Alerts

Owners: [alerts.md](alerts.md) 14 and 15, [health.md](health.md) 32–34 and [render.md](render.md) 11.

1. `Blank`, `pushovertest.Listen(t, pushovertest.JourneyAddr)`, `SetUpHost`, `create kids --version <pin>`. Then
   `systemctl stop abyssctl-health.timer`. From here on,
   "a run" means `systemctl start abyssctl-health.service`, which returns once the check has finished.
2. Alerts set-up:
   - `alerts test` exits 2, because no Pushover keys are set;
   - with `pushovertest` set to `RejectUser(<bad user>)`, `alerts setup --pushover-user <bad user>
     --pushover-token -` (token on stdin) exits 2, and `abyssctl.env` has no Pushover keys;
   - `alerts setup --pushover-user <user> --pushover-token -` (token on stdin) exits 0, and the server got one test
     message at priority 0;
   - `abyssctl.env` holds both keys and is root:root 0600;
   - `alerts test` exits 0, and the server gets a second message;
   - the token appears nowhere in the activity log.
3. `health check` exits 0. The MOTD shows the all-healthy header and `↑ kids <pin> → 99.0.2 available`.
4. `docker stop abyssctl-kids`.
   - The first run sends nothing.
   - The second sends the Instance-down Alert to `pushovertest` at priority 0, and the MOTD shows `! kids down`.
   - `health check` by hand exits 1.
5. `docker start abyssctl-kids` and wait until it is healthy. The next run sends the recovery at priority -1, and
   the MOTD shows the all-healthy header again.
6. One more run leaves `motd.txt`'s bytes unchanged with a newer mtime. Then `alerts test` leaves `health.json`'s
   bytes unchanged.
7. Not delivered:
   - the harness writes `instances/kids/outcomes.json` with a failed scheduled Backup, in
     [store.md](store.md)'s format, and sets `pushovertest` to `ReplyNext(500, …)`;
   - the next run raises `backup_failed`, its Started Alert is not delivered, and the MOTD shows "Alerts not
     delivered since";
   - the run after delivers it, and the line goes.
8. `health clear kids` exits 0, and the MOTD no longer shows `backup_failed`. `health clear nosuch` exits 2.

### Journey 8: Output contract

Owners: [cli.md](cli.md) 35–37 and [foundation.md](foundation.md) 4.

1. `Blank`, `SetUpHost`, `create kids --version <pin>`, then `backup kids`. The harness `docker pull`s the older
   Actual from the local registry, so `image prune` has an unused image to show.
2. These outputs match their golden files, after normalising (below):
   - the panels and outcome lines of `stop kids --dry-run`, `image prune --dry-run` (which lists the older Actual
     image) and `remove kids --dry-run`;
   - the output of `status`;
   - the `--json` documents of `version`, `list`, `status`, `backup list` and `health check`.
3. Without `--yes`, with no TTY, `stop kids`, `remove kids` and `restore kids <id>` each exit 2 and change nothing.
   `kids` keeps running, its folder and record are unchanged, and no Backup is added.
4. With `--yes`, `stop kids`, `restore kids <id>` and `remove kids` exit 0, in that order.
5. Run with `h.RunAs("nobody", …)`:
   - `status` exits 2, with "needs root. Run it with sudo." on stderr;
   - `host setup` exits 2;
   - the activity log is unchanged.
6. `/var/log/abyssctl/` is root:adm 0750, and `abyssctl.log` is root:adm 0640. For each changing run in this
   journey, matched by its `Result.PID`, the log holds a `begin` line and an `end` line. The `end` line's `outcome` equals
   the outcome line the run printed.

A typed-name mismatch needs a terminal, so `TestConfirmName` in [cli.md](cli.md) checks it. The Release check's
manual steps check it again on a real terminal.

### Makefile targets

[foundation.md](foundation.md) owns `tools`, `lint`, `test` and `build`. This spec adds:

- **`crane`** in `make tools` (`CRANE_VERSION`, installed into `./bin/`).
- **`journey` in the lint and vet build tags.** `make lint` runs golangci-lint with `--build-tags journey`, so the
  harness is linted and compiled on every push.
- **`make image-cache`** builds the test image cache, as above. With `DOCKERHUB_USER` and `DOCKERHUB_TOKEN` set, it
  logs `crane` in read-only first. Otherwise it pulls anonymously.
- **`make journey [HOST=<ssh host>] [ARCH=amd64|arm64] [RUN=<regexp>] [UPDATE=1] [JOURNEY_BIN=<dir>]`:**
  - It builds the three binaries for `ARCH`. With `JOURNEY_BIN`, it builds nothing and stages the three binaries
    from that folder instead (the journey test binary, `abyssctl` and `abyssctl-0.0.1`); it still stages the test
    image cache. The default is the target's own architecture: `uname -m` over ssh with
    `HOST`, the local one without it.
  - Without `HOST` it runs on this machine with `sudo`, as on the CI runners. With `HOST` it copies
    `/var/tmp/abyssctl-journey/` there over `ssh` and runs it there with `sudo`, then copies `out/` back to
    `dist/journey-out/`.
  - When the harness packed `cache/registry.tar` again, `make journey` copies it back into `testdata/cache/`
    (over `ssh` with `HOST`), so the next run starts warm.
  - `RUN` passes `-test.run`.
  - `UPDATE=1` passes `-journey.update`: the harness writes the normalised output to `out/golden/`, and `make
    journey` copies it into `journey/testdata/golden/`. A changed golden file is reviewed like code.

[docker-and-locks.md](docker-and-locks.md) adds `make capture`, which refreshes the Docker fixtures.

`make release-check` belongs to [release.md](release.md). It runs `make journey HOST=… JOURNEY_BIN=…` on each test
VM, with the tag workflow's own journey binaries.

### CI

Foundation's `ci.yml` job (lint, test, build) runs on every push and pull request. This spec adds the journeys
as a reusable workflow, `.github/workflows/journeys.yml` (`on: workflow_call`, inputs `version` and `keep-binaries`):

- **Matrix.** It runs on `ubuntu-24.04` and `ubuntu-24.04-arm`, which come with Docker Engine and Compose, so
  this is the "Docker already there" path on both architectures.
- **Image cache.** Each job restores `testdata/cache/` with `actions/cache/restore`, keyed
  `journey-images-<arch>-<hash of both pin files, journey/image-cache.sh and journey/client/package-lock.json>`.
  A miss runs `make image-cache`, with the optional read-only `DOCKERHUB_TOKEN` secret, before the run, and
  `actions/cache/save` saves the cache after the run, so the saved cache holds that architecture's
  `journey-client`. Each architecture has its own key, because the client image is built for one architecture.
- **Run.** It runs `make tools` and then `make journey JOURNEY_VERSION=<version>`.
- **Artifacts.** It uploads the JUnit report, and `dist/journey-out/` when the run fails. With
  `keep-binaries: true` (default `false`), each job also uploads its three journey binaries as the artifact
  `journey-bin-<arch>`, with `retention-days: 90`.
- **Secrets.** Every caller passes `secrets: inherit`, so a cache miss can use `DOCKERHUB_TOKEN`.
- **Actions** are pinned to commit SHAs, as in foundation.

`ci.yml` calls `journeys.yml` after its own job passes, for pull requests to `main` and pushes to `main`.

The tag workflow calls `journeys.yml` on the tagged commit, with the tag's version and `keep-binaries: true`.
[release.md](release.md) owns that workflow.

`.github/workflows/capture.yml`, run by hand, captures the Docker fixtures on Ubuntu runners and uploads them as
an artifact; [docker-and-locks.md](docker-and-locks.md) owns it.

## 4. Files and formats it owns

```
journey/
  harness_test.go       TestMain, H, Blank, Run, Golden, Mode, the failure collector  (build tag journey)
  contract_test.go      TestDockerContract, TestRegistryContract                       (build tag journey)
  client_cache_test.go  TestJourneyClientCache                                          (build tag journey)
  blank_test.go         TestHarnessBlank                                                (build tag journey)
  j1_bootstrap_test.go … j8_output_test.go   one file per journey                       (build tag journey)
  normalise.go, normalise_test.go            Normalise, TestNormalise                    (no tag)
  pins.go, pins_test.go                      ReadPins, TestPins_Valid                    (no tag)
  image-cache.sh        builds testdata/cache/, run by make image-cache
  fakeactual/           main.go, main_test.go (TestFakeActual), Dockerfile
  client/               client.js, package.json, package-lock.json, Dockerfile (FROM the node pin)
  testdata/pins.json
  testdata/golden/*.golden
testdata/cache/         registry.tar, registry-image-{amd64,arm64}.tar, key   (git-ignored)
.github/workflows/journeys.yml
```

`.gitignore` gets `testdata/cache/` and `dist/journey-out/`.

**`journey/testdata/pins.json`** is the journeys' image list:

```json
{
  "actual":       { "version": "26.11.0", "digest": "sha256:…" },
  "actual_older": { "version": "26.10.1", "digest": "sha256:…" },
  "caddy_older":  { "version": "2.10.0",  "digest": "sha256:…" },
  "node":         { "version": "24.11.1", "digest": "sha256:…" },
  "registry":     { "version": "2.8.3",   "digest": "sha256:…" }
}
```

The values above are examples. The file must follow these rules:

- All five keys are required, and unknown keys are an error.
- Every version is plain `X.Y.Z`, and every digest is `sha256:<64 hex>` of a multi-arch index.
- `actual_older` is the stable Actual release just before `actual`, and both are older than `99.0.0`.
- `caddy_older` is older than the compiled Caddy pin.
- `journey/client/package.json` pins `@actual-app/api` to `actual.version`.

**The client** (`client.js`) runs as `node client.js <dir> <action> [arg]` and prints one JSON line:
`syncId`, `groupId`, `accounts`, `error` and `code`. The actions are `bootstrap`, `create`, `download`, `add`,
`sync`, `list` and `revert`. Each client has its own folder, mounted into a `journey-client` container that runs
with `--add-host <hostname>:host-gateway` and `NODE_EXTRA_CA_CERTS` set to the test CA. So it reaches the
Instance through the Proxy at `https://<hostname>`.

**Golden normalisation** (`Normalise(s, v)`): the replacements run in this order on the text, and the same rules
apply to `--json`. The first group matches the run's own `Values` as exact strings. The rest match patterns.

| Matches | Becomes |
|---|---|
| `v`'s `JOURNEY_VERSION`, commit, Go version and `linux/<arch>` | `<VERSION>`, `<COMMIT>`, `<GO>`, `<PLATFORM>` |
| `v`'s Actual pin, older Actual and Caddy pin versions | `<ACTUAL>`, `<ACTUAL_OLDER>`, `<CADDY>` |
| `v`'s UID and GID, only after `uid=`, `gid=`, `user=` or in a `"user":` JSON value | `<UID>`, `<GID>` |
| RFC 3339 times, `HH:MM:SS` progress times, dates with a zone abbreviation | `<TIME>` |
| `YYYYMMDDTHHMMSSZ` stamps, including inside Backup ids | `<STAMP>` |
| `sha256:<64 hex>` | `<DIGEST>` |
| any other run of 12 or 64 lower-case hex characters standing alone (container ids, digest prefixes, Certificate ids) | `<HEX>` |
| durations (`41s`, `1m3s`) and sizes (`48MB`) | `<DUR>`, `<SIZE>` |

## 5. Depends on

- [foundation.md](foundation.md): the `-X` variables, `make build` and `LDFLAGS_EXTRA`, `make tools`, `hostfs.Under`,
  `op.ManualClock`, `op.Recorder` and `ci.yml`.
- Every module spec, for its own fakes and its journey slice. The harness imports `internal/docker/dockertest`,
  `internal/proxy/certtest` and `internal/alerts/pushovertest`, and `TestRegistryContract` uses `internal/versions`.
- [release.md](release.md) calls `journeys.yml` (with `keep-binaries`) and `make journey HOST=… JOURNEY_BIN=…`.

The import rule this spec adds to `internal/archtest`:

- `journey/fakeactual` imports only the standard library;
- no package outside `journey/` imports `journey/...`;
- the harness imports `internal/...` only from `_test.go` files.

## 6. Decisions

- **Two layers.** Module tests through each interface, with fakes only at real seams; and journeys of the real
  binary as root. Reason: module tests are fast, portable and run without privileges. Only the journeys prove
  owners, modes, Docker, systemd and Caddy together.
- **CLI tests go through `cli.Run`** with `clitest.Env(t)`, and journey 8 checks the same contract once more on
  the real binary.
- **Journeys are Go tests** behind a build tag, in one static test binary that runs the same way on a runner and
  on a test VM. There is no shell harness.
- **Each journey starts from a blank Host.** A failure in one journey can't hide or cause a failure in another,
  and `-test.run` can run any one of them alone.
- **Every journey image comes from a local registry** filled from a digest-intact cache, so there are no Docker
  Hub pulls, no rate limits and no run that depends on what Hub serves today. The images are stamped in at build
  time, and the release binary keeps the real values ([foundation.md](foundation.md)).
- **The fake Actual image has one tag per behaviour**, with nothing set at run time. The tags are `99.0.x`, so
  they sort above every real Actual version, and the Upgrade code path is the one a real Upgrade takes.
- **The never-healthy Upgrade passes `--timeout 30s`**, so journey 6 doesn't wait the full 5 minutes.
- **The scheduled Backup is checked once end to end** (journey 5, step 7). Its timing and Retention are module
  tests with a fixed clock ([backup.md](backup.md)), and the timer unit itself is checked in journey 1.
- **The journeys run only on GitHub's Ubuntu runners in CI.** The other supported Hosts and the fresh Docker
  install are checked by the Release check ([release.md](release.md)).

## 7. Later

None of its own. The Later items that need a journey (clone, off-host Backups) bring it when they land.

## 8. Docs it changes

- `CONTRIBUTING.md` gets a "Running the journeys" section. It covers:
  - what they need: a Linux Host with Docker and sudo, never one with real Instances (a run wipes all Abyss
    state);
  - `make image-cache` and when to run it again, with the optional read-only Docker Hub token;
  - `make journey`, with `HOST`, `RUN` and `UPDATE=1`;
  - reading `dist/journey-out/`;
  - moving a journey pin.

## 9. Acceptance list

The tests below check the harness itself. The journeys check the acceptance items of every other spec that names a
journey.

1. `journey/testdata/pins.json` has its five keys and no others, with valid versions and digests in the order
   rules above. `journey/client/package.json` pins `@actual-app/api` to the journey pin. →
   `TestPins_Valid` (in `make test`)
2. `Normalise` replaces each row of the table, and leaves text that only looks close alone: a 63-hex string, a
   version inside a Hostname, a number equal to the UID outside its contexts. → `TestNormalise` (table, in `make test`)
3. The fake Actual server answers `/health` and `/info` as its mode's row says. Its `node` health mode exits 0
   only on a healthy `/health`, and `node --version` exits 0. → `TestFakeActual` (table, in `make test`)
4. The harness and the journey files compile and lint with the `journey` tag. → the CI `make lint` step
5. `TestMain` refuses to start without `-journey.wipe`, without Docker at the minimums, or with a cache that does
   not serve every pinned digest. Each refusal comes with its message. → the harness start-up check, run by the
   journey jobs
6. `Blank` leaves none of the items in [A blank Host](#a-blank-host), keeps what it should keep, and fails the
   journey if one remains. → `TestHarnessBlank`, and checked at the start of every journey
7. `dockertest.Contract` passes against the real Docker CLI with the fake image's tags. → `TestDockerContract`
8. Versions against the local `registry:2` returns the digests Docker lists after a pull. → `TestRegistryContract`
9. Journey 1 passes as written above. → `TestJourney1Bootstrap`
10. Journey 2 passes as written above. → `TestJourney2ToolUpdate`
11. Journey 3 passes as written above. → `TestJourney3InstanceLife`
12. Journey 4 passes as written above. → `TestJourney4ProxyAndCertificate`
13. Journey 5 passes as written above. → `TestJourney5BackupAndRestore`
14. Journey 6 passes as written above. → `TestJourney6UpgradeAndRollback`
15. Journey 7 passes as written above. → `TestJourney7HealthAndAlerts`
16. Journey 8 passes as written above. → `TestJourney8OutputContract`
17. After every journey, each image on the Host is under `127.0.0.1:5000/`, or is `registry:2` or `journey-client`. → `TestNoHubPulls`
18. Items 5–17 pass on both `ubuntu-24.04` and `ubuntu-24.04-arm`. They run for every pull request to `main` and
    every push to `main`, and in the tag workflow before the draft Release. → the `journeys.yml` runs (review of
    the workflow triggers)
19. A failing journey leaves `out/<journey>/` with the items listed, and no Secret in it. → (review)
20. The import rules hold: `journey/fakeactual` imports only the standard library, and nothing outside `journey/`
    imports it. → `TestImportRules` in `internal/archtest`
21. `CONTRIBUTING.md` has the "Running the journeys" section. → (review)
22. With `journey-client:<hash>-<arch>` in the local registry, start-up pulls it and runs no `npm ci`; without it,
    start-up builds and pushes it and packs `cache/registry.tar` again, and the next run on that cache pulls it. →
    `TestJourneyClientCache`
