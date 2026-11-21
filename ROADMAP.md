# Roadmap

What comes after v1. v1 itself is described in the [spec overview](docs/specs/README.md). Capitalised terms are
defined in [GLOSSARY.md](GLOSSARY.md).

There are no version numbers or dates. **Next** is the order things are built in after v1. **Later** is grouped by
theme and unordered within a theme. **Not planned** says what won't be built, and why. Each item says what it is
and what it builds on; where a spec owns the v1 behaviour that an item extends, that spec is linked. When an item is
built, the spec that owns the seam it extends gains it (a new module gets its own spec), with any new verb in
[cli.md](docs/specs/cli.md)'s verb table, any new journey in [testing.md](docs/specs/testing.md) and its docs in
[docs.md](docs/specs/docs.md)'s index.

## Next

1. **New tool version notice.** `status` and the [MOTD](GLOSSARY.md) say when a newer [Release](GLOSSARY.md) of
   Abyss is out, the way they already show newer Actual and Caddy versions. Builds on: the
   [Health check](GLOSSARY.md)'s version lookups and MOTD ([health.md](docs/specs/health.md)), with the latest
   Release read from GitHub Releases and compared by `CompareVersions`.
2. **Tool update command.** An [Operator](GLOSSARY.md)-run [Tool update](GLOSSARY.md): download the newer Release's
   binary, check it against `SHA256SUMS` and run the new binary's `host setup`, which installs it and fixes drift.
   It refuses a downgrade. Builds on: the Release assets ([release.md](docs/specs/release.md)), the `host setup`
   re-run ([host.md](docs/specs/host.md)) and the new tool version notice.
3. **Clone.** Clone an [Instance](GLOSSARY.md) into a new one, [Restore](GLOSSARY.md) a [Backup](GLOSSARY.md) into
   a new Instance, and restore from the Backups a removed Instance left in the [Data root](GLOSSARY.md)'s
   `backups/removed/`. Builds on: Backup and Restore ([backup.md](docs/specs/backup.md)), and `create` and `remove`
   ([lifecycle.md](docs/specs/lifecycle.md)).
4. **Email Alert channel.** [Alerts](GLOSSARY.md) by email as well as Pushover. Builds on: the `Send(alert)` seam,
   as a second Adapter beside Pushover; the Health check does not change ([alerts.md](docs/specs/alerts.md)).
5. **Pushover priority per Problem kind.** The priority of a Pushover message is chosen by the kind of
   [Problem](GLOSSARY.md), for example high for an Instance down or a failed [Rollback](GLOSSARY.md). In v1 every
   started Problem is sent at normal priority and every recovery at quiet priority. Builds on: the Pushover Adapter
   ([alerts.md](docs/specs/alerts.md)).
6. **Off-host Backups, encrypted before they leave the Host.** Copy Backups to storage off the
   [Host](GLOSSARY.md), encrypting each one on the Host first, so the copy holds nothing readable without the key.
   Builds on: Backup and its documented Backup folder ([backup.md](docs/specs/backup.md)).

## Later

### Instances

- **Rename and `set-hostname`.** Change an Instance's name, or its [Hostname](GLOSSARY.md), after creation. Builds
  on: the [Instance record](GLOSSARY.md) ([store.md](docs/specs/store.md)) and the Instance commands
  ([lifecycle.md](docs/specs/lifecycle.md)).
- **Actual settings.** Commands for Actual's own server settings, including OpenID and multi-user, with
  per-Instance settings kept in the Instance record as an allow-list of keys. Builds on: the Instance record
  ([store.md](docs/specs/store.md)) and the generated `compose.yaml` ([render.md](docs/specs/render.md)).
- **`doctor`.** Compares every Instance record with Docker and the disk, and reports what is out of step.
  Builds on: the Instance store ([store.md](docs/specs/store.md)) and `status`
  ([lifecycle.md](docs/specs/lifecycle.md)).
- **Read-only root filesystem for Instances.** Run each Instance container with Docker's `read_only: true`, so the
  container can write only to its mounted `/data` folder and to temporary space, and a buggy or compromised server
  can't change its own program files. Blocked on checking whether Actual writes anywhere outside `/data`. Builds
  on: the generated `compose.yaml` ([render.md](docs/specs/render.md)).

### Backups

- **Encrypted Backups on the Host.** Encrypt Backups at rest on the Host too, not only the off-host copies. In v1
  they are readable by root only. Builds on: Backup ([backup.md](docs/specs/backup.md)).

### Upgrades

