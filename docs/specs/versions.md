# Spec: versions

Build step 9 of 16. The [overview](README.md) has the module map, the call graph and the Host layout. Shared
packages (`internal/hostfs`, `internal/op`, `internal/buildinfo`) are in [foundation.md](foundation.md); the Docker
adapter, image names and locks are in [docker-and-locks.md](docker-and-locks.md); the Instance record is in
[store.md](store.md); the Proxy and its upgrade rule are in [proxy.md](proxy.md); `create` is in
[lifecycle.md](lifecycle.md). Capitalised terms are defined in [GLOSSARY.md](../../GLOSSARY.md).

## 1. Purpose

Build the Versions module (`internal/versions`) and the verbs that need it: `image prune` and `proxy upgrade`, and
the final form of `create`'s version. Versions owns:

- **which Actual and Caddy versions exist**: `Latest(repo)` finds the newest stable version in an image repo, and
  `Resolve(repo, version)` turns one stable version into the digest it is pinned by;
- **the stable-version rule**: a version is a plain `X.Y.Z`; pre-releases, `latest`, `nightly`, `edge`, `-alpine`
  and every other tag are never versions;
- **the registry lookups**: the registry's Distribution API with the anonymous bearer-token flow, so the same code
  works against Docker Hub and the journeys' local `registry:2`, and no lookup ever downloads an image;
- **`image prune`**: finding the Actual and Caddy images no [Instance](../../GLOSSARY.md) and no
  [Proxy](../../GLOSSARY.md) uses, and removing only those.

Versions is **stateless**. It keeps nothing on disk and caches nothing between runs. The
[Health check](../../GLOSSARY.md) stores the latest versions it found in `health.json` ([health.md](health.md)),
and `status` and the [MOTD](../../GLOSSARY.md) read them there. The Health check refreshes them once a day, not
on every `HEALTH_INTERVAL` run; health.md owns that cadence. [Upgrade](../../GLOSSARY.md) and `create` always look
up afresh.

The new tool version notice and the downgrade refusal are not here: Abyss [Releases](../../GLOSSARY.md) live on
GitHub, not in an image registry. The downgrade refusal is [host.md](host.md)'s and the exit 4 for a newer
`TOOL_VERSION` is [lifecycle.md](lifecycle.md)'s; the notice is on the roadmap.

When this spec is done, `sudo abyssctl create kids` creates an Instance on the latest stable Actual,
`sudo abyssctl proxy upgrade` moves the Proxy as [proxy.md](proxy.md)'s rule says, and `sudo abyssctl image prune`
frees the space old versions take. Journey 4's `proxy upgrade` and `image prune` steps pass.

## 2. Interface

Signature sketches, not final code.

```go
package versions

type Deps struct {
    RegistryURL string        // buildinfo.RegistryURL, e.g. "https://registry-1.docker.io"
    HTTP        *http.Client  // nil: the production client (Go's default transport, 30 s per request)
    Docker      docker.Client // used only by image prune
    ActualRepo  string        // buildinfo.ActualRepo
    CaddyRepo   string        // buildinfo.CaddyRepo
    Emit        op.Emitter
}

func New(d Deps) *Versions

// --- The stable-version rule. ---

// Stable reports whether v is a stable version: buildinfo.PlainVersion(v) (foundation.md), the rule
// lifecycle.CheckVersion and the record's ACTUAL_VERSION (store.md) also use.
func Stable(v string) bool

// --- Lookups. Read-only: no file, no Docker call, no lock; safe inside any Plan…. ---

// Latest returns the highest stable version among repo's tags (buildinfo.CompareVersions order) and its digest.
func (v *Versions) Latest(ctx context.Context, repo string) (buildinfo.ImagePin, error)

// Resolve returns the digest that repo's tag <version> points to. version must be Stable.
func (v *Versions) Resolve(ctx context.Context, repo, version string) (buildinfo.ImagePin, error)

var (
    ErrNotStable       = errors.New("not a stable version")        // wrapped in op.Refused; no request is made
    ErrNoSuchVersion   = errors.New("version not in the registry") // wrapped in op.Refused
    ErrNoStableVersion = errors.New("no stable version in the registry")
    ErrNeedsLogin      = errors.New("the registry asks for credentials")
)

// --- image prune. ---

type PruneRequest struct {
    InUse  []string // full references that must stay: every Instance's pin and the Proxy's image
    Before []string // Prune only: the InUse the panel was planned from; nil means the same as InUse
}

type Unused struct {
    Image docker.Image
    Refs  []string // its references in ActualRepo or CaddyRepo: tags first, then digests, sorted
}

// PlanPrune lists the unused images and returns the panel. No lock, no removal.
func (v *Versions) PlanPrune(ctx context.Context, r PruneRequest) (op.Summary, error)

// Prune removes the images the panel listed that are still unused. Precondition: the caller holds the Host lock
// and every Instance lock, and built InUse under them.
func (v *Versions) Prune(ctx context.Context, r PruneRequest) (PruneResult, error)

type PruneResult struct {
    Removed []Unused  // every reference removed and the image gone
    Skipped []Skipped // left in place, with why
    Freed   int64     // bytes, an upper bound: the Size of each image in Removed (layers may be shared)
}

type Skipped struct {
    Unused
    Reason string // "in use by a container", "in use by an Instance or the Proxy", or "still tagged in another repo"
    // A skipped image may have lost some of its Actual or Caddy references before the one that was refused.
}
```

