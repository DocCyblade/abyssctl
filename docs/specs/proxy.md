# Spec: proxy

Build step 5 of 16. The [overview](README.md) has the module map, the call graph and the Host layout. Shared
packages (`internal/hostfs`, `internal/op`, `internal/buildinfo`) are in [foundation.md](foundation.md); the Docker
adapter and locks are in [docker-and-locks.md](docker-and-locks.md); the files Proxy writes are rendered by
[render.md](render.md). Capitalised terms are defined in [GLOSSARY.md](../../GLOSSARY.md).

## 1. Purpose

Build the Proxy module (`internal/proxy`) and the verbs `cert install`, `cert list` and `cert remove`. The
[Proxy](../../GLOSSARY.md) is one tool-owned Caddy container that owns ports 80 and 443 on all interfaces and sends
each [Hostname](../../GLOSSARY.md) to its [Instance](../../GLOSSARY.md) over the private network `abyssctl`
([ADR 0001](../adr/0001-tool-owned-caddy-proxy.md)). Proxy owns:

- the Proxy container: bringing it up ready, keeping its image, networks and published ports right, and
  recreating it only when its rendered `compose.yaml` changes or those drift;
- `proxy/Caddyfile`, `proxy/compose.yaml` and `proxy/proxy.env` (the Proxy's version and digest). Proxy is the
  only writer of all three;
- one site file per Instance, `proxy/sites/<name>.caddy`, and the write → validate → reload cycle that applies it;
- [Certificates](../../GLOSSARY.md) in `proxy/certs/`: their checks, install, replacement, listing and removal;
- coverage: which Certificate covers a Hostname, and the choice of one when several do;
- `Probe(ctx, hostname, path)`, the one way any module reaches an Instance over HTTPS;
- the [Proxy upgrade](../../GLOSSARY.md) operation and its target rule.

Proxy is fed what it needs: callers pass it Instance names and Hostnames, so it never reads an
[Instance record](../../GLOSSARY.md). Caddy's own state in `proxy/caddy-data/` and `proxy/caddy-config/` is
Caddy's; Proxy never reads or writes it. `logs proxy` is `docker.Logs` of the Proxy container, called by the CLI.
The [Health check](../../GLOSSARY.md) owns the Proxy-down and Certificate-expiry [Problems](../../GLOSSARY.md) and
[Alerts](../../GLOSSARY.md), from data Proxy gives it.

Proxy is built before Host, with module tests and the `cert` verbs' CLI tests only. When this spec is done, every
operation below passes its module tests against `dockerfake` and a temp Data root, and the `cert` verbs pass their
`internal/cli` tests. Host (step 6) wires the real Proxy into `host setup`; journey 4 runs once Lifecycle (step 7)
can create an Instance; the `proxy upgrade` verb lands with [versions.md](versions.md) (step 9), which resolves its
target, and calls `PlanUpgrade` and `Upgrade` as this spec describes them.

## 2. Interface

Signature sketches, not final code.

```go
type Deps struct {
    Paths         hostfs.Paths   // with the Data root (Paths.WithDataRoot)
    Docker        docker.Client
    Emit          op.Emitter
    Clock         op.Clock       // only for "now" (Certificate validity); waits use the durations below
    Pins          buildinfo.Pins // buildinfo.CompiledPins(); Pins.Caddy seeds proxy.env and sets the upgrade target rule
    ProbeAddr     string         // default "127.0.0.1:443"; tests pass a local test server's address
    ReadyTimeout  time.Duration  // default 30 s: how long the readiness check polls
    PollInterval  time.Duration  // default 1 s: between readiness attempts
    ReloadTimeout time.Duration  // default 30 s: one deadline for validate + reload
}

func New(d Deps) *Proxy // fills each zero duration and an empty ProbeAddr with its default

// --- The container and its files. Changing methods: precondition, the caller holds the Host lock. ---

// PlanEnsure says what Ensure would do: Steps one panel line each, Warnings (for example a Proxy behind the
// compiled-in Caddy pin), NoOp when there is no step. It writes nothing, starts nothing and pulls nothing.
func (p *Proxy) PlanEnsure(ctx context.Context) (op.Summary, error)

// Ensure writes the Caddyfile, the Proxy compose.yaml and (only if missing) proxy.env from Pins.Caddy, pulls the
// Proxy's image if it is not local, and brings the Proxy up, ready, on the abyssctl network with only 80 and 443
// published. A changed Caddyfile is validated and reloaded; the container is recreated only when its compose.yaml
// bytes change or its image, networks or published ports drift.
func (p *Proxy) Ensure(ctx context.Context) error

// Status is what status, the Health check, the MOTD, Upgrade's pre-flight and image prune need. Read-only.
// An invalid proxy.env is the same op.NeedsOperator error Ensure returns.
func (p *Proxy) Status(ctx context.Context) (Status, error)

type Status struct {
    SetUp   bool   // proxy.env exists
    Running bool   // the container abyssctl-proxy is running
    Version string // PROXY_VERSION
    Image   string // <CaddyRepo>:<PROXY_VERSION>@<PROXY_DIGEST>, the image in use (image prune keeps it)
    Behind  bool   // Pins.Caddy is newer than Version: status and the MOTD show an ↑ line
}

// --- Sites. ---

// Covers returns the id of the Certificate chosen for hostname, and false when none covers it. Read-only. Used by
// create before anything starts.
func (p *Proxy) Covers(hostname string) (id string, ok bool, err error)

// AddSite writes proxy/sites/<name>.caddy for hostname with the chosen Certificate, then validates and reloads.
// It does nothing when the file's bytes would not change. Refused (exit 2, cause ErrNotCovered) when no
// Certificate covers hostname, and with the hint "abyssctl host setup" when there is no proxy.env.
// Precondition: the caller holds the Host lock.
func (p *Proxy) AddSite(ctx context.Context, name, hostname string) error

// RemoveSite deletes proxy/sites/<name>.caddy, then validates and reloads. A missing file is not an error and
// needs no reload. Precondition: the caller holds the Host lock.
func (p *Proxy) RemoveSite(ctx context.Context, name string) error

var ErrNotCovered = errors.New("no Certificate covers this Hostname")

// --- Certificates. hostnames is every Instance's Hostname; the CLI and Host get them from store.List. ---

type Certificate struct {
    ID        string    // the first 12 hex digits of the SHA-256 fingerprint of the leaf
    Names     []string  // the leaf's DNS names (SANs), lower case, in the certificate's order
    Issuer    string    // the issuer's common name, or its full DN when there is none
    NotBefore time.Time
    NotAfter  time.Time
    UsedBy    []string  // the Hostnames whose chosen Certificate this is, sorted
    Unused    bool      // UsedBy is empty and no site file names this id
}

// Certificates lists every installed Certificate, sorted by ID. Read-only; for cert list and the Health check.
// A pair that cannot be read is left out and named in warnings.
func (p *Proxy) Certificates(hostnames []string) (certs []Certificate, warnings []string, err error)

// CheckCertificate runs cert install's checks on the two files and changes nothing. Its rows are the panel's
// facts: id, names, issuer, expires. Refusals match op.ErrRefused.
func (p *Proxy) CheckCertificate(certFile, keyFile string) (summary []op.Row, err error)

// PlanInstallCertificate is cert install's panel: CheckCertificate's rows, the Certificates it replaces, the site
// files it re-renders, the steps and warnings. Read-only.
func (p *Proxy) PlanInstallCertificate(ctx context.Context, certFile, keyFile string, hostnames []string) (op.Summary, error)

// InstallCertificate is cert install: copy, re-render the site files whose chosen Certificate changes, validate
// and reload, then delete the Certificates it replaces. Precondition: the caller holds the Host lock.
func (p *Proxy) InstallCertificate(ctx context.Context, certFile, keyFile string, hostnames []string) error

// PlanRemoveCertificate is cert remove's panel. Read-only. The same refusals as RemoveCertificate.
func (p *Proxy) PlanRemoveCertificate(ctx context.Context, id string, hostnames []string) (op.Summary, error)

// RemoveCertificate is cert remove: re-render the site files that used it, validate and reload, then delete both
// files. Refused when it would leave a Hostname uncovered. Precondition: the caller holds the Host lock.
func (p *Proxy) RemoveCertificate(ctx context.Context, id string, hostnames []string) error

// --- The probe. ---

// Probe sends GET https://<hostname><path> to ProbeAddr with hostname as SNI and Host header. It skips chain
// verification, but the served certificate must cover hostname. Any HTTP status is a Response with no error.
func (p *Proxy) Probe(ctx context.Context, hostname, path string) (Response, error)

type Response struct {
    Status    int
    Body      []byte // at most 1 MiB
    Truncated bool   // the body was longer and was cut at 1 MiB
}

var (
    ErrUnreachable      = errors.New("the Proxy is not accepting connections")         // no TCP connection
    ErrHandshake        = errors.New("the TLS handshake with the Proxy failed")        // no Certificate covers the name, or no site at all
    ErrWrongCertificate = errors.New("the Proxy served a Certificate for other names") // the served leaf does not cover hostname
)

// --- Proxy upgrade. ---

// What the CLI learned from Versions before calling, so Proxy can apply its target rule.
type UpgradeRequest struct {
    Version   string             // what the Operator typed; "" when no version was given
    Found     buildinfo.ImagePin // Versions.Resolve(CaddyRepo, Version), or Versions.Latest(CaddyRepo) when Version is ""
    LookupErr error              // the lookup failed; Found is then zero
}

// PlanUpgrade applies the target rule and returns the panel. Read-only; it never pulls.
func (p *Proxy) PlanUpgrade(ctx context.Context, r UpgradeRequest) (op.Summary, error)

// Upgrade pulls the target, validates the current config against it, swaps, and puts the old Proxy back if the new
// one does not come up ready. Precondition: the caller holds the Host lock.
func (p *Proxy) Upgrade(ctx context.Context, r UpgradeRequest) error
```

`*proxy.Proxy` satisfies Host's narrow `Proxy` interface ([host.md](host.md)); the CLI builds Host's
`NewProxy func(hostfs.Paths) Proxy` as a closure over `proxy.New` with the shared Docker client, Emitter, Clock and
pins.

`internal/proxy/certtest` is a test helper, imported only by tests and the journey harness: `NewCA(t)` and
`(*CA).Leaf(t, LeafOptions{Names, NotBefore, NotAfter, KeyType})`, which write PEM files into `t.TempDir()`.

### Invariants and error modes

- **Refusals come before any change.** A bad file, an expired Certificate, an uncovered Hostname, an unreadable
  site file, a Certificate that would leave a Hostname uncovered, a missing `proxy.env` and a disallowed upgrade
  target are `op.Refused` (exit 2), found before Proxy writes, pulls or starts anything. An invalid `proxy.env` is
  found at the same point and is `op.NeedsOperator` (exit 4): the message names the file, the line and the
  problem, and the hint is "fix <data root>/proxy/proxy.env by hand". `PlanX` and `X` return the same error for
  the same state.
- **What Caddy serves changes only through a successful reload.** Every write to the Caddyfile, a site file or
  `proxy/certs/` that Caddy must pick up is followed by `caddy validate`, then `caddy reload`. If either fails,
  Proxy puts back each file it wrote (deleting the ones it created), returns a plain error (exit 1) carrying
  Caddy's stderr tail, and Caddy keeps serving its old config, because a failed validate changes nothing and a
  failed reload leaves the running config in place.
- **A file is written only when its bytes change**, through `hostfs.WriteFile(path, data, 0600)`. An operation
  whose files are all unchanged does no reload and emits no event but `checked`.
- **The Proxy container is never recreated by a site or Certificate change**, only by `Ensure` (a changed Proxy
  `compose.yaml`, or a drifted image, network or port) or by `Upgrade`. Recreating it makes every Instance
  unreachable for a few seconds, so each plan that does it says so.
- **Private keys stay private.** Proxy reads a key only to check it against its Certificate and to copy it. No key
  byte, and no part of a key, appears in a row, an event, an error or a log line. Certificates and their names may
  be shown; they are public.
- **Proxy never prints and never locks.** It emits events and returns typed errors; the CLI takes the Host lock.
  `Plan…`, `Status`, `Covers`, `Certificates`, `CheckCertificate` and `Probe` change nothing and need no lock.
- **Proxy never reads Instance records.** Site files are named by the Instance name the caller passes. When a
  Certificate change re-renders site files, Proxy reads each one's name and Hostname back with `render.ParseSite`,
  and checks coverage for those Hostnames together with the `hostnames` passed in.
- **Waits are bounded by `ctx` deadlines** built from `ReadyTimeout` and `ReloadTimeout`, not by `Clock`, so module
  tests run with millisecond durations.

## 3. Behaviour

### Ensure

Ensure runs on every `host setup`, so a first run, a re-run and a [Tool update](../../GLOSSARY.md) are one path.

1. **Read `Paths.ProxyEnv()`.** Missing: the target is `Pins.Caddy` and apply writes it. Present but invalid:
   `op.NeedsOperator` (exit 4) naming the file, line and problem, with the hint "fix <data root>/proxy/proxy.env
   by hand", and nothing changed. The image is
   `<buildinfo.CaddyRepo>:<PROXY_VERSION>@<PROXY_DIGEST>`, never the compiled pin of an existing file: the Proxy's
   version moves only through `proxy upgrade`. When `Pins.Caddy` is newer, the plan warns "the Proxy is on caddy
   <v>; abyssctl proxy upgrade moves it to <pin>".
2. **Render.** `render.Caddyfile()` and `render.ProxyCompose(image)`; compare each with the file on disk.
3. **Inspect.** `Docker.Image(image)`, `Docker.Container(names.ProxyProject)`. Drift is a container whose image is
   not `image`, whose `Networks` is not exactly `{abyssctl}`, or whose `Published` ports are not exactly TCP 80 and
   443, each with a `HostIP` of `""`, `0.0.0.0` or `::`. `PlanEnsure` on a Host where Docker is missing
   (`ErrNotInstalled`, `ErrNoCompose`, `ErrDaemon`) or where `proxy/` does not exist yet treats it as "no image, no
   container" and returns the first-run steps, because `host setup` installs Docker and creates the folders before
   it calls `Ensure`. The steps, in order:
   - `pull <image>` when the image is not local;
   - `write proxy.env: caddy <version>` when it is missing;
   - `write the Caddyfile` and `write the Proxy compose.yaml` when their bytes change;
   - `start the Proxy` when there is no container or it is not running;
   - `recreate the Proxy: every Instance unreachable for a few seconds` when a running container's `compose.yaml`
     bytes change or it has drifted;
   - `reload the Proxy` when only the Caddyfile changed and the container keeps running.
4. **Apply**, in that order. Sweep leftover `proxy/certs/*.partial` files and warn about each `<id>.key` with no
   `<id>.crt`; `Docker.Pull(image)`; write the files; then:
   - start or recreate: `Docker.ComposeUp(Project{names.ProxyProject, Paths.ProxyDir()}, UpOptions{Wait: true,
     WaitTimeout: 5 * time.Minute, ForceRecreate: <true whenever the plan lists the recreate step>})`, then the
     readiness check. A Caddyfile written in the same run is read by the new container at start, so it needs no
     separate reload;
   - reload only: validate and reload (below).
5. **Ready.** The Proxy image has no healthcheck, so `ComposeUp`'s wait means only "running". Ready means both,
   polled every `PollInterval` until `ReadyTimeout`:
   - `docker exec abyssctl-proxy caddy version` exits 0 and its first word is `v<PROXY_VERSION>`;
   - `docker exec abyssctl-proxy caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile` exits 0, so the
     admin API answers and the running config is the one on disk.

   A timeout is a plain error naming the check that failed, with the hint `abyssctl logs proxy`. A TCP check is
   not used: Docker's port forwarder accepts connections before Caddy listens, and a Proxy with no sites has no
   HTTPS listener.

An interrupted `Ensure` or Proxy upgrade leaves files the next `host setup` finishes: `Ensure` renders the compose
file from whatever `proxy.env` holds and brings that version up.

A Proxy with no site files starts and serves no site: an HTTPS request to it fails (the connection is closed or the
handshake fails).

### Validate and reload

Both run inside the running container, within one `ReloadTimeout` deadline:

```
docker exec abyssctl-proxy caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
docker exec abyssctl-proxy caddy reload   --config /etc/caddy/Caddyfile --adapter caddyfile [--force]
```

`--force` is passed whenever a Certificate file's bytes changed under a path the config already names, because a
plain reload skips a config that has not changed. The whole `proxy/` folder is mounted read-only at `/etc/caddy`
([render.md](render.md)), so a file replaced by rename is what the next validate and reload read. A non-zero code
from either is a failure, and its stderr (last 2 KiB) goes into the error's cause; `Docker.Exec` returning an error
(for example the deadline) is a failure too. On a failure Proxy writes back the previous bytes of every file this
operation changed, in reverse order, and deletes every file it created. If the Proxy is not running when a site or
Certificate operation needs a reload, Proxy first starts it with `ComposeUp` from its existing files and waits until
it is ready (event `start`), so a stopped Proxy is brought back by the next command rather than refusing it.

### Re-rendering site files

Every Certificate change (install, replacement, remove, a chain rewrite) ends the same way: for each file in
`proxy/sites/`, `render.ParseSite` gives its name and Hostname; Proxy chooses again from the Certificates that will
remain, renders `render.Site(SiteInput{Name, Hostname, CertID})`, and writes the files whose bytes change. A site
file `ParseSite` refuses, or whose name is not its file name, stops the operation before any write with a refusal
naming the file and the hint `abyssctl restart <name>`, which rewrites it.

### Certificates

**Checks (`CheckCertificate`).** Each file must be at most 1 MiB. Each failure below is `op.Refused` with one
sentence:

- the certificate file holds one or more `CERTIFICATE` PEM blocks and no private key; the first is the leaf, the
  rest are the chain, in the order given;
- the key file holds exactly one private key (`PRIVATE KEY`, `RSA PRIVATE KEY` or `EC PRIVATE KEY`) and no
  certificate; an `EC PARAMETERS` block is ignored;
- the key is not encrypted: an `ENCRYPTED PRIVATE KEY` block, or a legacy block with a `Proc-Type: 4,ENCRYPTED`
  header, is refused with "decrypt the key first";
- the key matches the leaf (`tls.X509KeyPair`);
- the leaf is valid now by `Clock`: `NotBefore` ≤ now < `NotAfter`;
- the leaf has at least one DNS name. Only DNS SANs count; the subject common name and IP SANs are ignored.

The chain is not verified: Certificates may come from a private CA, and the Operator's clients trust it.

**Id and files.** The id is the first 12 lowercase hex digits of the SHA-256 of the leaf's DER bytes. The files are
`Paths.CertFile(id)` (every `CERTIFICATE` block, re-encoded as PEM, leaf first) and `Paths.KeyFile(id)` (the one
key block, re-encoded as PEM), both 0600 root. The names are read from the Certificate itself each time; there is
no index file. Installing a Certificate whose files are already byte-identical is a no-op. The same leaf with
different chain bytes rewrites `<id>.crt` and reloads with `--force`.

**Coverage and choice.** A name covers a Hostname when it equals it, or when it is `*.<rest>` and the Hostname is
one label followed by `.<rest>` (a wildcard covers one level only: `*.budget.example.com` covers
`kids.budget.example.com`, not `budget.example.com` or `a.kids.budget.example.com`). Matching ignores case. Of the
Certificates that cover a Hostname, Proxy chooses the most specific (one with the exact name beats one that covers
it only by a wildcard), then the latest `NotAfter`, then the lowest id. An expired Certificate still covers, so an
expired exact-name Certificate beats a valid wildcard until it is removed; the Health check raises its Problem.

**Install (`InstallCertificate`).** After the checks:

1. Find the Certificates it replaces: each installed one whose every name appears literally (ignoring case) among
   the new one's names. A wildcard therefore never replaces a per-Hostname Certificate, and a renewal, with the same
   names, replaces the old one. If the new one expires before one it replaces, the plan warns "the new Certificate
   expires before <id>, which it replaces".
2. Write `<id>.crt` and `<id>.key`.
3. Re-render the site files (above), choosing from the installed Certificates minus the replaced ones plus the new
   one.
4. Validate and reload. Until this succeeds the replaced Certificates' files stay in place, so Caddy keeps serving
   them; on a failure the site files are put back and the new pair is deleted.
5. Delete each replaced Certificate's two files. They are no longer referenced, so no reload follows.

A Certificate that covers no Hostname in use is installed with the warning "covers no Instance yet". Installing
cannot leave a Hostname uncovered, because it only replaces Certificates whose names it carries. `cert install`
asks no question and supports `--dry-run`; its panel shows the rows, each replaced id, the site files it moves, and
the warning "the key files of replaced Certificates are deleted" when it replaces any.

**List (`Certificates`).** Every `<id>.crt` in `proxy/certs/` with its `<id>.key`, parsed, with `UsedBy` from the
choice over `hostnames` and the site files' Hostnames. A Certificate is unused when it covers no Instance and no
site file names its id. A `.crt` that does not parse, has no `.key`, or whose file name is not its own id is left
out with a warning naming the file. `cert list` maps `UsedBy` Hostnames to Instance names and shows "unused" for an
unused one. Its columns are id, names, expires (Host local date and zone) and used by. It takes no lock and has no
`--dry-run`.

**Remove (`RemoveCertificate`).** An unknown id is refused with the hint `abyssctl cert list`. Proxy chooses again
for every Hostname without that Certificate; if any is left uncovered, it refuses and names each one
("kids.budget.example.com would have no Certificate"). Otherwise it re-renders the site files, validates and
reloads, then deletes both files. An unused Certificate needs no reload. The panel's rows are the id, names, expiry
and used-by; its warning is "the key file is deleted; keep a copy if you need it". The CLI asks y/N, as for `image
prune`, and supports `--dry-run`.

### Sites

`AddSite(name, hostname)` refuses without `proxy.env` (hint `abyssctl host setup`), chooses the Certificate (none:
`op.Refused("no Certificate covers <hostname>").WithCause(ErrNotCovered)` with the hint `abyssctl cert install
<cert.pem> <key.pem>`), renders the site file and, when its bytes change, writes it, validates and reloads. It is
called by `create`, `start` and `restart` ([lifecycle.md](lifecycle.md)), so a hand-edited Hostname or a Tool update
reaches the site file on the next of those. `upgrade` and Restore keep the Hostname and leave the site file alone.
`RemoveSite(name)` is called by `remove`.

There is no catch-all site, and `auto_https disable_certs` means Caddy holds only the Operator's Certificates. So a
Hostname that no installed Certificate covers, or a request with no SNI, fails at the TLS handshake. A Hostname
covered by an installed Certificate (for example a wildcard) but with no site completes the handshake, and Caddy
answers with an empty 200; journey 4 confirms both. Port 80 answers only with Caddy's redirect to HTTPS for each
site.

### Probe

`Probe` dials `ProbeAddr` (whatever address the URL names) with an `http.Transport` that Proxy builds once:
`ServerName` is the Hostname, chain verification is skipped, and a `VerifyConnection` hook requires the leaf to
cover the Hostname by the rule above. Redirects are not followed, keep-alives are off, and the call is bounded by
`ctx` and a 10 s client timeout. A refused or timed-out TCP connection is `ErrUnreachable`; a failed handshake is
`ErrHandshake`; a served leaf that does not cover the Hostname is `ErrWrongCertificate`. A body over 1 MiB is cut
there and `Truncated` is set. A 502 from Caddy (the Instance is down) is a `Response`, not an error.

Upgrade's pre-flight and verify (`/health`, `/info`) and the Health check use `Probe`. The Health check treats a
non-running Proxy or `ErrUnreachable` as the Proxy-down Problem and then raises no Instance-unhealthy Problem for
that run ([health.md](health.md)). Because a covered Hostname with no site answers an empty 200, a caller checking
`/health` must check the body (`"status":"UP"`), not only the status.

### Proxy upgrade

The `proxy upgrade [<version>]` verb lands with [versions.md](versions.md). The CLI asks Versions first
(`Latest(CaddyRepo)`, or `Resolve(CaddyRepo, v)` for `proxy upgrade <v>`) and passes the result, or the lookup's
failure, in `UpgradeRequest`. Proxy applies the rule; `current` is `PROXY_VERSION`.

| Request | Target |
|---|---|
| no version, `Pins.Caddy` newer than `current` | `Pins.Caddy`, whatever the lookup returned |
| no version, `Pins.Caddy` not newer, lookup succeeded | `Found` when newer than `current`; otherwise no-op "the Proxy is on caddy <current>, the latest" |
| no version, `Pins.Caddy` not newer, lookup failed | refused: "could not look up the latest Caddy", the lookup error as cause |
| `<v>` not a plain `X.Y.Z` (`buildinfo.PlainVersion`) | refused |
| `<v>` older than `current` | refused: "caddy <v> is older than the Proxy's <current>; a downgrade is not supported" |
| `<v>` equal to `current` | no-op |
| `<v>` newer, equal to `Pins.Caddy.Version` | `Pins.Caddy`; `Found` and `LookupErr` are ignored |
| `<v>` newer, lookup succeeded | `Found` |
| `<v>` newer, lookup failed | refused: "could not look up caddy <v>", the lookup error as cause |

A target newer than `Pins.Caddy` gets the warning "caddy <v> is newer than the version this Abyss was tested with
(<pin>)". With no `proxy.env`, `PlanUpgrade` refuses with the hint `abyssctl host setup`.

The panel: title "Proxy upgrade", rows from, to and "downtime: a few seconds for every Instance"; steps: pull,
validate the current config with the new image, recreate the Proxy, check it is ready. The CLI asks y/N and
supports `--dry-run`. `Upgrade`:

1. `Docker.Pull("<CaddyRepo>:<v>@<digest>")`. A failure changes nothing (exit 1).
2. Validate the current config with the new image: `Docker.Run(RunSpec{Image: <new>, Cmd: caddy validate --config
   /etc/caddy/Caddyfile --adapter caddyfile, Mounts: [{Paths.ProxyDir(), /etc/caddy, read-only}]})`, which the
   adapter runs with `--network none`. A non-zero code is a plain error, "caddy <v> rejects the current
   config", with the stderr tail, and nothing else changes.
3. Swap: write `proxy.env` with the new version and digest, write `render.ProxyCompose(<new>)`, `ComposeUp` with
   `Wait` and `ForceRecreate`, then the readiness check, whose `caddy version` must report `v<v>`.
4. On a failure in step 3: emit the last 20 lines of the failed container's log as `caddy` events (Caddy's own log;
   access logs are off, so it holds no client data), write back the old `proxy.env` and `compose.yaml`, `ComposeUp`
   with `Wait` and `ForceRecreate`, and check readiness again. The old image was never removed, so this needs no
   pull. Back: plain error (exit 1), "caddy <v> did not start; the Proxy is back on caddy <current>". Not back:
   `op.NeedsOperator("the Proxy is down: caddy <v> and caddy <current> both failed to start; read its log, then run
   abyssctl host setup", "abyssctl logs proxy")` (exit 4).

