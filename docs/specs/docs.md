# Spec: docs

Build step 16 of 16, with a part ticketed with [foundation.md](foundation.md) (step 1), because every later
spec's acceptance list relies on the reference check and the link check. That part is: `internal/cli/gendocs`,
`make docs` and `make docs-check`, the `ci.yml` step, `internal/docscheck` with `TestLinks`, `TestHelp_Complete`
and `TestRootHelp_CoversContract`, `LICENSE`, and the six guides and `CONTRIBUTING.md` as H2 skeletons (the
headings below, no body) so later specs' links to them pass `TestLinks`. The rest lands last, as the final docs
pass. The [overview](README.md) has the module map and the
index. Capitalised terms are defined in [GLOSSARY.md](../../GLOSSARY.md).

This spec owns the public-facing docs of Abyss: what each file is for, how it is laid out, and which spec supplies
each part of it. It does not hold their text. Every other spec's [Docs it changes](README.md#how-to-use-this-pack)
section names what it adds; this spec is the index that puts each of those items in one file, so nothing is left
out and nothing is written twice.

It links to what other specs own and does not restate it:

- [cli.md](cli.md) owns the root command's `Long` help (the root reference page), the verb table each verb's help
  text follows, and the output contract; this spec owns the other [help text rules](#help-text-rules);
- [release.md](release.md) owns the Release asset names and URLs, the Release notes and their template (the
  changelog), the release flow, and the repo-setup checklist (Discussions and private vulnerability reporting);
- [foundation.md](foundation.md) owns the Makefile's base targets, `ci.yml`, `Root()` and the import-rules test;
- [testing.md](testing.md) owns the journeys and `make journey`.

## 1. Purpose

Give an [Operator](../../GLOSSARY.md) everything they read to install, run, update and remove Abyss, and give a
contributor what they need to change it:

- the root `README.md`: what Abyss does, requirements, install, quickstart, updating, and links into `docs/`;
- six Operator guides in `docs/guides/`;
- a command reference in `docs/reference/`, generated from the help text by `make docs` and checked in CI;
- `LICENSE` (MIT), `SECURITY.md`, `CONTRIBUTING.md` and the issue templates;
- the rule that docs are part of done: each spec names the docs it changes, and its acceptance list carries a
  docs item.

The specs in `docs/specs/` are for whoever builds Abyss; the guides are for Operators. Both link to the one root
`GLOSSARY.md`; there is no second, Operator-only glossary.

This spec is done when every file below exists with the sections given, `make docs-check` passes in CI, the docs
tests pass, and the final docs pass finds every item of the [docs index](#4-files-and-formats-it-owns) carried.

## 2. Interface

This spec is cross-cutting, so it has no Go interface of its own.

## 3. Behaviour

### Docs are part of done

- Each spec's section 8 names the README sections, guides, reference pages and community files it changes. Its
  acceptance list has a docs item marked **(review)**, and its reference pages are also checked by
  `make docs-check`.
- A spec's docs are written with its code, in the same tickets. When a guide or `CONTRIBUTING.md` doesn't exist
  yet, the ticket creates it with the H2 headings this spec gives and fills only its own part. Foundation does this
  for `CONTRIBUTING.md`'s "Build and test" section.
- This spec's own tickets add what no module owns (the README frame, `SECURITY.md`, the issue templates, the
  CONTRIBUTING sections marked docs.md below, the orphans listed under each file) and end with one docs pass:
  every row of the index is checked against its file.
- A change to a verb's help text is committed with the regenerated `docs/reference/` pages. A change to what a
  verb does updates the guide row that covers it.

### The generated reference

`internal/cli/gendocs` is `package main`. It imports `internal/cli` for `Root()` ([foundation.md](foundation.md))
and `github.com/spf13/cobra/doc`, which comes with Cobra (foundation's dependency), so it adds none.

- **One walk.** The generator builds the command tree and sets `DisableAutoGenTag` on every command. It then walks
  the tree depth first, children in Cobra's sorted order, skipping a command when
  `!c.IsAvailableCommand() || c.IsAdditionalHelpTopicCommand()` (Cobra's own rule, so help and the turned-off
  `completion` command get no page). For each command it calls `doc.GenMarkdownCustom(cmd, &buf, linkHandler)`
  into a buffer and names the page `strings.ReplaceAll(cmd.CommandPath(), " ", "_") + ".md"`: `abyssctl.md`,
  `abyssctl_host_setup.md`, `abyssctl_cert_install.md`, and so on. That is the root, each group (`host`, `cert`,
  `proxy`, `backup`, `health`, `alerts`, `image`) and each verb. `linkHandler` returns the bare file name, since
  the pages share a folder. Both `-out` and `-check` use this walk, so they can't disagree.
- **`make docs`** runs `go run ./internal/cli/gendocs -out docs/reference` and writes each buffer to its page.
- **Every page** begins with the line
  `<!-- Generated by make docs from abyssctl's help text. Do not edit: run make docs. -->`. Then come Cobra's
  sections: the `Short` line, the synopsis from `Long`, usage, examples, flags, inherited flags and "SEE ALSO".
- **The root page** `abyssctl.md` is the reference's index. Its synopsis is [cli.md](cli.md)'s root `Long` help,
  and its "SEE ALSO" list links every top-level command. Guides link pages as `../reference/<page>.md`.
- **Pages are pure.** They come only from compiled-in text, so the same commit gives the same bytes on Linux and
  macOS, in any time zone and under any environment. Help text never shows a value read at run time: no
  environment variable, Host path from settings, date or `buildinfo.Version`. Fixed paths such as
  `/etc/abyssctl/abyssctl.env` and `/var/log/abyssctl/abyssctl.log` are fine.
- **`make docs`** deletes every `abyssctl*.md` in `docs/reference/` that it didn't write in this run, so a removed
  command's page goes too. The folder holds only generated pages.
- **`make docs-check`** runs `go run ./internal/cli/gendocs -check docs/reference`. It runs the walk in
  memory and compares them with the folder, writing nothing. When they match it exits 0. Otherwise it prints one
  line per difference on stderr (`stale abyssctl_backup.md`, `missing abyssctl_cert_remove.md`,
  `extra abyssctl_purge.md`) and then `docs/reference is out of date: run make docs`, and exits 1.
- **CI.** Foundation's `ci.yml` job gets one step, `make docs-check`, after `make test`. So a pull request that
  changes help text without regenerating the pages fails CI.
- There are no man pages in v1. On the Host, `--help` is the reference.

### Help text rules

The reference is only as good as the help text. This spec owns these rules; [cli.md](cli.md) owns the verb table
they draw on:

- Every runnable command has a `Short` (one line, no closing full stop), a `Long` and at least one `Example`.
  Every group has a `Short`.
- A verb's `Long` says what its verb-table row says about locks, the lock wait, prompts, `--dry-run`, `--yes`,
  `--json` and exit codes that differ from the usual, plus anything its own spec's section 8 lists for its page.
- The root `Long` covers exit codes 0–4, `--yes`, `--dry-run`, `--json` and its `schema` field, results on stdout
  and everything else on stderr, `NO_COLOR`, the activity log's path, and that a prompt needs a terminal or
  `--yes`.
- Examples use the Base domain `budget.example.com`, the Instance `kids`, and placeholders for every
  [Secret](../../GLOSSARY.md) (`--pushover-user <user key>`, `--pushover-token -`).
- **No Secret-shaped value** appears in any help text: no line containing `-----BEGIN` and `PRIVATE KEY-----`,
  and no run of exactly 30 letters and digits standing alone (the shape of a Pushover user key or token).

### Writing rules for the README and guides

- Write for the Operator: present tense, second person, a Linux shell with `sudo`. Every command is shown as
  `sudo abyssctl …`, except `version` and `--help`; the README says once that as root you drop the `sudo`.
- Link a capitalised [glossary](../../GLOSSARY.md) term to the glossary at its first use on each page:
  `GLOSSARY.md` from the README, `../../GLOSSARY.md` from a guide. Use the glossary's words, never its "Avoid"
  words.
- Link a verb to its reference page instead of copying its flags.
- Shown times follow the overview's [time rule](README.md#standing-constraints): Host local time with the zone
  abbreviation on screen, UTC in file names and `--json`.
- No example holds a Secret. Pushover values show as `…abcd`, keys and certificates as file names, and budget data
  never.
- Never mention earlier tools that managed Actual servers: Abyss is a fresh project.
- The README and guides say, once each, that Abyss is not part of the Actual project.
- No troubleshooting or FAQ page until real issues show what one should say.

### The docs tests

`internal/docscheck` holds test files only and imports only the standard library. It finds the repo root by
walking up to `go.mod` and runs in `make test` on Linux and macOS, offline:

- **`TestLinks`**: in every `*.md` file of the repo outside `.git/`, `bin/`, `dist/` and any `testdata/`, every
  relative link points at a file that exists. Links inside fenced code blocks and inline code spans are not
  links and are not checked. Every `#anchor`, on the same page or another, matches a heading by GitHub's anchor
  rule: take the heading's rendered text (backticks and link syntax removed), lower-case it, keep letters, digits,
  `-` and `_`, drop other punctuation, turn each space into `-` (runs are not collapsed), and add `-1`, `-2`, …
  to repeats on the page. External `http(s)` links are not fetched.
- **`TestNoPredecessorNames`**: every text file `git ls-files` lists (specs, ADRs, docs, code, workflows and
  `testdata/` included) is free of the names of the earlier tools that managed Actual servers, matched as whole
  words, case-insensitively. The name list lives only in the test's own source, and the test skips that one file.
- The structure tests in the [acceptance list](#9-acceptance-list), which check the file set, headings, the
  README's opening and install block, the license and the issue templates.

## 4. Files and formats it owns

```
README.md
LICENSE
SECURITY.md
CONTRIBUTING.md
.github/ISSUE_TEMPLATE/bug.yml
.github/ISSUE_TEMPLATE/config.yml
docs/guides/certificates.md  backup-and-restore.md  upgrades.md  alerts.md  security.md  teardown.md
docs/reference/abyssctl*.md     generated by make docs; never edited by hand
internal/cli/gendocs/           main.go (generate and check), gendocs_test.go
internal/docscheck/             docs_test.go (test files only)
Makefile                        docs, docs-check
```

There is no `CHANGELOG.md`: the Release notes are the changelog ([release.md](release.md)). There is no code of
conduct, pull-request template, feature-request template, troubleshooting page or FAQ in v1. Source files carry
no copyright or SPDX headers.

The tables below are the docs index. **Owner** is the spec whose section 8 supplies the item. "docs.md" marks an
item no other spec lists, so this spec carries it. Where two specs list the same item, the row says which one
owns it; the other only links.

### `README.md`

It opens with this text, word for word:

> # Abyss
> `abyssctl` runs and maintains several Actual Budget sync servers on one Docker Host.
>
> Abyss is not part of the Actual project, and Actual's maintainers don't support it.

Then these H2 sections, in this order:

| Section | Content | Owner |
|---|---|---|
| What it does | About six bullets from the [v1 capabilities](README.md#v1-capabilities): several Instances on one Host behind one Proxy with the Operator's Certificates; all-or-nothing `create` and a final Backup on `remove`; Upgrade with a Backup first and automatic Rollback; nightly Backups with Retention, and a Restore that Regroups; a Health check with Pushover Alerts and the MOTD; a summary before each change, a confirmation before anything that stops a service or deletes or replaces data (see each verb's help), and `--dry-run`; one static binary | docs.md |
| Requirements | Supported Hosts (Debian 13, Ubuntu 24.04 and 26.04, TurnKey Core 19; amd64 or arm64), root or a user with `sudo` (as root, drop the `sudo` from every command), ports 80 and 443 free, a Base domain with DNS pointing at the Host, the Operator's own Certificate | [host.md](host.md) |
| | Docker Engine 24.0 and Compose 2.24 or newer; an existing Docker that meets them is used as is | [docker-and-locks.md](docker-and-locks.md) |
| | Or no Docker: `host setup` installs it from Docker's apt source | [host.md](host.md) |
| | Outbound HTTPS to Docker Hub (`registry-1.docker.io` and `auth.docker.io` for lookups, plus Docker's download CDN for pulls) | [versions.md](versions.md) |
| | Outbound HTTPS to `api.pushover.net` when Pushover is used | [alerts.md](alerts.md) |
| | Outbound HTTPS to `github.com` to download a Release, and to `download.docker.com` when `host setup` installs Docker; `curl` (`sudo apt install curl` if missing) | docs.md |
| Install | The block below; delete the download after `host setup`; the optional `gh attestation verify` line, run off the Host; a link to the Releases page | [release.md](release.md) (asset names, URLs, checksum line), [host.md](host.md) (the `host setup` line) |
| Quickstart | `sudo abyssctl host setup --base-domain …` (done in Install) | [host.md](host.md) |
| | `sudo abyssctl cert install cert.pem key.pem` (a wildcard for the Base domain or one per Hostname) | [proxy.md](proxy.md) |
| | optional `sudo abyssctl alerts setup --pushover-user <user key> --pushover-token -` | [alerts.md](alerts.md) |
| | `sudo abyssctl create kids` (no `--version`: the latest stable Actual), `sudo abyssctl status`, then open `https://kids.<Base domain>` and set the server password in Actual | [lifecycle.md](lifecycle.md), [versions.md](versions.md) |
| | later: `sudo abyssctl upgrade kids` | [upgrade.md](upgrade.md) |
| Updating Abyss | The Install block with the newer Release, from the same `releases/latest/download` URLs; read the Release notes' "Before you update" first | [release.md](release.md) |
| | A downgrade is refused | [host.md](host.md) |
| | release.md's "Before you update" sentence: updating recreates no Instance container except to fix drift; the Proxy may restart for a few seconds when the new version renders it differently, and `host setup` says so before you confirm; each Instance moves to the new files on its next `start`, `restart`, `upgrade` or `restore`; `status` shows which are out of date | [release.md](release.md) (wording), [host.md](host.md), [lifecycle.md](lifecycle.md) |
| | Every generated file says it is generated, and a hand edit is overwritten | [render.md](render.md) |
| | A link to `docs/guides/upgrades.md`'s "Updating Abyss itself" | docs.md |
| Documentation | Links to the six guides (teardown included: it is linked, not written, in the README), the reference (`docs/reference/abyssctl.md`), `GLOSSARY.md`, `ROADMAP.md`, the Releases page (what changed), `CONTRIBUTING.md`, `SECURITY.md`, and `docs/specs/README.md` for how Abyss is built | docs.md |
| Getting help | Bugs: GitHub Issues, with the bug template. Questions and ideas: GitHub Discussions. Security: `SECURITY.md`, never a public issue. Don't ask about Abyss in Actual's Discord or issue tracker. On the Host, `abyssctl --help` | docs.md |
| License | MIT, linking `LICENSE` | docs.md |

**The install block** (the asset names and URLs are [release.md](release.md)'s):

```sh
ARCH=$(dpkg --print-architecture)   # amd64 or arm64
curl -fsSLO https://github.com/DocCyblade/abyssctl/releases/latest/download/abyssctl-linux-$ARCH
curl -fsSLO https://github.com/DocCyblade/abyssctl/releases/latest/download/SHA256SUMS
sha256sum -c --ignore-missing SHA256SUMS
chmod +x abyssctl-linux-$ARCH
sudo ./abyssctl-linux-$ARCH host setup --base-domain budget.example.com
rm abyssctl-linux-$ARCH SHA256SUMS
```

As root, the same block runs without `sudo`. `host setup` installs the binary as `/usr/local/bin/abyssctl` ([host.md](host.md)). Updating Abyss uses the same
first five lines and runs `host setup` with no flags, which keeps every setting.

### `docs/guides/certificates.md`

| Section | Content | Owner |
|---|---|---|
| What Abyss needs | A Certificate per Hostname or a wildcard for the Base domain; the leaf first and the chain after it in one file; an unencrypted key; a private CA is fine; no ACME | [proxy.md](proxy.md) |
| Installing a Certificate | `cert install` | [proxy.md](proxy.md) |
| | `host setup --cert <file> --key <file>` does the same | [host.md](host.md) |
| Which Certificate serves a Hostname | One level per wildcard; the most specific, then the latest expiry; an expired exact-name Certificate still wins until it is removed | [proxy.md](proxy.md) |
| Renewing | Install the new Certificate; it replaces only Certificates whose names it carries | [proxy.md](proxy.md) |
| Listing and removing | `cert list` and "unused"; `cert remove`, refused while it would leave an Instance uncovered; the key file is deleted, so keep a copy | [proxy.md](proxy.md) |
| Expiry warnings | `CERT_WARN_DAYS`, only for Certificates in use, again when expired; links `alerts.md` for the Problem | [proxy.md](proxy.md) (owner), [health.md](health.md) (links) |

### `docs/guides/backup-and-restore.md`

| Section | Content | Owner |
|---|---|---|
| Backups and their kinds | The Backup kinds and ids; where Backups live; the brief stop | [backup.md](backup.md) |
| The nightly Backup | `BACKUP_TIME` (Host local time) and `BACKUP_KEEP` | [host.md](host.md) (owner), [backup.md](backup.md) (links) |
| | The timer starts within five minutes after `BACKUP_TIME`; a run missed while the Host was off runs soon after boot | [render.md](render.md) |
| | What the nightly run does: every Instance, a busy one skipped, Retention of scheduled Backups only; a failure becomes a Problem (see `alerts.md`) | [backup.md](backup.md) |
| Backups by hand | `backup`, `backup --all`, `backup list`, `backup prune` | [backup.md](backup.md) |
| Checking a Backup | `sha256sum -c` with the `.sha256` file | [backup.md](backup.md) |
| Restoring | `restore` by id, `--latest` and the pick list; the Restore brings back the Backup's Actual version, takes a `pre-restore` Backup and Regroups | [backup.md](backup.md) |
| | `--latest` picks the newest verified Backup of any kind, so right after a Restore it picks that Restore's `pre-restore` Backup | docs.md (orphan from backup.md's notes) |
| What devices see | `file-has-reset`, revert, and that devices' later changes are lost | [backup.md](backup.md) |
| An interrupted Restore | Finishing it with `restore <name>` | [backup.md](backup.md) |
| Removed Instances | `remove` takes a `final` Backup first; removed Instances' Backups live in `backups/removed/<name>-<stamp>/`, are not offered to `restore`, and are deleted by hand (see `teardown.md`) | [lifecycle.md](lifecycle.md) |
| Restoring by hand | The by-hand steps that `restore`'s exit-4 hint names | [backup.md](backup.md) |

### `docs/guides/upgrades.md`

| Section | Content | Owner |
|---|---|---|
| Upgrading an Instance | What `upgrade` does and how long it is down; `upgrade <name> <version>`, `--timeout`, `--dry-run` | [upgrade.md](upgrade.md) |
| Which versions Abyss picks | Plain `X.Y.Z` only, never pre-releases, nightly, edge or `-alpine`; `create` takes the latest unless `--version` is given | [versions.md](versions.md) |
| When an Upgrade fails | Exit 3 and the automatic Rollback; the failure panel's log pointer and the `upgrade-failed-*.log` files | [upgrade.md](upgrade.md) (owner), [cli.md](cli.md) (the exit code on the root page) |
| When Abyss needs you | Exit 4; finishing an interrupted Upgrade with `upgrade <name>` | [upgrade.md](upgrade.md) |
| | An Instance changed by a newer Abyss is refused until that Abyss is installed | [lifecycle.md](lifecycle.md) |
| Undoing an Upgrade later | `restore <name> <pre-upgrade id>` | [upgrade.md](upgrade.md) (owner), [backup.md](backup.md) (links) |
| Upgrading the Proxy | `proxy upgrade [<version>]`, the target rule, the few seconds every Instance is unreachable, a failed upgrade putting the old Proxy back, and `host setup` never moving the Proxy's version | [proxy.md](proxy.md) |
| Freeing disk space | What `image prune` removes and keeps; that it waits for every Instance's lock; a Restore of a Backup whose version was pruned downloads it again | [versions.md](versions.md) (owner), [upgrade.md](upgrade.md) (links) |
| Updating Abyss itself | Read the Release notes' "Before you update" first | [release.md](release.md) |
| | The same steps as the README's Updating Abyss; a downgrade is refused; release.md's "Before you update" sentence (no Instance container recreated except to fix drift, the Proxy may restart for a few seconds, Instances move on their next `start`, `restart`, `upgrade` or `restore`) | docs.md, from [release.md](release.md) and [host.md](host.md) |
| | After a Tool update, `status` flags files out of date and the next `start`, `restart`, `upgrade` or `restore` of each Instance applies them (`restart <name>` does it at once) | [lifecycle.md](lifecycle.md) |
| | The MOTD's `↑` "files out of date" line compares only `TOOL_VERSION`; `status` also compares `compose.yaml` with what Abyss renders, so it catches a hand edit too | docs.md (orphan from health.md's notes) |

### `docs/guides/alerts.md`

| Section | Content | Owner |
|---|---|---|
| What the Health check looks at | What it checks and how often; `HEALTH_INTERVAL` (whole minutes, 1–60) | [health.md](health.md), [host.md](host.md) (the setting) |
| Problems | Live and Event Problems; each Problem kind, what it means and its fix (the kinds table); the two-run wait for down, unhealthy, Proxy down and Docker down; a busy or stopped Instance is not judged; an Orphan's Problem ends without a recovery message | [health.md](health.md) |
| | `DISK_MIN_FREE` (a percentage) and `CERT_WARN_DAYS` | [host.md](host.md) |
| | A Certificate in use warns `CERT_WARN_DAYS` before expiry and again when expired; an expired exact-name Certificate must be removed with `cert remove` (links `certificates.md`) | [health.md](health.md) |
| Clearing a Problem | `health clear <name>` ends an Instance's Event Problems; for a failed scheduled Backup, fix the cause, then clear; the next scheduled Backup that succeeds also ends it, a Backup by hand does not | [health.md](health.md) |
| Setting up Pushover | Getting a user key and creating an application for its token; `alerts setup`, which sends a test first and saves only if it gets through; the token from stdin or the hidden prompt; `alerts test`; `host setup --pushover-user … --pushover-token -` saves the pair without a test; changing the pair; removing Pushover by deleting its two lines from `abyssctl.env` | [alerts.md](alerts.md) |
| | Without Pushover no Alert is sent and the MOTD still shows the Problems | [alerts.md](alerts.md) (owner), [host.md](host.md) (links) |
| What an Alert looks like | A start is a normal notification, a recovery a quiet one; a failed delivery is tried again at the next Health check; an Alert may rarely arrive twice | [alerts.md](alerts.md) (owner), [health.md](health.md) (links) |
| The MOTD | Reading it: `!` and `↑` lines, "Alerts not delivered", "Alerts not sent", "version info stale" | [health.md](health.md) |
| | "Health check not running" when `motd.txt` is older than three intervals, and how to check the timer | [render.md](render.md) |
| Checking by hand | `health check`, its exit codes, `--json`, and which runs reach the activity log | [health.md](health.md) |

### `docs/guides/security.md`

| Section | Content | Owner |
|---|---|---|
| What Abyss keeps secret | The Secrets; never shown in output, logs, `--json`, panels, manifests or Alerts; masked as `…abcd` where shown; `umask 077` and atomic writes | docs.md (from the overview's standing constraints) |
| Files and owners | Certificate private keys are read only to check them and are never logged or shown | docs.md (from the security decisions) |
| | The root-only layout and the `abyssctl` account; a re-run of `host setup` resets owners and modes | [host.md](host.md) |
| | The Instance record holds no Secrets; `instance.env` and `outcomes.json` are root-only (0600) | [store.md](store.md) |
| | No generated file holds a Secret | [render.md](render.md) |
| Containers | Instance containers run as the `abyssctl` account with every capability dropped and no published ports; the Proxy runs read-only with only `NET_BIND_SERVICE` | [render.md](render.md) |
| | The Proxy's hardening; keys at 0600 root under `proxy/certs/`; no ACME and no outbound certificate traffic; access logs off; the Proxy listens on 80 and 443 on all interfaces | [proxy.md](proxy.md) |
| Backups and logs | Backups are unencrypted and root-only, as sensitive as Instance data; copy them off the Host with care | [backup.md](backup.md) |
| | `upgrade-failed-*.log` files are root-only and may hold Instance data | [upgrade.md](upgrade.md) |
| | The activity log is readable by the `adm` group and holds no Secrets | [cli.md](cli.md) |
| Pushover keys | `abyssctl.env` holds the Pushover token at 0600 and must never be pasted into an issue | [host.md](host.md) |
| | The token is read from stdin or a hidden prompt, never as a command-line value; the user key is a command-line value, visible in shell history and `ps` while the command runs, but can't send a message on its own, and the activity log shows it only as `…abcd` | [alerts.md](alerts.md) (owner), [cli.md](cli.md) (the same paragraph) |
| What leaves the Host | Alerts go through Pushover's servers and carry only Instance names, Problems, times and Hostnames | [alerts.md](alerts.md) |
| | Version lookups are anonymous; Abyss stores no registry credentials | [versions.md](versions.md) |
| Docker | Abyss manages only the local daemon through the default context and ignores `DOCKER_HOST`, `DOCKER_CONTEXT` and `DOCKER_CONFIG` | [docker-and-locks.md](docker-and-locks.md) |
| | Rootless Docker and userns-remap are not supported | [host.md](host.md) |
| | The Operator is not added to the `docker` group; images are trusted by their digest pin only, with no Docker Content Trust or signature check in v1 | docs.md (orphan from the security decisions) |
| What's left to you | The firewall, SSH, OS updates and Docker daemon hardening; Docker-published ports bypass ufw (only 80 and 443 are published, and they are meant to be public) | [host.md](host.md) |
| | `exec` and `logs` show the Instance's own data; `reset-password` signs out existing sessions | [lifecycle.md](lifecycle.md) |
| | `logs` and `exec` output is never written to the activity log; `reset-password` connects the terminal straight to the container, so Abyss never sees the new password | docs.md (from the security decisions) |
| Reporting a vulnerability | A link to `SECURITY.md` | docs.md |

### `docs/guides/teardown.md`

The manual steps for removing Abyss from a Host, in this order. There is no `host teardown` verb in v1.

| Section | Content | Owner |
|---|---|---|
| Before you start | It can't be undone; copy any Backup you want to keep off the Host first | docs.md |
| Stop the timers | `systemctl disable --now` the Health check and Backup timers, so neither runs during the teardown | docs.md |
| Remove each Instance | `remove` each Instance first; each takes a `final` Backup | [lifecycle.md](lifecycle.md) |
| | An Instance's folder is `instances/<name>/`; a removed Instance's Backups are in `backups/removed/<name>-<stamp>/` until deleted by hand | [store.md](store.md) |
| Remove the Proxy and network | Take the Proxy down from its `compose.yaml`, remove the `abyssctl` network and the Actual and Caddy images | [host.md](host.md) |
| Remove the Host files | The four units, the MOTD script and the logrotate rule, then `systemctl daemon-reload` | [render.md](render.md) (owner), [host.md](host.md) (links) |
| | `/etc/abyssctl/`, `/var/log/abyssctl/` and the binary | [host.md](host.md) |
| Remove the account | The `abyssctl` system account and group | [host.md](host.md) |
| Delete the Data root | Including `backups/removed/`, deleted by hand | [host.md](host.md), [lifecycle.md](lifecycle.md) |
| Docker | Docker's apt source and key, if `host setup` installed Docker; removing Docker itself is optional | [host.md](host.md) |

### The reference (`docs/reference/`)

Generated; each spec supplies its pages through its help text. [cli.md](cli.md) owns the root page and the help
rules every verb follows.

| Pages | Owner |
|---|---|
| Group pages (`host`, `cert`, `proxy`, `backup`, `health`, `alerts`, `image`) | the spec of its verbs |
| `abyssctl` (root): exit codes, `--yes`, `--dry-run`, `--json` and `schema`, the stdout/stderr split, `NO_COLOR`, the activity log's path, a prompt needs a terminal or `--yes` | [cli.md](cli.md) |
| `version` | [foundation.md](foundation.md) |
| `host setup`: every flag, an omitted flag keeps its value, `--data-root` and `--base-domain` fixed after the first run | [host.md](host.md) |
| `cert install`, `cert list`, `cert remove` | [proxy.md](proxy.md) |
| `proxy upgrade` | [versions.md](versions.md) (owner), [proxy.md](proxy.md) (links) |
| `create`: arguments, flags, the default Hostname, prompts and locks | [lifecycle.md](lifecycle.md) |
| `create`: the Instance name rules (reserved `all`, `proxy`, `removed`) and Hostname rules | [store.md](store.md) |
| `create`: the optional `--version` | [versions.md](versions.md) |
| `remove`, `list`, `status`, `start`, `stop`, `restart`, `logs`, `exec`, `reset-password` | [lifecycle.md](lifecycle.md) |
| `upgrade` | [upgrade.md](upgrade.md) |
| `image prune` | [versions.md](versions.md) |
| `backup`, `backup list`, `backup prune`, `restore` | [backup.md](backup.md) |
| `health check`, `health clear` | [health.md](health.md) |
| `alerts setup`, `alerts test` | [alerts.md](alerts.md) |

### `CONTRIBUTING.md`

Short. H2 sections, in this order:

| Section | Content | Owner |
|---|---|---|
| Before you start | Questions go to Discussions, bugs to Issues with the template, security reports to `SECURITY.md`; open an issue before a large pull request | docs.md |
| How Abyss is built | The spec pack (`docs/specs/README.md`), `GLOSSARY.md`, the ADRs and `AGENTS.md`, whose coding rules bind every contributor; a spec's acceptance list is its definition of done; flag any conflict with an ADR. Code review takes its standards from `AGENTS.md` and this file | docs.md |
| Build and test | `make tools`, `make lint`, `make test`, `make build`, stamping `VERSION` to try a build on a test Host, the toolchain pin, the dependency rule | [foundation.md](foundation.md) |
| | Use a scratch test Host: a pre-release build writes its version as `TOOL_VERSION`, so later Releases are refused there as downgrades | docs.md (orphan from foundation.md's notes) |
| Docker fixtures | When to refresh them (a minimum moves, or a new Docker breaks parsing). In CI: run the `capture.yml` workflow from the Actions tab or `gh workflow run capture.yml`, then `gh run download <run-id>`, put each `fixtures-<engine>-<compose>` artifact's files in `internal/docker/testdata/<engine>-<compose>/`, review them and commit them by a normal pull request (the workflow never opens one). Locally: `make capture` on a throwaway Linux machine with the Docker to capture | [docker-and-locks.md](docker-and-locks.md) |
| Running the journeys | A Linux Host with Docker and sudo, never one with real Instances (a run wipes all Abyss state); `make image-cache` and when to run it again, with the optional read-only Docker Hub token; `make journey` with `HOST`, `RUN` and `UPDATE=1`; reading `dist/journey-out/`; moving a journey pin | [testing.md](testing.md) |
| Docs | Docs are part of done; run `make docs` after any help-text change and commit `docs/reference/`, never edited by hand; which guide carries what (this spec's index); `make test` checks links | docs.md |
| Pull requests | `make lint` and `make test` pass before each commit; CI (`ci.yml` and both journey jobs) must pass | docs.md |
| Releases | Releases are cut by the [Maintainer](../../GLOSSARY.md) as [release.md](release.md) describes; a contributor never tags | [release.md](release.md) |
| License | Contributions are under the MIT license, the same as the project; no CLA or sign-off | docs.md |

### `SECURITY.md`

| Section | Content | Owner |
|---|---|---|
| Supported versions | The latest Release only, meaning the latest published Release on GitHub | docs.md, [release.md](release.md) (the meaning) |
| Reporting a vulnerability | Through GitHub private vulnerability reporting (`https://github.com/DocCyblade/abyssctl/security/advisories/new`), never in a public issue or Discussion; what to include (`abyssctl version`, Host OS and arch, steps, impact), with no real Secrets | docs.md |
| What happens next | Best effort, with no fixed response time; the fix ships in the next Release, with a GitHub security advisory | docs.md |
| Not Abyss | Vulnerabilities in Actual itself go to the Actual project | docs.md |

Private vulnerability reporting is switched on by [release.md](release.md)'s repo-setup checklist.

### `.github/ISSUE_TEMPLATE/bug.yml` and `config.yml`

`bug.yml` (GitHub issue form): `name: Bug report`, `description: Something in abyssctl doesn't work as
documented`, `labels: [bug]`:

| Field | Kind | Required | Owner |
|---|---|---|---|
| Intro | markdown: never paste Secrets (`abyssctl.env`, Pushover keys, Certificate keys, Instance data or Backups); `logs` and `exec` output may show budget data; security problems go to `SECURITY.md` | n/a | docs.md |
| `version` | textarea, the output of `abyssctl version` | yes | docs.md |
| `host` | input, Host OS, version and arch (e.g. "Debian 13, amd64") | yes | docs.md |
| `docker` | input, Docker Engine and Compose versions | no | docs.md |
| `command` | input, the command as typed | yes | docs.md |
| `outcome` | textarea (rendered as text), the outcome line or the failure panel | yes | [cli.md](cli.md) |
| `log` | textarea (rendered as text), the run's lines from `/var/log/abyssctl/abyssctl.log` (read verbs aren't logged) | no | [cli.md](cli.md) |
| `expected` | textarea, what you expected | yes | docs.md |
| `other` | textarea, anything else | no | docs.md |
| `no_secrets` | checkbox, "I removed every Secret from this report" | yes | docs.md |

`config.yml`: `blank_issues_enabled: true`, and `contact_links`, each with `name`, `url` and `about`:

- "Questions and ideas", to the Discussions page, `about: Ask how to do something or suggest a change`.
- "Report a security problem", to `SECURITY.md` on GitHub, `about: Never in a public issue`, only while GitHub
  doesn't add its own "Report a security vulnerability" link to the issue chooser. The rule: the chooser shows one
  security link, never two. Once private vulnerability reporting is on and GitHub shows its link, this entry is
  left out.

### `LICENSE`

The MIT license text, with the line `Copyright (c) 2026 DocCyblade`. It is in the repo from the first commit.

### Makefile targets

`docs` and `docs-check`, as in [the generated reference](#the-generated-reference). Foundation owns the Makefile
and its base targets.

## 5. Depends on

- [foundation.md](foundation.md): `cli.Root()`, the Makefile, `ci.yml` (one step added), and `internal/archtest`.
- [cli.md](cli.md): the root `Long` help, the verb table and the help rules.
- [release.md](release.md): the asset names and URLs, the Release notes, the repo-setup checklist (Issues,
  Discussions, private vulnerability reporting).
- Every spec's section 8, for the content the index assigns to it.
- Dependencies: none new. `github.com/spf13/cobra/doc` is part of the Cobra module (with its own requirement for
  Markdown and man output), which foundation already allows.

The import rules this spec adds to `internal/archtest`:

- `internal/cli/gendocs` imports only the standard library, Cobra, `cobra/doc` and `internal/cli`;
- `internal/docscheck` imports only the standard library, and nothing imports it.

## 6. Decisions

- **MIT, the same as Actual.** One root `LICENSE`, no per-file headers.
- **Guides apart from specs.** `docs/guides/` is for Operators, `docs/specs/` for whoever builds Abyss. Both use
  the one root `GLOSSARY.md`.
- **A generated, checked-in reference.** `cobra/doc` writes one Markdown page per command, so the reference can't
  drift from `--help`, and the pages are readable on GitHub without a build. CI fails when they are stale. No man
  pages in v1: on the Host, `--help` is the reference.
- **`make docs-check` compares in memory** instead of running `git diff`, so it gives the same answer in a dirty
  work tree, and it names each stale page.
- **Docs are written with the code.** Each spec names its docs and carries a (review) item, and this spec is the
  index that gives every item one home. Its last ticket is the pass that checks the index.
- **Docs tests are offline.** Links, anchors, headings and the file set are checked in `make test`. External
  links are not fetched, so a test never depends on the network.
- **GitHub Issues for bugs, Discussions for questions.** The README sends nobody to Actual's Discord or tracker,
  because Abyss is not part of Actual.
- **The Release notes are the changelog**, with the Release check summary in each ([release.md](release.md)).
  There is no `CHANGELOG.md` to keep in step.
- **The fewest community files that do the job:** `SECURITY.md`, a short `CONTRIBUTING.md` and one bug form.
  Blank issues stay allowed. No code of conduct, pull-request template or feature-request template in v1.
- **No troubleshooting or FAQ page** until real issues show what one should say.
- **The README install has no install script**, keeping to the standing constraints: the Operator downloads,
  checks and runs one binary, and can read every step.

## 7. Later

None of its own. The other specs' Later items that would change the docs (the Tool update command, `host teardown`,
off-host Backups) bring their own docs items when they land.

## 8. Docs it changes

All the files in [Files and formats it owns](#4-files-and-formats-it-owns). The other specs' items are carried as
the index says.

## 9. Acceptance list

Generator tests are in `internal/cli/gendocs`; repo docs tests are in `internal/docscheck`. Both run in
`make test`, unprivileged, on Linux and macOS.

### Generator and help tests

1. `make docs` writes one page per available command (the root, every group and every verb) under Cobra's names,
   and none for help or `completion`. → `TestGenerate_OnePagePerCommand`
2. Two runs give byte-identical pages, also with a different `TZ`, `HOME`, `LANG` and extra environment
   variables, and no page holds a date, Cobra's auto-generated tag or `buildinfo.Version`. →
   `TestGenerate_Deterministic`
3. Every page starts with the generated-file comment, and every link between pages names a page that was
   written. → `TestGenerate_HeaderAndLinks`
4. `make docs` deletes an `abyssctl*.md` page whose command no longer exists. → `TestGenerate_RemovesStale`
5. `make docs-check` writes nothing, exits 0 when the folder matches, and otherwise exits 1 with one `stale`,
   `missing` or `extra` line per page and `run make docs`. → `TestCheck_ReportsDiffs`
6. The `ci.yml` job runs `make docs-check` after `make test`, on every push and pull request, so a stale page fails
   CI. → `TestCI_RunsDocsCheck` (in `internal/docscheck`, reading `.github/workflows/ci.yml`)
7. Every runnable command has a `Short` with no closing full stop, a `Long` and an `Example`; every group has a
   `Short`; no help text holds a Secret-shaped value as defined in [Help text rules](#help-text-rules). →
   `TestHelp_Complete`
8. The root `Long` names exit codes 0 to 4, `--yes`, `--dry-run`, `--json` and `schema`, stdout and stderr,
   `NO_COLOR`, `/var/log/abyssctl/abyssctl.log` and the terminal-or-`--yes` rule. → `TestRootHelp_CoversContract`
9. The import rules hold for `internal/cli/gendocs` and `internal/docscheck`. → `TestImportRules` in
   `internal/archtest`

### Repo files

10. The file set is as in [Files and formats](#4-files-and-formats-it-owns): `README.md`, `LICENSE`,
    `SECURITY.md`, `CONTRIBUTING.md`, both issue-template files and exactly the six guides exist; no
    `CHANGELOG.md`, code of conduct, pull-request template or feature-request template does. → `TestDocSet`
11. `LICENSE` is the MIT text with `Copyright (c) 2026 DocCyblade`, and no `.go` file holds an SPDX or copyright
    header. → `TestLicense`
12. The README opens with the fixed text word for word, has the eight H2 sections in order, and its Documentation
    section links the six guides, `docs/reference/abyssctl.md`, `GLOSSARY.md`, `ROADMAP.md`, `CONTRIBUTING.md`,
    `SECURITY.md` and `docs/specs/README.md`. → `TestReadme_Structure`
13. The README's install block uses the three asset names under `releases/latest/download/`,
    `sha256sum -c --ignore-missing SHA256SUMS`, `chmod +x` and `sudo ./abyssctl-linux-$ARCH host setup`, and the
    README links the Releases page. → `TestReadme_InstallBlock`
14. Every guide and `CONTRIBUTING.md` has the H2 sections given above, in that order. → `TestGuides_Sections`,
    `TestContributing_Sections`
15. Every relative link and anchor in every Markdown file resolves. → `TestLinks`
16. `bug.yml` has `name`, `description` and `labels`, the fields in the table with their kinds and required
    flags, the Secrets warning and the checkbox; `config.yml` allows blank issues, and every contact link has
    `name`, `url` and `about`. → `TestIssueTemplates`
17. No tracked text file in the repo, except the test's own source, names an earlier tool that managed Actual
    servers. → `TestNoPredecessorNames`

### Review

18. Every row of the docs index is carried in its file, in its section, once; each other spec's docs item is
    satisfied by the rows that name it. → (review), the final docs pass
19. The README and guides follow the writing rules: glossary links at first use, the glossary's words, `sudo` on
    every command, the time rule, no Secret in any example. → (review)
20. `SECURITY.md` says what its table lists, and the issue chooser shows exactly one security link. → (review)
21. Each verb's help text says what its verb-table row and its spec's section 8 list for its page. → (review);
    the pages match the help text by `make docs-check`