`internal/versions/registrytest` is a test helper, imported only by tests: `New(t, Options{Auth string,
OmitDigestHeader bool, PageSize int})` starts an `httptest` server speaking the part of the Distribution API
Versions uses. `Auth` is `"none"` (like `registry:2`), `"bearer"` (an anonymous token realm, like Docker Hub) or
`"basic"`. It has `(*Registry).Push(name, tag string, manifest []byte, mediaType string)`, which stores the
manifest and computes its digest, `ExpireTokens()`, `FailNext(status int)` and `Requests()`. Its `URL()` goes in
`Deps.RegistryURL`. Upgrade's and Health's module tests and the CLI tests use it too.

### Invariants and error modes

- **Lookups never pull and never write.** They are plain HTTPS requests. A `Plan…` anywhere may call them
  ([foundation.md](foundation.md)'s plan rule allows a registry query), so `create --dry-run` and
  `proxy upgrade --dry-run` resolve their target without downloading it.
- **Only stable versions come out.** `Latest` ignores every tag that is not `Stable`. `Resolve` refuses a version
  that is not `Stable` with `op.Refused` wrapping `ErrNotStable`, before any request.
- **Refusals are what the Operator can fix by typing something else.** `ErrNotStable` and `ErrNoSuchVersion`
  are `op.Refused` (exit 2): "actual-server 26.13.0 is not in the registry". Everything else (no network, a
  timeout, a 429 rate limit, a 5xx, a malformed answer, `ErrNoStableVersion`, `ErrNeedsLogin`) is a plain error
  naming the repo, exit 1.
- **Anonymous only.** Versions never sends credentials and never reads any; the bearer token it gets is kept in
  memory, per repo, for the life of the `Versions` value, and never appears in an event, error or log line.
- **The digest is the one Docker records.** `Resolve` returns the digest of whatever the tag points to: for both
  repos, a multi-arch index. It is the digest `docker image inspect` lists in `RepoDigests` after
  `docker pull <repo>:<version>@<digest>`, so an Instance record's `ACTUAL_IMAGE_DIGEST` and `proxy.env`'s
  `PROXY_DIGEST` match what Docker shows.
- **`image prune` touches only Actual and Caddy images**, never forces a removal, and never removes an image in
  `InUse`. An image another container still uses (an [Orphan](../../GLOSSARY.md), or something else on a shared
  Host) is skipped, because Docker refuses to remove its last reference; it may lose its other Actual or Caddy
  references first, which a later pull by digest restores. An image that also carries a reference in another repo
  loses only its Actual or Caddy references and stays.
- **Both image stores.** These rules hold on Docker's classic image store and on the containerd image store, the
  default on a fresh recent Engine: the digest `Resolve` returns is the one either store lists in `RepoDigests`, and
  `image rm` removes references the same way. `TestRegistryContract` and the prune check run on both.

## 3. Behaviour

### The registry name

A repo carries its registry host (`buildinfo.ActualRepo` is `docker.io/actualbudget/actual-server`). The name
used in the API is the repo normalised ([docker-and-locks.md](docker-and-locks.md)'s `NormalizeRepo`) with its
first path element removed: `docker.io/library/caddy` → `library/caddy`, `127.0.0.1:5000/actualbudget/actual-server`
→ `actualbudget/actual-server`. Requests go to `Deps.RegistryURL`, never to the host in the repo name; the journey
build stamps both to the same local registry ([testing.md](testing.md)).

### Requests and the anonymous token

Each request is a `GET` or `HEAD` to `<RegistryURL>/v2/<name>/…` with the request's own context and a 30 s
limit:

1. Send the request, with `Authorization: Bearer <token>` when a token for this name is held.
2. **200** (or 404 for a manifest): done. A local `registry:2` answers here, with no auth.
3. **401** with `WWW-Authenticate: Bearer realm="…",service="…",scope="…"`: `GET <realm>?service=<service>&scope=
   repository:<name>:pull` with no credentials, read `token` (or `access_token`) from the JSON answer, hold it
   for this name, and send the request once more. A 401 to a request sent with a held token (it expired) drops
   the token and gets a fresh one the same way, once. A 401 to a request sent with a fresh token is an error. When `RegistryURL` is `https`, a realm
   that is not `https` is an error.
4. **401** with any other challenge (`Basic`): `ErrNeedsLogin`.
5. **429**: "the registry is rate-limiting this Host; try again later". Any other status: an error with the status
   and the repo.

A response body is read up to 8 MiB; more is an error.

### `Latest(repo)`

1. `GET /v2/<name>/tags/list?n=1000`, then follow each `Link: <…>; rel="next"` header (a relative link is
   resolved against `RegistryURL`), up to 100 pages; more is an error.
2. Keep the tags that are `Stable`. None → `ErrNoStableVersion`.
3. Take the highest by `buildinfo.CompareVersions`.
4. `Resolve(repo, highest)`.
5. Event `latest repo=docker.io/actualbudget/actual-server version=26.11.0 digest=sha256:…`.

### `Resolve(repo, version)`

1. Not `Stable` → refused (`ErrNotStable`), no request.
2. `HEAD /v2/<name>/manifests/<version>` with these four `Accept` values, in this order:
   `application/vnd.oci.image.index.v1+json`, `application/vnd.docker.distribution.manifest.list.v2+json`,
   `application/vnd.oci.image.manifest.v1+json` and `application/vnd.docker.distribution.manifest.v2+json`.
   A `HEAD` is not counted as a pull by Docker Hub.
3. 404 → refused (`ErrNoSuchVersion`).
4. 200 → the `Docker-Content-Digest` header, which must be `sha256:` and 64 lowercase hex digits. When the header is
   missing, `GET` the same manifest with the same `Accept` values and take the SHA-256 of the body as it came.
5. Event `resolve repo=… version=26.11.0 digest=sha256:…`. Return `ImagePin{Version, Digest}`.

### What the CLI does with Versions

The CLI builds one `Versions` per run with `buildinfo.RegistryURL`, `ActualRepo` and `CaddyRepo`, and nil `HTTP`.

| Verb | Lookup | Locks | Prompt | `--dry-run` | Exit |
|---|---|---|---|---|---|
| `create <name> [--version X.Y.Z]` | `Resolve(ActualRepo, v)`, or `Latest(ActualRepo)` | Host ([lifecycle.md](lifecycle.md)) | none | yes | 0, 1, 2 |
| `proxy upgrade [<version>]` | `Resolve(CaddyRepo, v)`, or `Latest(CaddyRepo)` | Host | y/N | yes | 0, 1, 2, 4 |
| `image prune` | none | Host, then every Instance lock in name order | y/N | yes | 0, 1, 2, 4 |

Every lookup runs before any lock is taken. With no TTY and no `--yes`, a prompt refuses (exit 2). Both new verbs
are changing verbs and write the activity log ([cli.md](cli.md)).

**`create`.** The CLI calls `lifecycle.CheckVersion` on `--version` first, so a pre-release such as
`26.11.0-beta.1` (or `v26.11.0`, `26.11`) is refused (exit 2) before any request. With `--version` it calls
`Resolve(ActualRepo, v)`; without it, `Latest(ActualRepo)`, always afresh, never from `health.json`. The
`ImagePin` becomes `CreateRequest.Version` and `Digest`, and everything after that is
[lifecycle.md](lifecycle.md)'s. A version the registry does not have is refused (exit 2); any other lookup failure
is exit 1; either way nothing is created and no lock was taken. `--version` is optional.

**`proxy upgrade [<version>]`.** At most one argument; more is refused (exit 2). A Proxy that is not set up is
refused (exit 2) by [proxy.md](proxy.md)'s plan, hint `abyssctl host setup`.

1. Read Settings and build the Proxy as `host setup` does.
2. With `<version>`: `Resolve(CaddyRepo, version)`; without: `Latest(CaddyRepo)`. The CLI passes the `ImagePin`
   as `UpgradeRequest.Found`, or the error as `LookupErr`, with `Version` as typed. It never acts on the error
   itself: [proxy.md](proxy.md)'s target rule decides. With no version, a failed lookup falls back to the compiled
   Caddy pin when that is newer than the Proxy, and is refused otherwise; with a version, a failed lookup is
   refused unless the version is the compiled pin's. An older version is refused, the same version is a no-op.
3. `proxy.PlanUpgrade(request)` → the panel. `--dry-run` stops here. `NoOp` prints the panel and exits 0.
4. y/N (`--yes` skips it).
5. Take the Host lock, then `proxy.Upgrade(request)`, which checks again.

**`image prune`.**

1. Build `InUse` from `store.List()` with no lock, refusing first, with nothing removed:
   - an invalid record: `op.NeedsOperator` (exit 4), the message naming the file, line and problem, hint
     "fix <file> by hand";
   - `UPGRADE_BACKUP` set: `op.NeedsOperator` (exit 4), hint `abyssctl upgrade <name>`: the Rollback needs the old
     image, which no record names;
   - `backup.Interrupted(name)`: exit 4, hint `abyssctl restore <name>`, for the same reason.

   Otherwise `InUse` holds each record's `Record.Image(ActualRepo)` and, when `proxy.Status` says the Proxy is set
   up, `Status.Image`. A `proxy.Status` error (an invalid `proxy.env`, exit 4) passes through as it is.
2. `PlanPrune` → the panel; `NoOp` ("no unused Actual or Caddy images") exits 0. `--dry-run` stops here.
3. y/N (`--yes` skips it).
4. Take the Host lock, then every Instance lock in name order (as `host setup` does), so no `create`, `start`,
   `upgrade` or `restore` can pull or switch to an image while the images are removed. It waits up to 30 s for
   each lock, then gives up (exit 1) naming the busy Instance, or the Host lock, with nothing removed.
5. Build `InUse` again from `store.List()` under the locks, with the same refusals, and call `Prune` with it and
   with step 1's set as `Before`. An image that became in use since the panel is skipped, not removed. This is the
   same pattern as `cert install` ([cli.md](cli.md)).

The outcome line: "✓ 3 images removed, up to 1.4 GB freed", plus "1 skipped" with each skip's reason as a log line.
A skip alone is still exit 0. Any other removal failure is logged, the rest go on, and the exit is 1.

### `PlanPrune` and `Prune`

1. **Keep set.** For each `InUse` reference, `Docker.Image(ref)`; a present image's ID is kept. A reference not
   present locally keeps nothing. `Prune` builds one keep set from `Before`, for the candidates, and one from
   `InUse`.
2. **Candidates.** `Docker.ListImages`: each image with at least one `RepoTags` or `RepoDigests` entry whose repo
   is `SameRepo` as `ActualRepo` or `CaddyRepo`, and whose ID is not kept. An image with neither tags nor digests
   is never a candidate: its repo can't be known.
3. **The panel.** Title "Remove unused images"; rows "images" (count) and "space" ("up to" the total `Size`,
   human-readable, since images can share layers); one step per image, naming its repo and its version: its first
   tag's, else its `org.opencontainers.image.version` label, else its short digest and `Created` date, with its size
   ("remove docker.io/actualbudget/actual-server 26.10.0 (≈512 MB)"). An image pulled as `<repo>:<version>@<digest>`
   keeps only its digest reference, so the label is the usual source; the warning "a Restore of a Backup made with a removed version downloads it again,
   which needs the network". `NoOp` when there is no candidate.
4. **`Prune`** builds the candidate list again from `Before` (it never takes a `Summary`), then keeps aside each
   candidate whose ID `InUse`'s keep set now holds: `Skipped` "in use by an Instance or the Proxy", event
   `skipped image=<ref> reason=in_use_record`. For each other candidate it calls `RemoveImage` on each of its `Refs` in
   order, never forced:
   - `ErrConflict` (a container uses it): stop on this image, `Skipped` "in use by a container"; event
     `skipped image=… reason=in_use_container`;
   - `ErrNotFound` on a later reference: the image went with an earlier one; carry on;
   - after its last reference, `Docker.Image(ID)`: gone → `Removed`, event `removed image=… size=…`; still there
     (it is tagged in another repo) → `Skipped` "still tagged in another repo";
   - any other error: emitted, joined to the returned error, and the next image is tried.

## 4. Files and formats it owns

No file on the Host. Versions owns:

- **The stable-version rule** (`Stable`, above).
- **The registry lookups**: the requests, the token flow and the limits in [Behaviour](#3-behaviour).
- **The `image prune` panel and outcome line**, in [cli.md](cli.md)'s style.

### Package layout

```
internal/versions/
  versions.go   New, Deps, Stable, the sentinels
  registry.go   requests, token flow, paging, Latest, Resolve
  prune.go      PlanPrune, Prune
  registrytest/ the httptest Distribution API
internal/cli/
  image.go      image prune
  proxy.go      proxy upgrade (beside the cert verbs)
  create.go     create's version lookup
```

## 5. Depends on

- [foundation](foundation.md): `buildinfo.ImagePin`, `buildinfo.PlainVersion`, `buildinfo.CompareVersions`, `buildinfo.RegistryURL`,
  `ActualRepo` and `CaddyRepo`; `op.Emitter`, `op.Summary` and the typed errors.
- [docker-and-locks](docker-and-locks.md): `docker.Client` (`Image`, `ListImages`, `RemoveImage`), `docker.Image`,
  `NormalizeRepo`, `SameRepo`, `ErrConflict`, `ErrNotFound`; the CLI's `locks.Locker` for `image prune` and
  `proxy upgrade`.
- [store](store.md): `List`, `Record.Image`, `Record.UpgradeBackup`, read by the CLI for `image prune`.
- [proxy](proxy.md): `Status`, `UpgradeRequest`, `PlanUpgrade`, `Upgrade` and the target rule, called by the CLI.
- [lifecycle](lifecycle.md): `CheckVersion`, `CreateRequest`, called by the CLI.
- [backup](backup.md): `Interrupted`, called by the CLI for `image prune`.
- [host](host.md): `ReadSettings`, and building the Proxy the way `host setup` does.

[Upgrade](upgrade.md) calls `Latest` and `Resolve` for its target, and [Health](health.md) calls `Latest` for both
repos and stores the results as `health.json`'s latest versions; how often is health.md's.

Import rule this spec adds to `internal/archtest`: `internal/versions` imports only the standard library,
`internal/op`, `internal/buildinfo` and `internal/docker`. Only `internal/cli` and `internal/upgrade` import it;
Health calls `Latest` through a seam the CLI fills; `internal/versions/registrytest` is imported only by tests.

## 6. Decisions

- **The registry API, not `docker pull`, for lookups.** Learning a version's digest needs no download, so a
  `Plan…` and `--dry-run` can resolve a target without changing the Host, and Docker Hub does not count a
  `HEAD` against its pull limits. Upgrade and `create` still pull through Docker, by `<repo>:<version>@<digest>`.
- **Anonymous bearer tokens only.** Both images are public, so no credential is ever stored on the Host, and the
  same flow works against Docker Hub and, with no auth at all, the journeys' `registry:2`.
- **Stateless.** Versions keeps no cache file. Health writes what it found to `health.json`, so there is one
  writer, and `create` and Upgrade always ask afresh, so they never act on a stale answer.
- **Only plain `X.Y.Z` is a version.** Pre-releases, `latest`, `nightly`, `edge` and variant tags such as
  `-alpine` are never offered or accepted; Actual's nightly and edge builds are on the roadmap.
- **Pins are digests of what the tag points to** (the multi-arch index), the same digest Docker records, so a
  record, `proxy.env` and `docker image inspect` always agree.
- **A failed Caddy lookup is decided by Proxy's rule**, not by the CLI: with no version it falls back to the
  compiled pin when that is newer than the Proxy, because the pin needs no lookup; otherwise it refuses, because
  Proxy never guesses a version it can't pin by digest.
- **`image prune` plans unlocked, then takes the Host lock and every Instance lock and checks again**, so the panel
  and the prompt hold no lock, as every verb's do ([cli.md](cli.md)), and an image that came into use meanwhile is
  skipped. It refuses while an Upgrade or Restore is interrupted, because the images those need are named by no
  record, and Rollback must never need the network. It never forces a removal and never touches another repo's images, so a shared Host's other containers are safe.
- No new dependency: `net/http` and `encoding/json` are enough.

## 7. Later

- Actual's nightly and edge versions (`--version nightly`), which the stable-version rule leaves out in v1.

## 8. Docs it changes

- README **Requirements**: outbound HTTPS to Docker Hub (`registry-1.docker.io` and `auth.docker.io` for
  lookups; pulls also need Docker's download CDN, see Docker's allowlist doc). **Quickstart**'s `create` line needs no `--version`.
- `docs/guides/upgrades.md`: how Abyss finds the latest stable Actual and Caddy (plain `X.Y.Z` only; pre-releases,
  nightly, edge and `-alpine` never), that `create` takes the latest unless `--version` is given, what
  `image prune` removes and keeps, that it waits for every Instance's lock, and that a Restore of a Backup made
  with a pruned version downloads it again.
- `docs/guides/security.md`: version lookups are anonymous; Abyss stores no registry credentials.
- Reference: the `image prune` and `proxy upgrade` pages, and `create`'s optional `--version`, from their help
  text.

## 9. Acceptance list

Module tests are in `internal/versions`, with `registrytest`, `dockerfake` and an `op.Recorder`, without root,
Docker or network. CLI tests are in `internal/cli`, with `registrytest`, fake Proxy, Lifecycle and Backup seams
and real locks on a temp Data root. This spec's journey slice is journey 4's `proxy upgrade` and `image prune` steps.

1. `Stable` accepts `26.11.0` and `2.10.2` and rejects `v26.11.0`, `26.11`, `26.11.0-beta.1`, `26.11.0+x`,
   `026.1.0`, `latest`, `nightly`, `edge` and `26.11.0-alpine`. → `TestStable` (table)
2. The registry name drops the registry host after normalising: `docker.io/library/caddy` → `library/caddy`,
   `docker.io/actualbudget/actual-server` and `127.0.0.1:5000/actualbudget/actual-server` →
   `actualbudget/actual-server`; every request goes to `RegistryURL`. → `TestRegistryName` (table)
3. `Latest` follows `Link` pages, ignores every non-stable tag, returns the highest stable one by SemVer order
   (`26.10.0` beats `26.9.3`) with its digest, and gives `ErrNoStableVersion` when there is none. → `TestLatest`
   (table)
4. `Resolve` sends `HEAD` with the four manifest media types and returns `Docker-Content-Digest`; with no header it
   `GET`s the manifest and returns the SHA-256 of the body. → `TestResolve`, `TestResolve_NoDigestHeader`
5. `Resolve` of a missing tag is refused (exit 2, `ErrNoSuchVersion`); of a non-stable version it is refused
   (`ErrNotStable`) with no request made. → `TestResolve_Refusals` (table)
6. A 401 Bearer challenge gets one anonymous token request with the challenge's `service` and the name's pull
   `scope`, then one retry; the token is reused for the next request to that name; an expired held token is
   refreshed once; a 401 after a fresh token is an error; a `Basic` challenge is `ErrNeedsLogin`; an `http` realm for an `https` registry is an error. → `TestAuth` (table)
7. A 429, a 5xx, a timeout, malformed JSON, a body over 8 MiB, more than 100 pages and a bad digest header each
   give a plain error (exit 1) naming the repo. → `TestLookupErrors` (table)
8. Lookups make no Docker call and write no file, and no event or error holds the token. →
   `TestLookups_NoSideEffects`, `TestEvents_NoToken`
9. `PlanPrune` lists exactly the Actual and Caddy images whose ID no `InUse` reference resolves to, ignores
   images of other repos and images with no tag or digest, and is `NoOp` when there is none; its panel has the
   count, the space ("up to"), one step per image named by tag, else version label, else short digest and date,
   and the re-download warning. → `TestPlanPrune` (table)
10. `Prune` removes each candidate's references, tags first, never forced; `ErrConflict` skips the image as "in
    use by a container" (`reason=in_use_container`), after the references removed before it (the dockerfake drops a non-last reference of a
    used image, as Docker does); `ErrNotFound` on a later reference is not a failure; an image still tagged in another
    repo is skipped and kept; a candidate of `Before` whose ID `InUse` now keeps is skipped with `skipped
    image=<ref> reason=in_use_record` and no `RemoveImage` call; `Freed` sums the removed images' sizes; other errors are joined and the next image
    is tried. → `TestPrune` (table)
11. `PlanPrune` removes nothing and returns what `Prune` then does for the same images. → `TestPlansMatchRuns`
12. `internal/versions`'s direct imports are only those in the import rule, and only `internal/cli` and
    `internal/upgrade` import it; Health calls `Latest` through a seam the CLI fills. → `TestImportRules` in `internal/archtest`
13. `create` refuses a pre-release, `v…` or `X.Y` (exit 2) before any request; with `--version` it calls
    `Resolve`, without it `Latest`; it passes the pin to `lifecycle.Create`; a missing version is exit 2 and any
    other lookup failure exit 1, each with no lock taken and nothing created; `create --dry-run` looks up but
    never pulls. → `TestCreateCmd_ResolvesVersion` (table) in `internal/cli`, which replaces lifecycle.md's
    `TestCreateCmd_InterimVersion`
14. `proxy upgrade` calls `Latest(CaddyRepo)` with no argument and `Resolve(CaddyRepo, v)` with one, passes the
    result or the error in `UpgradeRequest` without acting on it, looks up before the Host lock, asks y/N
    (`--yes` skips it; no TTY and no `--yes` is exit 2), and with `--dry-run` takes no lock and changes nothing;
    more than one argument and a Proxy that is not set up are refused (exit 2). →
    `TestProxyUpgradeCmd` (table) in `internal/cli`
15. `image prune` builds the panel from an unlocked `InUse` and asks y/N with no lock held, then takes the Host
    lock and every Instance lock in name order, builds `InUse` again and passes both sets to `Prune`, so an
    Instance created between the panel and the locks keeps its image; `InUse` is every record's pin plus the
    Proxy's image; it refuses an invalid record (exit 4, hint "fix <file> by hand"), `UPGRADE_BACKUP` set (exit 4,
    hint `abyssctl upgrade <name>`) and an interrupted Restore (exit 4, hint `abyssctl restore <name>`) with
    nothing removed, before and under the locks; a `proxy.Status` error passes through with its own exit code; a
    lock still busy after 30 s is exit 1 naming it; `--yes` skips the prompt and `--dry-run` takes no lock; skips
    alone exit 0, a failed removal exits 1. → `TestImagePruneCmd` (table) in `internal/cli`
16. Against the journeys' local `registry:2`, `Latest` and `Resolve` return the digest that Docker lists in
    `RepoDigests` after pulling `<repo>:<version>@<digest>`, and removing one of a used image's references
    behaves as the dockerfake does. → `TestRegistryContract` in the journey binary ([testing.md](testing.md)), on
    the CI runners and on the Release check VMs' fresh Engine (containerd image store)
17. Journey 4: with `proxy.env` seeded to the older cached Caddy, `proxy upgrade --yes` moves the Proxy to the
    pin and both Hostnames answer afterwards; `image prune --yes` then removes the old Caddy image and keeps the
    pin's; a second `proxy upgrade` is a no-op (exit 0); `proxy upgrade <older>` is refused (exit 2);
    `proxy upgrade --dry-run` changes nothing. → journey 4
18. Journey 6: after the Restore to the older version, `image prune --yes` removes the Actual images no Instance
    uses (the pin's and the two fake tags'), keeps the older (used by `kids`) and the Proxy's image, and leaves
    every image of another repo. → journey 6 (checked by
    upgrade.md's journey 6 slice)
19. On a Release check Host, against Docker Hub, `create` with no `--version` gets the latest stable Actual and
    `proxy upgrade --dry-run` either shows a target no older than the pin or says the Proxy is on the latest,
    through the anonymous token flow; `TestRegistryContract` and an `image prune` check pass on the fresh Engine. → the Release check (checked by
    release.md's Release check)
20. Docs: the README lines, `upgrades.md`, `security.md` and the reference pages say what
    [Docs it changes](#8-docs-it-changes) lists. → (review); the reference pages also by the generated-reference
    check in CI
