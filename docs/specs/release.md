# Spec: release

Build step 15 of 16. The [overview](README.md) has the module map, the supported Hosts and the index.
Capitalised terms are defined in [GLOSSARY.md](../../GLOSSARY.md).

This spec owns how a [Release](../../GLOSSARY.md) of Abyss is made and published:

- the version and tag rules;
- the tag workflow, `.github/workflows/release.yml`: tag → checks → journeys → build → draft Release;
- the Release assets: two bare binaries and `SHA256SUMS`;
- the Release notes, which are the changelog;
- the Release check: `make release-check` and `make release-check-manual` on the [Maintainer](../../GLOSSARY.md)'s test VMs;
- publishing: `make release-publish`;
- the repo-setup checklist.

A **draft Release** is a GitHub Release the tag workflow has created but the Maintainer has not yet published. Only
the Maintainer can see it, and it becomes a [Release](../../GLOSSARY.md) when it is published.

It links to what other specs own and does not restate it:

- [foundation.md](foundation.md) owns `internal/buildinfo`, the `-X` stamping and `make build`;
- [testing.md](testing.md) owns the journeys, the test image cache, `make journey` and `journeys.yml`;
- [host.md](host.md) owns install and Tool update through `host setup`, and the downgrade refusal;
- [versions.md](versions.md) owns the lookups of Actual and Caddy versions.

## 1. Purpose

Turn a commit on `main` into a published Release that an [Operator](../../GLOSSARY.md) can trust:

- every Release is built by CI from a tag, the same way a local `make build` builds;
- nothing is published until the checks on the tagged commit pass, the journeys pass on GitHub's runners, and the
  Release check passes on real VMs of every supported OS, including a fresh Docker install and one run against
  Docker Hub and Pushover;
- each Release carries the two binaries, a checksum file the Host can check with `sha256sum -c`, a build
  provenance attestation, and notes that say what changed and what the Release check found.

This spec is done when:

- pushing a `vX.Y.Z` tag on `main` ends in a draft Release with three assets, or in a failed workflow with no
  Release;
- `make release-check` runs every scripted step below against that draft and prints the summary table;
- `make release-check-manual` walks the Maintainer through the terminal steps and adds them to the summary;
- `make release-publish` publishes the draft only when the summary passes and is in the notes.

## 2. Interface

The release tooling is one program, `release/` (package `main`), run as `go run ./release <subcommand>`. The
interface its tests go through:

```go
// Cmd is one command the program runs, on the Mac or, through ssh, on a VM.
type Cmd struct {
    Name    string        // "ssh", "scp", "gh", "git", "make", "shasum", ...
    Args    []string      // never holds a Secret
    Stdin   []byte        // the only way a Secret reaches a child
    Env     []string      // added to the program's own environment; never holds a Secret
    Dir     string
    Timeout time.Duration // 0 means no limit
}

// Runner runs a Cmd. It returns combined stdout and stderr and the exit code; err is non-nil only when the
// command could not be started or ran out of time.
type Runner interface {
    Run(ctx context.Context, c Cmd) (out []byte, code int, err error)
}

// FakeRunner (in release/ _test.go files) records every Cmd and answers from a table keyed on Name and Args.
type FakeRunner struct {
    Calls   []Cmd
    Answers func(Cmd) (out []byte, code int, err error)
}

// Config is the parsed local config. Secret fields are never printed by %v (a String method masks them).
type Config struct {
    FreshHosts, DockerHosts, ApplianceHosts []string
    RealPathHost                            string
    ResetCmd                                []string // split on spaces; empty when unset
    PushoverUser, PushoverToken             string
}
func LoadConfig(path string) (Config, error)

type Role string // "fresh", "docker", "appliance"

type VM struct {
    Host     string // ssh host name; never written to a transcript header, result.json or the summary
    Role     Role
    Index    int    // 1-based within the role
    RealPath bool
}

// Plan returns the steps for one VM, in order: e.g. ["R1","R2","R3","R6","R7","R8"] for the real-path VM.
func Plan(vm VM) []string
// VMs returns the VMs to run, in run order; only, when not "", picks one ssh host name.
func VMs(c Config, only string) ([]VM, error)

// Result is one VM's (or the manual run's) outcome, written as result.json.
type Result struct {
    Schema   int          `json:"schema"`   // 1
    Role     string       `json:"role"`     // a Role, or "manual"
    Index    int          `json:"index"`
    OS       string       `json:"os"`       // e.g. "Ubuntu 24.04", "TurnKey Core 19 (Debian 13)"
    Arch     string       `json:"arch"`
    Docker   *DockerInfo  `json:"docker"`   // nil when Docker is absent
    Actual   string       `json:"actual"`   // the Actual version R6.3 got from Docker Hub; "" elsewhere
    Steps    []StepResult `json:"steps"`
    Journeys *TestCount   `json:"journeys"` // nil when R8 did not run
    At       time.Time    `json:"at"`
}
type DockerInfo struct{ Engine, Compose, Store string; Installed bool } // Store: "containerd" or "classic"
type StepResult struct{ ID string; OK bool; Error string }               // Error: first line, scrubbed
type TestCount struct{ Passed, Total int }

func Summary(results []Result, tag, commit string, now time.Time) (md string, pass bool)
```

## 3. Behaviour

### Versions and tags

- **SemVer tags.** A Release tag is `vX.Y.Z`: three numbers, no leading zeros, no pre-release part and no build
  part. The binary's `Version` is the tag without its `v` ([foundation.md](foundation.md)). Builds such as
  `0.2.0-dev.3` run on a test Host but are never tagged or published.