The old image stays local after a successful upgrade; `image prune` removes it later, keeping `Status.Image`.

### Events

One line each, snake_case steps, values with spaces quoted, never a key or key material: `pull ref=…`,
`write path=…`, `validate`, `reload force=true|false`, `start`, `recreate reason=compose|drift|upgrade`,
`ready version=…`, `cert id=… names="a.example.com, *.example.com" expires=…` (installed), `cert_removed id=…`,
`site name=… hostname=… cert=…`, `restored path=…` (after a failure), `swept path=…`, `caddy line="…"`, `checked`.

## 4. Files and formats it owns

All under the Data root, root:root, folders 0700 and files 0600 ([Host layout](README.md#host-layout)). Host
creates the folders; Proxy writes the files.

| Path | Content | Written by |
|---|---|---|
| `proxy/Caddyfile` | `render.Caddyfile()` | `Ensure` |
| `proxy/compose.yaml` | `render.ProxyCompose(image)` | `Ensure`, `Upgrade` |
| `proxy/proxy.env` | the Proxy's version and digest | `Ensure` (only if missing), `Upgrade` |
| `proxy/sites/<name>.caddy` | `render.Site(SiteInput{Name, Hostname, CertID})` | `AddSite`, Certificate changes; deleted by `RemoveSite` |
| `proxy/certs/<id>.crt` | the Certificate and its chain, PEM, leaf first | `InstallCertificate`; deleted when replaced or removed |
| `proxy/certs/<id>.key` | its private key, PEM: a [Secret](../../GLOSSARY.md) | with its `.crt` |

`proxy.env`, `KEY=value`, exactly these two lines in this order, each ending in a newline (a missing final newline
is accepted):

```
PROXY_VERSION=2.10.2
PROXY_DIGEST=sha256:<64 lowercase hex>
```

`PROXY_VERSION` passes `buildinfo.PlainVersion` ([foundation.md](foundation.md)), the rule the Caddy pin follows.
`PROXY_DIGEST` is `sha256:` and 64 lowercase hex digits. A missing key, an unknown key, a blank line or a comment
makes the file invalid (exit 4, fix by hand); Proxy is its only writer, so any of these is a hand edit. The exact formats of the
Caddyfile, the site file and the Proxy `compose.yaml` are [render.md](render.md)'s.

## 5. Depends on

- [foundation](foundation.md): `hostfs.Paths` (the `proxy/` paths), `hostfs.WriteFile`; `op.Emitter`, `op.Clock`,
  `op.Row`, `op.Summary` and the typed errors; `buildinfo.Pins`, `buildinfo.CaddyRepo`,
  `buildinfo.CompareVersions`, `buildinfo.PlainVersion`.
- [docker-and-locks](docker-and-locks.md): `docker.Client` (`Container`, `Image`, `Pull`, `ComposeUp`, `Exec`,
  `Run`, `Logs`) and its error kinds, `docker/names` (`ProxyProject`, `Network`). Proxy does not import
  `internal/locks`.
- [render](render.md): `Site`, `ParseSite`, `Caddyfile`, `ProxyCompose`.
- Not Store and not Versions: callers pass Hostnames and the resolved Caddy version.

Import rules this spec adds to `internal/archtest`:

- `internal/proxy` imports only the standard library, `internal/hostfs`, `internal/op`, `internal/buildinfo`,
  `internal/docker`, `internal/docker/names` and `internal/render`.
- Only `internal/cli`, `internal/host`, `internal/lifecycle`, `internal/upgrade` and `internal/health` import
  `internal/proxy`.
- `internal/proxy/certtest` is imported only by tests and the journey harness.

The CLI's lock table ([cli.md](cli.md)) gives the Host lock to every verb that can write a Proxy file: `cert
install`, `cert remove`, `proxy upgrade` and `host setup`. Of the verbs that call `AddSite` or `RemoveSite`,
`create` takes the Host lock and holds it until `Create` returns (`Create` takes the new Instance's lock itself);
`start` and `restart` take the Host lock, then the Instance lock, and release the Host lock right after the site
step (`Options.AfterSite`); `remove` holds both to the end. `cert list` takes no lock.

Notes for [health.md](health.md): the Proxy-down Problem comes from `Status.Running` and `ErrUnreachable`; the
expiry Problem uses `Certificates` and only Certificates that are not `Unused`; its hint is `abyssctl cert remove
<id>` when another installed Certificate also covers every Hostname in `UsedBy` (an expired exact-name Certificate
still beats a valid wildcard), otherwise `abyssctl cert install <cert.pem> <key.pem>`; and `/health` must check the
body, not only a 200. Note for [testing.md](testing.md): the test image cache holds two Caddy versions; journey 4
seeds `proxy.env` with the older one and `proxy upgrade` moves it to the pin.

## 6. Decisions

- One Proxy, the official `caddy` image pinned by digest, owning 80 and 443 on all interfaces, with the Operator's
  own Certificates and `auto_https disable_certs`, so it never contacts an ACME CA
  ([ADR 0001](../adr/0001-tool-owned-caddy-proxy.md)). Hosts may be internal-only, so a public challenge can't be
  relied on.
- The Proxy runs root inside its container with `cap_drop: [ALL]`, `cap_add: [NET_BIND_SERVICE]`,
  `no-new-privileges` and a read-only root filesystem ([render.md](render.md)). Non-root fails because the image
  gets low ports from file capabilities, which `no-new-privileges` ignores; high internal ports would break Caddy's
  redirect to HTTPS; and root reads the 0600 root keys without a second account.
- Every Caddy change goes write → validate → reload with an undo, and a site or Certificate change never recreates
  the container, so one Instance's change never interrupts the others.
- Certificates are stored by fingerprint, and their names are read from the Certificate itself, so there is no
  index to drift from the files. A Certificate that carries all of an existing one's names replaces it, which makes
  renewal one `cert install`; a wildcard never replaces a per-Hostname Certificate, so adding one never removes the
  Operator's more specific ones.
- The most specific Certificate wins, then the latest expiry, so a per-Hostname Certificate can sit beside a
  wildcard and the newest of two overlapping ones is served.
- Removing a Certificate never leaves a Hostname uncovered; `create` for an uncovered Hostname fails before
  anything starts.
- Proxy is fed Hostnames and never reads Instance records, so it depends only on Render and Docker. It reads its own
  site files back through Render, which owns their format.
- The Proxy's version moves only through `proxy upgrade`, never through `host setup` or a Tool update, because a new
  Caddy can change behaviour for every Instance at once. A Tool update may still recreate the Proxy when the
  binary renders a different Proxy `compose.yaml`, because the container's hardening belongs to the binary.
- With no version given, `proxy upgrade` prefers the binary's compiled-in pin when it is newer, because that is the
  version this Abyss was tested with. A failed lookup falls back to that pin when it is newer and otherwise
  refuses: Proxy never guesses a version it cannot pin by digest.
- A failed Proxy upgrade puts the old Proxy back and exits 1, not 3: exit 3 is an Instance's
  [Upgrade](../../GLOSSARY.md) rolled back, and a Proxy upgrade is not an Upgrade.
- Readiness is checked through Caddy itself (`caddy version` and a reload through the admin API), not through a
  TCP connect, which Docker's port forwarder accepts before Caddy listens.
- The probe goes through the Proxy with real SNI and skips chain verification, so it checks routing and coverage
  without needing the Operator's CA on the Host. It takes an address rather than an `http.Client`, because the SNI
  and the coverage check live in the transport it builds.
- Access logs are off: Caddy logs only its own errors.
- No new dependency: `crypto/x509`, `crypto/tls`, `crypto/sha256`, `encoding/pem` and `net/http` from the standard
  library.

## 7. Later

- ACME / Let's Encrypt. The Operator's Hosts are internal-only, so it needs the DNS-01 challenge and a custom Caddy
  build (`xcaddy`) with a DNS provider plugin.
- Sharing the Host: custom ports, or binding to one IP.
- An HSTS option for Hosts on the internet.
- Access logs as a setting.

## 8. Docs it changes

- README: the `cert install` line of **Quickstart** (`sudo abyssctl cert install cert.pem key.pem`, a wildcard for
  the Base domain or one per Hostname).
- `docs/guides/certificates.md`: `cert install` (the leaf first and the chain after it in one file, an unencrypted
  key, a private CA is fine), the coverage rules (one level per wildcard, the most specific then the latest
  expiry, an expired exact-name Certificate still winning until removed), renewal by installing the new
  Certificate and what it replaces (only Certificates whose names it carries), the expiry Alert
  (`CERT_WARN_DAYS`, only for Certificates in use), `cert list` and "unused", and `cert remove` (refused while it
  would leave an Instance uncovered; the key file is deleted).
- `docs/guides/upgrades.md`: `proxy upgrade [<version>]`, the target rule, the few seconds every Instance is
  unreachable, that a failed upgrade puts the old Proxy back, and that `host setup` never moves the Proxy's
  version.
- `docs/guides/security.md`: the Proxy's hardening, the keys at 0600 root under `proxy/certs/`, no ACME and no
  outbound certificate traffic, access logs off, and that the Proxy listens on 80 and 443 on all interfaces.
- Reference: the `cert install`, `cert list` and `cert remove` pages, from their help text (the `proxy upgrade` page
  with [versions.md](versions.md)).

## 9. Acceptance list

Module tests are in `internal/proxy`, on `hostfs.Under(t.TempDir())`, `dockerfake`, an `op.Recorder`, an
`op.ManualClock` and `certtest`, without root, Docker or Caddy, with `ReadyTimeout`, `PollInterval` and
`ReloadTimeout` in milliseconds. Caddy's answers are scripted with `SetExec`/`SetRun`, and `ProbeAddr` points at an
`httptest` TLS server or a closed port. The `cert` verbs' tests are in `internal/cli`, with a fake Proxy. Journey 4
(Proxy and Certificate) needs `host setup` and `create`, so its items are checked by lifecycle.md's journey slice,
and its `proxy upgrade` step by versions.md's.

1. A first `Ensure` writes `proxy.env` from `Pins.Caddy`, the Caddyfile and the Proxy `compose.yaml` (each equal to
   Render's bytes, the image `<CaddyRepo>:<version>@<digest>`), pulls the missing image and calls `ComposeUp` with
   `Wait` and 5 min; a second `Ensure` writes nothing, starts nothing and `PlanEnsure` returns `NoOp`. →
   `TestEnsure_FirstRun`, `TestEnsure_NoOp`
2. With a `proxy.env` naming an older version than `Pins.Caddy`, `Ensure` keeps it, renders the compose file from
   it, and `PlanEnsure` warns that the Proxy is behind the pin. → `TestEnsure_KeepsProxyVersion`
3. `PlanEnsure` with Docker missing (`ErrNotInstalled`, `ErrNoCompose`) or with no `proxy/` folder returns the
   first-run steps and no error. → `TestPlanEnsure_NoDockerYet` (table)
4. A changed Caddyfile with a running container is validated then reloaded through `docker exec abyssctl-proxy
   caddy …` with the exact arguments, and the container keeps its ID; a validate or reload failure puts the old
   Caddyfile back and returns an error with Caddy's stderr; a deadline past `ReloadTimeout` is a failure too. →
   `TestEnsure_ReloadsCaddyfile`, `TestReload_UndoOnFailure`, `TestReload_Deadline`
5. Changed compose bytes, a different image, a second network, an extra published port or one bound to
   `127.0.0.1` each plan "recreate the Proxy: every Instance unreachable for a few seconds" and call `ComposeUp`
   with `ForceRecreate`, giving a new container ID; ports on `""`, `0.0.0.0` and `::` are not drift; a stopped
   container plans "start the Proxy". → `TestPlanEnsure_Steps` (table), `TestEnsure_Recreate`
6. Readiness needs `caddy version` printing `v<PROXY_VERSION>` and `caddy reload` exiting 0, retries every
   `PollInterval`, and fails after `ReadyTimeout` naming the failed check with the hint `abyssctl logs proxy`. →
   `TestEnsure_Ready` (table)
7. `Ensure` deletes `proxy/certs/*.partial` files and warns about a `.key` with no `.crt`. → `TestEnsure_SweepsCerts`
8. An invalid `proxy.env` (a missing key, an unknown key, a comment, a blank line, `v2.10.2`, a bad digest) gives
   `op.NeedsOperator` (exit 4) naming the file, the line and the problem, with the hint "fix <data
   root>/proxy/proxy.env by hand", from `Ensure`, `Status`, `AddSite` and `PlanUpgrade`, with nothing written; a missing final newline is
   accepted; with no `proxy.env`, `PlanUpgrade` and `Upgrade` refuse with the hint `abyssctl host setup`. →
   `TestProxyEnv_Invalid` (table), `TestProxyEnv_NoFinalNewline`, `TestPlanUpgrade_NoProxyEnv`
9. `CheckCertificate` refuses an unparseable file, a key in the certificate file, a certificate in the key file, a
   key that does not match, an `ENCRYPTED PRIVATE KEY` and a `Proc-Type: 4,ENCRYPTED` key, an expired or
   not-yet-valid leaf, a leaf with no DNS name and a file over 1 MiB, each with exit 2; it accepts RSA, ECDSA and
   Ed25519 keys in PKCS#1, PKCS#8 and SEC 1 form (with an `EC PARAMETERS` block), and a leaf from a private CA; its
   rows hold the id, names, issuer and expiry. → `TestCheckCertificate` (table)
10. Install writes `<id>.crt` (chain, leaf first) and `<id>.key` at 0600 under the fingerprint id; installing the
    same files again is a no-op with no reload; the same leaf with a different chain rewrites `<id>.crt` and
    reloads with `--force`. → `TestInstall_Files`, `TestInstall_Again`, `TestInstall_ChainChange`
11. Coverage: an exact name covers only itself; `*.b.example.com` covers `a.b.example.com` and not `b.example.com`
    or `x.a.b.example.com`; case is ignored; the choice is exact over wildcard (an expired exact one included),
    then the latest `NotAfter`, then the lowest id. → `TestCovers` (table), `TestChoose` (table)
12. A Certificate carrying all of an older one's names replaces it: the site files move to the new id, the old files
    are still on disk at the reload and deleted after it; a reload failure keeps the old Certificate, puts the site
    files back and deletes the new pair. → `TestInstall_Replaces`, `TestInstall_UndoOnFailure`
13. A wildcard installed beside per-Hostname Certificates replaces none of them and moves no site file whose
    exact-name Certificate stays; one that expires before a Certificate it replaces gives the warning; the panel
    says replaced key files are deleted; one covering no Hostname warns "covers no Instance yet". →
    `TestInstall_WildcardKeepsExact`, `TestPlanInstall_Warnings`
14. A Certificate change re-renders every site file from `ParseSite`'s name and Hostname and writes only those
    whose bytes change; an unparseable site file or one whose name differs from its file name refuses before any
    write, naming the file, with the hint `abyssctl restart <name>`. → `TestRerender_OnlyChanged`,
    `TestRerender_BadSiteFile`
15. `Certificates` returns every pair with names, expiry, `UsedBy` and `Unused` (false for a Certificate a site file
    names even with no matching Hostname passed), and leaves out an unparseable `.crt`, a `.crt` without `.key` and
    a misnamed file, with a warning each. → `TestCertificates`
16. `RemoveCertificate` of a Certificate that is the only cover of a Hostname (passed in, or from a site file) is
    refused naming that Hostname; of an unknown id is refused with the hint `abyssctl cert list`; of a covered one
    re-renders its site files, reloads, then deletes both files; of an unused one deletes them without a reload.
    The plan warns "the key file is deleted; keep a copy if you need it". → `TestRemoveCertificate` (table),
    `TestPlanRemoveCertificate`
17. `AddSite` for an uncovered Hostname is refused (exit 2) with `errors.Is(err, ErrNotCovered)` and the `cert
    install` hint, and writes nothing; with no `proxy.env` it is refused with the hint `abyssctl host setup`; for a
    covered one it writes `render.Site`'s bytes, validates and reloads; unchanged bytes do nothing; a stopped Proxy
    is started and made ready first. `RemoveSite` deletes and reloads, and a missing file is a no-op. →
    `TestAddSite`, `TestRemoveSite`
18. A site or Certificate change reloads without recreating the Proxy (the container keeps its ID), and starts a
    stopped Proxy with `ComposeUp` and waits until it is ready before the reload. → `TestCertChange_StartsStoppedProxy`
19. `PlanX` and `X` return the same refusal for each refused case, and no `Plan…` writes a file, pulls or makes a
    changing Docker call. → `TestPlanMatches` (table)
20. No event, row, error or warning from any operation contains a byte of the private key's PEM or DER, and event
    values with spaces are quoted. → `TestNoKeyMaterial`, `TestEvents_Format`
21. `Probe` sends the Hostname as SNI and Host header to `ProbeAddr`, returns any status, cuts a body over 1 MiB and
    sets `Truncated`, follows no redirect, and returns `ErrUnreachable` with nothing listening, `ErrHandshake` when
    the server rejects the handshake and `ErrWrongCertificate` when the served leaf does not cover the Hostname. →
    `TestProbe` (table)
22. The upgrade target rule gives the table's result for each row, including a lookup error ignored when `<v>`
    equals the pin, with the newer-than-tested warning. → `TestUpgradeTarget` (table)
23. `Upgrade` pulls `repo:v@digest`, validates with `Run` mounting `proxy/` read-only at `/etc/caddy`, and only
    then rewrites `proxy.env` and `compose.yaml` and recreates the container with `ForceRecreate`; a pull or
    validation failure changes no file. → `TestUpgrade_Happy`, `TestUpgrade_ValidateFails`, `TestUpgrade_PullFails`
24. A new Proxy that does not become ready gets the old `proxy.env` and `compose.yaml` back and the old image up,
    and `Upgrade` exits 1 naming both versions, with the failed container's log lines as `caddy` events; when the
    old one does not come back either, exit 4 with the hint `abyssctl logs proxy`. → `TestUpgrade_PutsOldBack`,
    `TestUpgrade_OldFailsToo`
25. After an `Upgrade` cut off between writing `proxy.env` and the recreate, `Ensure` renders the compose file from
    the new `proxy.env` and brings that version up. → `TestEnsure_FinishesInterruptedUpgrade`
26. `Status` reports set-up, running, the version, the image and `Behind` when `Pins.Caddy` is newer. →
    `TestStatus`
27. `cert install` shows the panel and asks nothing, `--dry-run` prints it and changes nothing; `cert remove` asks
    y/N, refuses with no TTY and no `--yes`, and `--dry-run` changes nothing. → `TestCertInstall_Panel`,
    `TestCertInstall_DryRun`, `TestCertRemove_Confirm`, `TestCertRemove_DryRun` in `internal/cli`; the Hostnames
    both pass from `store.List` are checked by cli.md's `TestCertVerbs_HostnamesUnderLock`
28. `cert list` prints the columns id, names, expires (local date and zone) and used by, maps `UsedBy` Hostnames to
    Instance names, and shows "unused". → `TestCertList_Columns`, `TestCertList_MapsHostnames` in `internal/cli`
29. `internal/proxy`'s direct imports are only those in the import rule, only the listed packages import it, and
    `certtest` appears in no non-test build. → `TestImportRules` in `internal/archtest`
30. Journey 1: the Proxy runs ready with no site files, and an HTTPS request to it fails (connection closed or
    handshake error). → journey 1 (checked by host.md's journey slice)
31. Journey 3: the Proxy runs `read_only` with only `NET_BIND_SERVICE`. → journey 3 (checked by lifecycle.md's
    journey slice)
32. Journey 4: `host setup --cert/--key` with a wildcard from a throwaway test CA covering the test Base domain,
    then two Instances, each reached through the Proxy with `curl --resolve <hostname>:443:127.0.0.1 --cacert <test
    CA>`; a Hostname outside every installed Certificate fails the handshake; a covered Hostname with no site
    completes it and gets an empty 200; port 80 redirects to HTTPS. → journey 4 (checked by lifecycle.md's journey
    slice)
33. Journey 4: `cert install` of a Certificate that covers no Instance succeeds with the warning "covers no
    Instance yet"; `create` with a Hostname no Certificate covers is refused (exit 2) before anything starts; `cert
    list` shows the wildcard used by both Instances and the other Certificate as unused; `cert remove` of the
    wildcard is refused, of the unused one deletes its files. → journey 4 (checked by lifecycle.md's journey slice)
34. Journey 4: a renewed wildcard from the test CA replaces the old one, and both Hostnames serve the new
    Certificate. → journey 4 (checked by lifecycle.md's journey slice)
35. Journey 4: with `proxy.env` seeded to the older cached Caddy, `proxy upgrade` moves the Proxy to the pin and
    both Hostnames answer afterwards; `proxy upgrade` to an older version is refused (exit 2). → journey 4 (checked
    by versions.md's journey slice)
36. Docs: the README Quickstart line, `certificates.md`, `upgrades.md`, `security.md` and the three `cert`
    reference pages say what [Docs it changes](#8-docs-it-changes) lists. → (review)