- **Nightly and edge versions.** Actual's nightly and edge builds as targets for `create` and
  [Upgrade](GLOSSARY.md) (for example `--version nightly`); v1 offers only stable versions. Builds on: the version
  lookups ([versions.md](docs/specs/versions.md)) and Upgrade ([upgrade.md](docs/specs/upgrade.md)).
- **Unattended and bulk Upgrade.** Upgrade without the Operator at the keyboard, and many Instances in one run (for
  example `upgrade --all`, one Instance at a time, stopping at the first Rollback). Builds on: Upgrade
  ([upgrade.md](docs/specs/upgrade.md)).
- **Breaking-change warnings.** Read Actual's release notes and show any breaking-change warnings in the Upgrade
  summary panel; v1 shows the release notes link only. Builds on: Upgrade's summary panel
  ([upgrade.md](docs/specs/upgrade.md)).

### Proxy and TLS

- **ACME / Let's Encrypt.** [Certificates](GLOSSARY.md) issued automatically. Hosts are often internal-only, so this
  needs the DNS-01 challenge and a custom Caddy build (`xcaddy`) with a DNS provider plugin. Builds on: the
  [Proxy](GLOSSARY.md) ([proxy.md](docs/specs/proxy.md)).
- **Sharing the Host.** Run the Proxy on custom ports, or bind it to one IP address, so other services can share
  the Host. Builds on: the Proxy ([proxy.md](docs/specs/proxy.md)).
- **HSTS option.** A setting that turns on HSTS, for Hosts reachable from the internet. Builds on: the generated
  site files ([proxy.md](docs/specs/proxy.md), [render.md](docs/specs/render.md)).
- **Access logs as a setting.** Turn the Proxy's access logs on or off; they are off in v1. Builds on: the Proxy
  ([proxy.md](docs/specs/proxy.md)).

### Alerts

- **Webhook Alert channel.** Alerts posted to a webhook. Builds on: the `Send(alert)` seam, as one more Adapter
  ([alerts.md](docs/specs/alerts.md)).
- **Update-available Alerts.** Send an Alert when a newer Actual or Caddy version becomes available; in v1 these
  show only in `status` and the MOTD. Builds on: the Health check's version lookups
  ([health.md](docs/specs/health.md)), sending through the existing `Send(alert)` seam.

### Host

- **`host teardown`.** One command that removes Abyss from a Host. In v1 the teardown guide lists the manual steps.
  Builds on: [Bootstrap](GLOSSARY.md) ([host.md](docs/specs/host.md)).
- **Moving the Data root, and changing the Base domain.** Move the [Data root](GLOSSARY.md) to another folder, or
  change the [Base domain](GLOSSARY.md), after `host setup` has set them. Builds on: Host settings and
  Bootstrap ([host.md](docs/specs/host.md)).

### Tool

- **`.deb` package and apt repository.** Install and update Abyss with the Host's package manager. Builds on: the
  Release assets ([release.md](docs/specs/release.md)) and install through `host setup`
  ([host.md](docs/specs/host.md)).
- **Key-based signatures on Releases.** Sign each Release (cosign, minisign or GPG), so a download can be checked
  against a key as well as `SHA256SUMS`. Builds on: the Release assets ([release.md](docs/specs/release.md)).

### Convenience

- **Shell completion.** Tab completion of commands, flags and Instance names, through Cobra's `completion`
  command, which is off in v1. Builds on: [cli.md](docs/specs/cli.md).
- **`abyssctl log`.** Show the activity log (`/var/log/abyssctl/abyssctl.log`) without opening the file. Builds
  on: [cli.md](docs/specs/cli.md).

## Not planned

- **More than one Host.** Abyss manages the Host it runs on; an Operator with several Hosts runs it on each.
- **Hosting for others (SaaS).** Abyss serves one Operator on their own Host; it has no sign-up, billing or
  separation between strangers.
- **A web UI.** Abyss is a command-line tool run as root on the Host; a web UI would be one more service to expose
  and secure.
- **More than one administrator, or admin roles.** There is one Operator, and root on the Host is the only access
  control.
- **Migrating Instances from other tools.** An Operator moving to Abyss sets up a new Host and moves each budget
  with export and import in Actual's web UI.
- **Unattended Tool updates.** A Tool update replaces the binary that manages every Instance and may recreate the
  Proxy, so the Operator runs it and watches it.

## Dropping or rethinking an item

An item is dropped or rethought when any of these holds:

- Actual ships it upstream, making ours redundant.
- It would break one of the [standing constraints](docs/specs/README.md#standing-constraints), such as one static
  binary needing nothing on the Host beyond Docker and a few packages, no Docker socket inside any container, or no
  plugins.
- It can't be built as a deep module behind an existing seam without breaking v1's interfaces.