- **First and v1.** The first public Release is `v0.1.0`. `v1.0.0` marks the v1 capability list
  ([overview](README.md#v1-capabilities)) complete.
- **Which part moves.**
  - Before `1.0.0`: MINOR for a new capability, and for any change an Operator must act on or that breaks a
    script (a renamed verb or flag, a `--json` schema bump, a raised Docker minimum, a dropped supported Host);
    PATCH for everything else.
  - From `1.0.0`: MAJOR for a change that breaks a script or needs the Operator to act, as listed above; MINOR for
    a new capability or flag; PATCH for fixes, pin moves and docs.
  - Moving the Caddy pin alone is a PATCH. The notes say so, because the Proxy moves only through `proxy upgrade`.
- **Only forward.** A new tag must be higher than every existing `v*` tag, by `buildinfo.CompareVersions`.
- **Only from `main`.** The tagged commit must be on `main`'s history. The Maintainer tags with
  `git tag -a vX.Y.Z -m "Abyss X.Y.Z"` and pushes the tag. Only the Maintainer can create `v*` tags, and no one can
  move or delete them ([Repo setup](#repo-setup-checklist)).
- **A failed version is skipped.** When the Release check finds a bug, the draft is deleted, the fix lands on
  `main`, and the next PATCH version is tagged. The failed tag stays, because tags can't be deleted. A failure of
  the test VMs themselves (a reset that didn't work, a VM that didn't answer) is not a bug: the Release check is
  run again against the same draft.
- The tags are Release tags, not Go module versions. Nothing imports the module, and Abyss is never installed
  with `go install`.

### The tag workflow (`release.yml`)

`on: push: tags: ["v*"]`. Every job checks out the tagged commit, and the `tag`, `build`, `smoke` and `draft` jobs
then set up Go from `go.mod` (`actions/setup-go` with `go-version-file`). The top-level `permissions` is
`contents: read`; only the `draft` job gets more. Third-party actions are pinned to full commit SHAs, as in
[foundation.md](foundation.md). `concurrency` is the tag ref, so a tag never runs twice at once.

The jobs, in order:

1. **`tag`.** Checks out with full history and runs `go run ./release tag-check <tag>`, which refuses:
   - a tag that is not `vX.Y.Z` as above;
   - a commit that is not on `origin/main`'s history;
   - a version that is not higher than every other `v*` tag;
   - a tag that already has a published Release. (Its `contents: read` token can't see drafts; the `draft` job
     checks those.)

   Each refusal names the rule. Nothing else runs after a refusal.
2. **`ci`** (needs `tag`). Calls `ci.yml` as a reusable workflow (`on: workflow_call`, [foundation.md](foundation.md)): lint, module tests, build and the static-binary
   check on the tagged commit. `ci.yml` calls `journeys.yml` itself only for `main`, so here it does not.
3. **`journeys`** (needs `tag`, runs beside `ci`). Calls `journeys.yml` ([testing.md](testing.md)) with
   `version` set to the tag's version, `keep-binaries: true` and `secrets: inherit`. Both architectures run the
   journey binary's eleven tests. With `keep-binaries`, each job uploads its three journey binaries (the journey test binary,
   `abyssctl` and `abyssctl-0.0.1`) as the workflow artifact `journey-bin-<arch>`, kept 90 days. They are never
   Release assets: they are stamped for a local registry and a test Pushover, and would fail on a real Host.
4. **`build`** (needs `ci` and `journeys`):
   - `make build VERSION=<X.Y.Z>` builds `dist/abyssctl-linux-amd64` and `dist/abyssctl-linux-arm64`, with no
     `LDFLAGS_EXTRA`, so every other `buildinfo` variable keeps its release default;
   - in `dist/`, `sha256sum abyssctl-linux-amd64 abyssctl-linux-arm64 > SHA256SUMS`;
   - uploads the three files as the workflow artifact `release-assets`.
5. **`smoke`** (needs `build`), a matrix on `ubuntu-24.04` and `ubuntu-24.04-arm`. Each runner, as its
   unprivileged user, on its own architecture's binary:
   - `sha256sum -c --ignore-missing SHA256SUMS` passes;
   - `file` reports a statically linked executable;
   - `go run ./release release-binary-check <binary>` passes (below);
   - `abyssctl version --json` exits 0 and shows the tag's version, the tagged commit, `linux/<arch>` and the Caddy
     pin from `internal/buildinfo/pins.json`;
   - `abyssctl --help` exits 0.
6. **`draft`** (needs `smoke`). Permissions `contents: write`, `id-token: write` and `attestations: write`. It:
   - first runs `gh release view <tag>` and refuses, creating nothing, when any Release for the tag exists, draft
     or published (its token can see drafts);
   - attests both binaries with `actions/attest-build-provenance` (`subject-path` the two binaries);
   - writes the notes with `go run ./release notes <tag>` ([Release notes](#release-notes));
   - runs `gh release create <tag> --draft --verify-tag --title "Abyss X.Y.Z" --notes-file <notes>` with the
     three assets.

   The draft is never marked a pre-release, so once published it becomes the latest Release that the README's
   `releases/latest/download/…` links point at.

If any job fails, no Release exists for the tag. Re-running the workflow, all jobs or only the failed ones, is
safe: a re-run that skips the passed `tag` job still reaches the `draft` job's check, which refuses once a draft
exists.

**`release-binary-check`** passes only when both of these hold:

- **Build settings.** `go version -m <binary>` shows `-trimpath=true`, `CGO_ENABLED=0`, `GOOS=linux`, and a
  `GOARCH` that matches the file name. (With `-trimpath`, Go leaves `-ldflags` out of the build info, so the `-X`
  values can't be read there.)
- **Byte scan.** The binary's bytes contain none of the journey stamps' addresses, `127.0.0.1:5000` and
  `127.0.0.1:18089`, and contain all four release defaults from [foundation.md](foundation.md):
  `docker.io/actualbudget/actual-server`, `docker.io/library/caddy`, `https://registry-1.docker.io` and
  `https://api.pushover.net`.

`Version` and `Commit` are checked by `version --json` in the same job. Together, this is how a Release is known to
set only `Version` and `Commit` and keep the other four release defaults ([foundation.md](foundation.md)).

### Release assets

Every Release has exactly these three assets, and the names never change. The README's install block, the
Tool update in [host.md](host.md) and the Tool update command on the roadmap all depend on them:

| Asset | What it is |
|---|---|
| `abyssctl-linux-amd64` | the static binary for amd64, mode 0755 once downloaded and `chmod +x`'d |
| `abyssctl-linux-arm64` | the static binary for arm64 |
| `SHA256SUMS` | `sha256sum` output: two lines, `<64 hex>  <file name>`, amd64 first, no paths |

There is no archive, because each binary is one file. Integrity on the Host is `sha256sum -c --ignore-missing
SHA256SUMS` (coreutils), the check the README's install and update blocks use. The build provenance attestation
is an extra, checked off the Host with `gh attestation verify <file> --repo DocCyblade/abyssctl`. It is never
needed on a Host.

### Release notes

The GitHub Release notes are the changelog. There is no `CHANGELOG.md`.

`go run ./release notes <tag>` renders `release/notes.md.tmpl` (Go `text/template`) with the tag, the previous
`v*` tag, both pin files at the tagged commit, and the same files at the previous tag. The draft's notes have
these sections, in this order:

1. **What's changed.** The subjects of the non-merge commits since the previous tag, one `- ` line each, or "First
   public Release." for `v0.1.0`. Before publishing, the Maintainer rewrites them in plain words under `Added`,
   `Changed`, `Fixed` and `Security`, keeping only the headings that apply.
2. **Before you update.** One line always: "Updating recreates no Instance container except to fix drift; the
   Proxy may restart for a few seconds when this version renders it differently, and `host setup` says so before
   you confirm. Each Instance moves to this version's files on its next `start`, `restart`, `upgrade` or `restore`;
   `sudo abyssctl status` shows which are out of date." Then, only when changed since the previous tag:
   - "The Proxy's Caddy pin moves from A to B. Run `sudo abyssctl proxy upgrade` to move the Proxy.";
   - "This Release needs Docker Engine N or newer and Compose M or newer.";
   - a placeholder comment for anything else the Operator must do, which the Maintainer fills or deletes.
3. **Tested with.** The Caddy pin, the Docker minimums, and the journeys' Actual versions (the pin and the older,
   from `journey/testdata/pins.json`), plus "the latest stable Actual on Docker Hub, in the Release check".
4. **Release check.** A placeholder comment. The Maintainer pastes the Release check summary here.
5. **Install or update.** A link to the README's Install and Updating Abyss sections, the `sha256sum -c`
   reminder, and the optional `gh attestation verify` line.

Placeholders are HTML comments (`<!-- … -->`). A published Release has none ([Publishing](#publishing)).

### The Release check

The Release check gates publishing. It runs the draft's assets and the tag workflow's journey binaries on the
Maintainer's test VMs, from the Maintainer's Mac, so an agent can run the scripted part. It never runs in CI.

Both Make targets run the program `release/` (`go run ./release check …`), which drives `ssh`, `scp`, `gh` and
`make journey` through `os/exec`. Everything it writes goes under `dist/release-check/<tag>/`.

#### Prerequisites on the Mac

- A clean checkout at the tag: `HEAD` is the tagged commit and `git status` is clean.
- The Go toolchain from `go.mod`, and `make tools`.
- The GitHub CLI, logged in (`gh auth status`) as the Maintainer, with push access to the repo: draft Releases are
  visible only to accounts with push access. The same login downloads the workflow artifacts.
- A test image cache that matches the tagged commit's pins (`make image-cache`, [testing.md](testing.md)).
- `ssh <host>` reaching each VM with a key (`BatchMode=yes`), as a user with passwordless `sudo`.
- The local config file below.

#### The local config

The test VMs, how to reset them and the real Pushover pair live in one file outside the repo:
`~/.config/abyssctl/release-check.env`, or the path in `RELEASE_CHECK_CONFIG`. The program refuses the file unless
it is a regular file owned by the current user with mode 0600. It is `KEY=value` lines, like `abyssctl.env`.
Blank lines and `#` comments are allowed, and an unknown key is an error.

| Key | Required | What it holds |
|---|---|---|
| `FRESH_HOSTS` | yes | ssh host names of fresh VMs, space-separated: a supported OS at its reset snapshot with no Docker. Together they must cover Debian 13, Ubuntu 24.04, Ubuntu 26.04 and TurnKey Core 19. |
| `DOCKER_HOSTS` | yes | VMs of a supported OS that already have Docker at or above the minimums, and no containers or images at their reset snapshot. |
| `APPLIANCE_HOSTS` | yes | VMs of a supported OS where another web server already listens on 80 or 443. |
| `REAL_PATH_HOST` | yes | one of `FRESH_HOSTS`, running Debian 13. It runs the real-path steps, and is the manual steps' default host. |
| `RESET_CMD` | no | a command run as `$RESET_CMD <ssh host>`. It must put the VM back at its reset snapshot, start it, and exit 0 once `ssh` answers. 10-minute limit. |
| `PUSHOVER_USER` | yes | the Maintainer's real Pushover user key. |
| `PUSHOVER_TOKEN` | yes | the Maintainer's real Pushover app token. |

A host may appear in one role only. Hostnames, addresses, snapshot names and hypervisor credentials never enter
the repo: the Maintainer's `RESET_CMD` hook knows them, and the summary names each VM by its role and OS only.

**How the real Pushover pair reaches a VM.** The pair is a [Secret](../../GLOSSARY.md), and it travels like this:

- It lives only in the 0600 local config.
- The token goes to the VM only on the ssh session's stdin, to `abyssctl alerts setup --pushover-user <user>
  --pushover-token -`. It is never in a command line, an environment variable, a file the program writes, the
  transcript or the summary.
- The user key is a flag ([alerts.md](alerts.md)). The program masks it as `…abcd` wherever it shows or records a
  command line, and Abyss masks it in the VM's activity log.
- On the VM, the pair is only in `/etc/abyssctl/abyssctl.env` (root, 0600). The cleanup step deletes that file
  before the journeys run, and the journeys' `Blank` deletes `/etc/abyssctl/` again. The next run's `RESET_CMD`
  rolls the VM back anyway.

#### Preflight

`make release-check TAG=vX.Y.Z` first checks, before touching any VM:

- **P1.** The config is valid, as above.
- **P2.** The checkout is clean and at the tag, `gh auth status` passes, and the image cache key matches.
- **P3.** The tag has a draft Release whose assets are exactly the three above. The program downloads them with
  `gh release download` into `release/`, checks `SHA256SUMS` with `shasum -a 256 -c`, and checks each binary
  with `gh attestation verify --repo DocCyblade/abyssctl`.
- **P4.** The tag's `release.yml` run succeeded, and its `journey-bin-<arch>` artifact exists for each VM
  architecture. The program downloads it with `gh run download` into `journey-bin/<arch>/`.
- **P5.** It makes a throwaway test CA and a wildcard Leaf for `*.abyss.test` with `crypto/x509`, into `ca/`.
  They exist only for this check.

Any preflight failure stops the run with its message and exit 1, and no VM is touched.

#### Scripted steps, per VM

The VMs run one at a time, in this order: `FRESH_HOSTS`, `DOCKER_HOSTS`, `APPLIANCE_HOSTS`. `ONLY=<ssh host>` runs
just one VM, for a re-run. A failed step ends that VM's run and is recorded, and the program moves on to the next
VM. Each remote step has a time limit: 15 minutes for `host setup`, 75 minutes for the journeys, 10 minutes for
the others. Running out of time fails the step.

Every VM:

- **R1. Reset.** Run `RESET_CMD <host>` if it is set. Without it, the VM is used as it is.
- **R2. Download check.** Read `/etc/os-release` (and `/etc/turnkey_version` if present) and `uname -m`. Copy the
  matching release binary and `SHA256SUMS` to `/var/tmp/abyssctl-release-check/`. There, `sha256sum -c
  --ignore-missing SHA256SUMS` passes, `chmod +x` is done, and, as the unprivileged ssh user,
  `./abyssctl-linux-<arch> version --json` shows the tag's version and commit. This is the README's install block
  with `curl` replaced by `scp`, because a draft has no public download URL.

Then, by role:

- **R3. Fresh VM: Docker install.** Before: `docker` is not installed (otherwise the step fails with "Docker is
  already on this VM; reset it to its reset snapshot"). Then `sudo ./abyssctl-linux-<arch> host setup
  --base-domain abyss.test --yes` exits 0. After:
  - `docker version` and `docker compose version` meet the minimums;
  - `/etc/apt/keyrings/docker.asc` and `/etc/apt/sources.list.d/docker.sources` exist at 0644;
  - the activity log has an `install package=docker-ce` line;
  - `/usr/local/bin/abyssctl version` shows the tag's version;
  - the Proxy is running.

  The summary records Docker's versions and its image store, read from `docker info`: "containerd" when the
  containerd snapshotter is in use, otherwise "classic". This is [host.md](host.md) item 9's Docker install.
- **R4. Docker VM: existing Docker.** Before: Docker is installed, and `docker ps -aq` and `docker images -q` are
  both empty (otherwise the step fails with "This VM has containers or images; reset it to its reset snapshot").
  Note `docker version`. Then the same `host setup` exits 0, the activity log has no `install` line for any Docker
  package (`docker-ce`, `docker-ce-cli`, `containerd.io`, `docker-compose-plugin`), and `docker version` is
  unchanged.
- **R5. Appliance VM: refusal.** Note whether Docker is installed. Then the same `host setup` exits 2, and stderr
  names the port: "port 80 is in use by another service" (or 443). After, nothing has changed:
  - there is no `/etc/abyssctl/`, no `/usr/local/bin/abyssctl` and no `abyssctl` account;
  - no Docker apt source was added;
  - Docker is installed only if it was before.

  An appliance VM's run ends here, with no journeys. This is [host.md](host.md) item 7's refusal on a Host with a
  web server.
- **R6. Real path** (the `REAL_PATH_HOST` only, after R3). The release binary, now `/usr/local/bin/abyssctl`, works
  against the real Docker Hub and the real Pushover:
  1. `cert install <ca>/wild.crt <ca>/wild.key` exits 0, with the files copied over first.
  2. `alerts setup --pushover-user <user> --pushover-token -`, with the token on stdin, exits 0. That means
     Pushover answered `"status": 1` to the test message ([alerts.md](alerts.md) item 16). Then `abyssctl.env`
     is root:root 0600, and a `grep -F -f -` of the token (fed on stdin again) finds nothing in the activity
     log. The message reaching the Maintainer's device is checked by eye in the manual steps, as an optional step.
  3. `create rc` with no `--version` exits 0. Then:
     - `status --json` shows `rc` running and healthy;
     - its record's `ACTUAL_VERSION` is the highest plain `X.Y.Z` tag of `actualbudget/actual-server`, which the
       program reads itself from Docker Hub's registry API with an anonymous token. On a mismatch the program reads
       the tags once more, because a new upstream Actual release may have landed between the two lookups, and the
       step passes if the second read agrees. The summary shows that version;
     - its image digest matches the container's `RepoDigests`;
     - `/info`'s `build.version`, fetched through the Proxy with `curl --resolve rc.abyss.test:443:127.0.0.1
       --cacert <ca>`, is that version.

     This step and the next are [versions.md](versions.md) item 19's lookups against Docker Hub.
  4. `proxy upgrade --dry-run` exits 0, and either its panel names a target Caddy version no older than the
     compiled pin, or it prints the no-op line "the Proxy is on caddy <current>, the latest"
     ([proxy.md](proxy.md)). A refusal such as "could not look up the latest Caddy" fails the step.
  5. `remove rc --yes` exits 0 and leaves a `final` Backup under `backups/removed/`.
  6. `image prune --yes` exits 0. Afterwards no `actualbudget/actual-server` image is left, and the Proxy's Caddy
     image is kept. This is [versions.md](versions.md) item 19's `image prune` check on a fresh Engine.
- **R7. Cleanup** (fresh and Docker VMs). In this order: delete `/etc/abyssctl/abyssctl.env`; stop
  `abyssctl-health.timer` and `abyssctl-backup.timer`; take down the Compose project `abyssctl-proxy`; remove every
  local `actualbudget/actual-server` and `caddy` image that came from Docker Hub. Then check that no such image is left, so the journeys' `TestNoHubPulls` sees only
  local-registry images. Docker itself stays.
- **R8. Journeys** (fresh and Docker VMs). `make journey HOST=<host> JOURNEY_BIN=journey-bin/<arch>` runs the
  journey binary's eleven tests ([testing.md](testing.md)): the two contract tests, the eight journeys and
  `TestNoHubPulls`, with the tag workflow's own journey binaries.
  It does not rebuild them. Every test passes. On a fresh VM this also runs `TestRegistryContract` against the
  Engine that R3 just installed, which [versions.md](versions.md) items 16 and 19 ask for. The program then moves
  `dist/journey-out/` into `<role>-<n>/journey-out/`.

After the last VM, the program writes and prints the [summary](#the-summary). It exits 0 when every step it ran
passed, and 1 otherwise.

Each VM's commands and their output go to `<role>-<n>/transcript.log`, with the Pushover user key masked. The
outcome goes to `<role>-<n>/result.json`. Every error line saved in `result.json` is scrubbed first: the ssh host
names in the config and the `RESET_CMD` words are replaced with `<vm>` and `<reset>`.

#### Manual steps

`make release-check-manual TAG=vX.Y.Z [HOST=<ssh host>]` runs on the Maintainer's own terminal. `HOST` must be one
of `FRESH_HOSTS` or `DOCKER_HOSTS`, and the default is `REAL_PATH_HOST`. Preflight P1–P5 run first. Then it does
R1 and R2, then R3 when Docker is absent or R4 when it is present (with their own checks), so the run doesn't
depend on `RESET_CMD`. Then R6.1, `create rc` (latest from Docker Hub) and `backup rc`.

Each step then runs one command on the VM over `ssh -t`, attached to the Maintainer's terminal. The program prints
what to type before each command, and checks the exit code and the VM's state after it:

| Step | Command | The Maintainer | Then |
|---|---|---|---|
| M1 | `stop rc` | answers `n` at `(y/N)` | exit 2, "Not confirmed; nothing changed", `rc` still running |
| M2 | `stop rc` | answers `y` | exit 0, `rc` stopped; the program runs `start rc` |
| M3 | `restart rc` | presses Ctrl-C at `(y/N)` | exit 2, nothing changed, `rc`'s start time unchanged |
| M4 | `remove rc` | types `rcx` at the typed-name prompt | exit 2, "The name did not match; nothing changed", `rc` still there |
| M5 | `restore rc` | sees the pick list newest first, picks 1, then types `rc` | exit 0, a `pre-restore` Backup of `rc` exists |
| M6 | `alerts setup --pushover-user <user>` | pastes the real token at the hidden prompt | exit 0; the program asks "Did the token stay hidden as you pasted it? (y/N)" |
| M7 | (optional) | checks the device | "Did the test message from M6 arrive? (y/N/skip)"; `skip` does not fail the check |
| M8 | `reset-password rc` | sets a new password at Actual's prompt | exit 0; the program asks for the same password at its own hidden prompt and logs in through the Proxy (below), which answers `"status":"ok"` |

The M8 login is `POST https://rc.abyss.test/account/login` with `Content-Type: application/json` and the body
`{"loginMethod":"password","password":"…"}`. The program builds the body with `encoding/json` and sends it on
`curl`'s stdin (`--data-binary @-`, `--resolve`, `--cacert <ca>`). The password and the response's `data.token`
never reach the transcript or `result.json`: the program records the step as "login ok" or the response's
`status` and `reason` only.

M1–M6 are [cli.md](cli.md) item 38's prompts on a real terminal. M8 is [lifecycle.md](lifecycle.md) item 43's
`reset-password`. The typed-name mismatch, which journey 8 can't reach without a terminal, is M4.

Afterwards the program runs `RESET_CMD <host>` if it is set, and otherwise deletes `/etc/abyssctl/abyssctl.env` on
the VM. It records `manual/result.json`, and then writes and prints the summary.

#### The summary

`dist/release-check/<tag>/summary.md` is built from every `result.json` present for the tag. A re-run of one VM
(`ONLY=`) or of the manual steps replaces its own row. It is a Markdown table, pasted into the notes as is:

```
Release check: Abyss 0.1.0 (commit 9b1c4e2), 2026-11-18 14:02 EST

| VM | OS | Docker | Steps | Journeys | Result |
|---|---|---|---|---|---|
| fresh 1 | Debian 13 amd64 | 29.1.0 / 2.40.3, containerd, installed | R1–R3, R6–R8 (Actual 26.1.0) | 11/11 | pass |
| fresh 2 | Ubuntu 24.04 amd64 | … installed | R1–R3, R7, R8 | 11/11 | pass |
| fresh 3 | Ubuntu 26.04 amd64 | … | … | … | pass |
| fresh 4 | TurnKey Core 19 (Debian 13) amd64 | … | … | … | pass |
| docker 1 | TurnKey 19 with Docker (Debian 13) amd64 | 28.5.1 / 2.39.4, classic, kept | R1, R2, R4, R7, R8 | 11/11 | pass |
| appliance 1 | TurnKey Node.js 19 (Debian 13) amd64 | none | R1, R2, R5 | — | pass |
| manual | Debian 13 amd64 | | M1–M6, M8 (M7 skipped) | — | pass |

Result: PASS
```

The values above are examples. The OS column comes from `/etc/os-release` and, on TurnKey, the appliance name in
`/etc/turnkey_version`. "Journeys" counts the journey binary's eleven tests: the two contract tests, the eight
journeys and `TestNoHubPulls`. A failed row gives
the failed step and its first error line. The summary shows the date in the Mac's local time with the zone, and
never shows an ssh host name, an address or a Secret.

**Result** is PASS only when all of these hold:

- every row passed;
- the fresh rows cover all four supported OSes;
- at least one fresh row's Docker uses the containerd image store;
- there is at least one Docker row and one appliance row;
- the real-path steps R6 passed;
- the manual row passed.

Otherwise it is FAIL, with the missing items listed under the table.

### Publishing

Publishing is the Maintainer's decision. `make release-publish TAG=vX.Y.Z` runs `go run ./release publish <tag>`,
which refuses (exit 1, nothing changed) unless:

- the summary's Result is PASS;
- the draft's notes hold the summary exactly as written, compared after turning CRLF into LF and trimming
  trailing whitespace from every line (GitHub's web editor saves CRLF);
- the notes hold no `<!--` placeholder.

It then shows the title, the Result line and the asset names, and asks "Publish Abyss X.Y.Z? (y/N)" on a terminal.
Without a terminal it refuses. On `y` it runs `gh release edit <tag> --draft=false --latest`.

Then a **post-publish check**: it downloads `SHA256SUMS` from
`https://github.com/DocCyblade/abyssctl/releases/latest/download/SHA256SUMS`, without auth, and checks that it is
byte-identical to the one checked in P3, retrying every 10 seconds for up to a minute while GitHub's redirect
catches up. That proves the README's install links now serve this Release.

### Repo-setup checklist

Done once by the Maintainer when the repo is created, and checked again before the first Release:

- Default branch `main`. Branch protection on `main`: changes come by pull request, and the `ci.yml` job and
  both `journeys.yml` jobs must pass.
- Two tag rulesets on `v*`:
  - "restrict creations", with the Repository admin role as the only bypass, so only the Maintainer creates them;
  - "restrict updates" and "restrict deletions", with no bypass, so no one moves or deletes them.
- Releases: immutable releases on, so a published Release's assets and tag can't change.
- Actions: default workflow permissions read-only; Actions may not create or approve pull requests; artifact
  retention at least 90 days, so `journey-bin-<arch>` lasts as long as the workflow asks.
- Secrets: the optional read-only `DOCKERHUB_TOKEN` (and `DOCKERHUB_USER`) for test image cache rebuilds
  ([testing.md](testing.md)). There are no other repository secrets: the tag workflow uses only `GITHUB_TOKEN`
  and OIDC for the attestation.
- Discussions on, for questions. Issues on.
- Private vulnerability reporting on, as `SECURITY.md` says ([docs.md](docs.md)).

## 4. Files and formats it owns

```
.github/workflows/release.yml   the tag workflow
release/                        package main: go run ./release <tag-check|release-binary-check|notes|check|check-manual|publish>
  main.go, runner.go, tagcheck.go, binarycheck.go, notes.go, config.go, check.go, manual.go, summary.go, publish.go
  *_test.go                     the tests in the acceptance list
  notes.md.tmpl                 the Release notes template
  testdata/                     captured `go version -m` output, small fake binaries, notes and summary golden files
Makefile                        release-check, release-check-manual, release-publish
dist/release-check/<tag>/       (git-ignored under dist/) release/, journey-bin/<arch>/, ca/, <role>-<n>/, manual/, summary.md
```

- **The asset names and `SHA256SUMS`** (above).
- **The Release notes template** and its five sections (above).
- **The local config** keys (above). Its default path is outside the repo, so it can't be committed.
- **`result.json`**, one per VM and one for the manual steps: `Result` in [Interface](#2-interface), as JSON. It is
  internal to the program, and only `Summary` reads it:

  ```json
  {"schema": 1, "role": "fresh", "index": 1, "os": "Debian 13", "arch": "amd64",
   "docker": {"engine": "29.1.0", "compose": "2.40.3", "store": "containerd", "installed": true},
   "actual": "26.1.0", "steps": [{"id": "R3", "ok": true, "error": ""}], "journeys": {"passed": 11, "total": 11},
   "at": "2026-11-18T19:02:11Z"}
  ```

- **Make targets:** `release-check TAG=… [ONLY=…]`, `release-check-manual TAG=… [HOST=…]` and
  `release-publish TAG=…`. Each runs the program with the same arguments.
- **How the Release check reuses the journeys:** `make journey HOST=<host> JOURNEY_BIN=<dir>`. `JOURNEY_BIN`
  skips the build and stages the three binaries from that folder, with the test image cache
  ([testing.md](testing.md) owns `make journey`).
  The tag workflow calls `journeys.yml` with `keep-binaries: true`, which uploads those binaries as
  `journey-bin-<arch>`.

The program is a dev tool: it never ships in a Release, and the binary never imports it.

## 5. Depends on

- [foundation.md](foundation.md): `make build`, `buildinfo.ValidVersion` and `CompareVersions`, the pins file, and
  `ci.yml`, which `release.yml` calls as a reusable workflow.
- [testing.md](testing.md): `journeys.yml` (inputs `version` and `keep-binaries`), `make journey` (`HOST`,
  `JOURNEY_BIN`), `make image-cache`, and the journeys themselves.
- [host.md](host.md): `host setup`'s Docker install, conflict refusal and install of the binary.
- [alerts.md](alerts.md): `alerts setup` and the test message. [versions.md](versions.md): `create`'s latest
  lookup, `proxy upgrade` and `image prune`. [lifecycle.md](lifecycle.md) and [cli.md](cli.md): the prompts the
  manual steps check.
- On the Mac: `gh`, `ssh`, `scp` and `shasum`. On the VMs: `sha256sum`, `curl` and `sudo`.

The import rule this spec adds to `internal/archtest`:

- `release` imports only the standard library and `internal/buildinfo`;
- no package imports `release`.

## 6. Decisions

- **[ADR 0003](../adr/0003-single-static-go-binary.md):** one static binary per architecture, so a Release is two
  bare files and no archive.
- **Plain `make build` in CI, no GoReleaser.** A Release and a local build come from the same Make target and the
  same toolchain line in `go.mod`.
- **`SHA256SUMS` is the required check; the attestation is optional.** `sha256sum` is on every supported Host,
  while `gh` isn't. Key-based signatures are Later.
- **Draft first, publish by hand.** The tag workflow can prove the code on GitHub's runners, but only the test VMs
  prove a fresh Docker install on each supported OS, an appliance refusal, Docker Hub, Pushover and a real
  terminal. So the workflow stops at a draft, and the Maintainer publishes.
- **Journey binaries are workflow artifacts, not assets.** The Release check runs exactly the journey builds CI
  ran, and an Operator can't download a binary stamped for `127.0.0.1:5000` by mistake.
- **Fresh install first, journeys after.** A fresh VM has no Docker, and the journeys need one. So the release
  binary's own `host setup` installs it (R3), the real-path steps run on that Host, and the cleanup (R7) removes
  what came from Docker Hub before the journeys' `Blank`. The release binary's `host setup` pulls the Caddy pin from
  Docker Hub on each fresh and Docker VM, and the real-path `create` pulls Actual once: about six anonymous pulls
  per Release check, each from its VM's own address. Docker Hub allows 10 anonymous pulls per hour per IP address,
  so VMs behind one NAT address share that limit, as does a `make image-cache` run from the same network.
- **The real pair never enters the repo or a command line.** It lives in the Maintainer's 0600 config outside the
  repo and travels on ssh stdin. The VM keeps it only until the cleanup.
- **VM details stay outside the repo.** `RESET_CMD` is the Maintainer's own hook. The repo knows roles, never names,
  addresses, snapshots or hypervisor tokens.
- **The Release check is a Go program**, for the same reason the journeys are Go tests: it is tested, and it runs
  the same on macOS and Linux with no shell differences. It adds no dependency to `go.mod`, because it calls `gh`
  and `ssh` as commands.
- **Manual steps are a separate target.** The scripted part needs no person, so an agent can run it. The terminal
  steps need the Maintainer at a keyboard, and the summary isn't PASS without them.
- **The Release check VMs are amd64 only.** A fresh Docker install on arm64 is not checked; arm64 is covered only
  by the CI journeys on `ubuntu-24.04-arm`, where Docker is already present.
- **Only forward** means there are no patch Releases on an older line: a fix always ships as the next version
  above the highest tag.
- **No pre-release tags in v1.** Every tag is a candidate for the latest Release, and `releases/latest/download`
  always means the newest checked one.
- **Release notes are the changelog**, written for Operators, with the Release check summary in each.

## 7. Later

- Key-based signatures on Releases (cosign, minisign or GPG).
- A `.deb` package and an apt repository ([host.md](host.md)).
- The Operator-run Tool update command (ROADMAP Next) downloads these asset names and checks `SHA256SUMS`.

## 8. Docs it changes

- README **Install** and **Updating Abyss**: the asset names and `releases/latest/download/…` URLs above, the
  `sha256sum -c --ignore-missing SHA256SUMS` line, and a link to the Releases page for what changed
  ([docs.md](docs.md) writes them).
- `CONTRIBUTING.md`: one line saying Releases are cut by the Maintainer as this spec describes, and that a
  contributor never tags.
- `SECURITY.md`: "the latest Release only" means the latest published Release on GitHub.
- `docs/guides/upgrades.md`, "Updating Abyss itself": read the Release notes' "Before you update" first.

## 9. Acceptance list

### Tag workflow

1. `tag-check` passes `v0.1.0` and `v1.2.10`. It refuses `v1.2`, `1.2.3`, `v01.2.3`, `v1.2.3-rc.1`,
   `v1.2.3+b`, a version not higher than the highest existing `v*` tag, a commit not on `origin/main`, and a tag
   that already has a published Release, each naming its rule. → `TestTagCheck` (table, git and gh faked)
2. `release-binary-check` passes a release build. It fails on build info without `-trimpath=true`, with
   `CGO_ENABLED=1`, with `GOOS` not `linux`, or with a `GOARCH` that doesn't match the file name; on a binary that
   holds either journey address; and on one missing any of the four release defaults. → `TestReleaseBinaryCheck`
   (table: captured `go version -m` output and small fake binaries in `release/testdata/`)
3. `notes` renders the five sections. "What's changed" lists the non-merge commit subjects since the previous tag,
   or "First public Release." when there is none. The Caddy-pin and Docker-minimum lines appear only when those
   values changed since the previous tag. "Tested with" carries both pin files' values. →
   `TestNotes` (table, golden files)
4. The draft is created only after `tag`, `ci`, both `journeys` jobs and both `smoke` jobs pass, and only when no
   Release for the tag exists, draft or published. Any failure leaves no Release, and the draft is never a
   pre-release. The journey binaries are uploaded as
   `journey-bin-<arch>` with 90-day retention and never attached to the Release. → (review of `release.yml`'s
   `needs:`, the `draft` job's check and `journeys.yml`'s `keep-binaries`); the "exactly three assets" part also
   by preflight P3
5. On each architecture's runner, the release binary checks against `SHA256SUMS`, is statically linked, passes
   `release-binary-check`, and its unprivileged `version --json` shows the tag's version, the tagged commit,
   `linux/<arch>` and the compiled Caddy pin. → the `smoke` jobs in `release.yml`

### The Release check program

6. The config is refused when it is not owned by the user or not 0600, has an unknown key or a missing required
   key, lists a host in two roles, or has a `REAL_PATH_HOST` that is not in `FRESH_HOSTS`. `RESET_CMD` may be
   absent. → `TestConfig` (table)
7. Preflight P1–P5 refuse, before any `ssh`, on each of: gh not logged in, a dirty checkout or `HEAD` not at the
   tag, no draft or a published Release, assets other than the three, a checksum or attestation failure, no
   successful tag run, a missing `journey-bin-<arch>`, and a cache key mismatch. Each comes with its message and
   exit 1. → `TestPreflight` (table, the command runner faked)
8. `Plan` gives R1, R2, R3, R7, R8 for a fresh VM; adds R6 between R3 and R7 for the real-path VM; gives R1, R2,
   R4, R7, R8 for a Docker VM; and R1, R2, R5 for an appliance VM. `VMs` orders fresh, Docker, appliance, and
   `only` picks one VM. A failed step ends that VM and the run goes on to the next. → `TestPlan` (table)
9. The Pushover token, the M8 password and the M8 response's `data.token` never appear in the `Args` or `Env` of
   any `Cmd`, in a transcript, a `result.json` or the summary: each is only ever written to a child's stdin or read
   from its output. In transcripts, `result.json` and the summary, the user key appears only as `…abcd`. →
   `TestNoSecrets` (`FakeRunner` records every call)
10. The summary is built from the `result.json` files, a re-run replaces only its own row, and it never holds an
    ssh host name, an address or a `RESET_CMD` word, including in a failed row's error line. Result is FAIL when a
    row failed, one of the four OSes has no fresh row, no fresh row has the containerd image store, there is no
    Docker or appliance row, R6 didn't pass, or the manual row is missing or failed. → `TestSummary` (table, golden
    files)
11. `publish` refuses (exit 1, no `gh release edit`) when the Result is not PASS, when the notes don't hold the
    summary (a CRLF copy with trailing spaces still matches), when they hold a `<!--`, or when there is no terminal. A `y` publishes with
    `--draft=false --latest`. → `TestPublishGate` (table, gh faked)
12. `release` imports only the standard library and `internal/buildinfo`, and nothing imports it. →
    `TestImportRules` in `internal/archtest`

### On the test VMs (scripted)

13. On every VM, the copied asset passes `sha256sum -c --ignore-missing SHA256SUMS`, and its unprivileged
    `version --json` shows the tag's version and commit. → Release check step R2
14. On each fresh VM (Debian 13, Ubuntu 24.04, Ubuntu 26.04, TurnKey Core 19), `host setup` installs Docker from
    Docker's apt repository to at least the minimums, with the key and source files at 0644, and installs the
    binary. This is [host.md](host.md) item 9. → Release check step R3
15. On a VM with Docker already present and no containers or images, `host setup` installs no Docker package and
    leaves Docker's version unchanged. A VM with containers or images fails the step. → Release check step R4
16. On a VM where another web server holds 80 or 443, `host setup` exits 2 naming the port and changes nothing,
    with no Docker installed by it. This is [host.md](host.md) item 7. → Release check step R5
17. On the real-path VM, the release binary's `alerts setup` with the Maintainer's real Pushover pair exits 0, and
    the token is nowhere in the activity log. This is [alerts.md](alerts.md) item 16. → Release check step R6.2
18. On the real-path VM, against Docker Hub: `create` with no `--version` gets the highest stable Actual and
    serves it through the Proxy; `proxy upgrade --dry-run` names a target no older than the pin, or says the Proxy is
    on the latest; after `remove`, `image prune` removes
    the Actual image and keeps the Proxy's. With item 19's `TestRegistryContract` on the fresh Engine, this is
    [versions.md](versions.md) item 19. → Release check steps R6.3–R6.6
19. On each fresh and Docker VM, after the cleanup leaves no Docker Hub image, all eleven tests of the journey
    binary (two contract tests, eight journeys, `TestNoHubPulls`) pass with the tag workflow's own journey
    binaries. → Release check step R8

### On the test VMs (manual)

20. On a real terminal: `n` at y/N refuses and changes nothing; `y` acts; Ctrl-C at y/N refuses; a mismatched
    typed name refuses; the restore pick list lists newest first and leads to the typed name; the token prompt
    hides its input. This is [cli.md](cli.md) item 38. → Release check manual steps M1–M6 (M7, the test message's
    arrival, is optional)
21. `reset-password` on a running Instance prompts on the terminal, and the new password logs in through the
    Proxy. This is [lifecycle.md](lifecycle.md) item 43. → Release check manual step M8

### Publishing, repo and docs

22. After publishing, within a minute, `releases/latest/download/SHA256SUMS` is byte-identical to the draft's. →
    the post-publish check in `make release-publish`
23. The repo settings match the [repo-setup checklist](#repo-setup-checklist). → (review)
24. Every published Release's notes have the five sections, the Release check summary and no placeholder. →
    (review); the placeholder and summary parts also by `TestPublishGate`
25. Docs: the README's Install and Updating Abyss blocks use the asset names and URLs above; `CONTRIBUTING.md`,
    `SECURITY.md` and `upgrades.md` say what [Docs it changes](#8-docs-it-changes) lists. → (review)
