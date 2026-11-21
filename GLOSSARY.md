# Abyss

Running and maintaining several independent Actual Budget sync servers on one Docker host,
through their whole lifecycle.

## Language

**Host**:
The single machine running Docker on which all Instances live.
_Avoid_: Server (ambiguous with sync server), box, node

**Operator**:
The one person who administers the Host and its Instances.
_Avoid_: Admin, user (Actual has its own users), Maintainer (the person who cuts Releases)

**Bootstrap**:
Preparing a Host so the tool can run Instances on it: installing Docker if it is missing, starting
the Proxy and setting up the schedule for Health checks and Backups. Safe to repeat: a re-run puts
back anything that has drifted and never changes the contents of Instance data or Backups (it may
fix their ownership and modes).
_Avoid_: Install, provision

**Data root**:
The one folder on the Host, chosen at Bootstrap, that holds every Instance's data, all Backups and
the Proxy's files.

**Instance**:
One independent Actual sync server with its own data, running as a container on the Host.
_Avoid_: Server, site, tenant, container (the container is only how it runs)

**Instance name**:
The short name the Operator gives an Instance at creation. It identifies the Instance everywhere and
is fixed for the Instance's life.

**Instance record**:
The small file in an Instance's own folder that says the Instance exists and how it is configured.
It is the only record of an Instance. Everything else about the Instance is generated from it.
_Avoid_: Registry, inventory, config (Actual has its own config)

**Orphan**:
A container that looks like one of the tool's Instances but has no Instance record. The tool reports
it and never removes it on its own.

**Hostname**:
The fully qualified name that browsers and Actual apps use to reach one Instance. Each Instance has
exactly one.

**Base domain**:
The domain, set once for the Host, under which each Instance gets its default Hostname
(`<instance-name>.<base domain>`). The Operator can give an Instance a different Hostname instead.

**Proxy**:
The tool's single reverse proxy on the Host. It receives all browser and app traffic and passes
each request to the Instance whose Hostname it names. Instances are reachable only through it.
_Avoid_: Ingress, load balancer, gateway

**Certificate**:
A TLS certificate and its key, supplied by the Operator, covering one or more Hostnames (a
wildcard covers every Hostname one level under its domain).
_Avoid_: Cert bundle, SSL

**Backup**:
A complete, point-in-time copy of one Instance's data and its Instance record, kept on the Host.
_Avoid_: Snapshot, export (Actual's web UI has its own budget export)

**Backup kind**:
Why a Backup was made: by the Operator, on schedule, before an Upgrade, before a Restore, or as the
final Backup when an Instance is removed.

**Retention**:
The rule for which scheduled Backups are kept automatically. Other kinds of Backup are only removed
by the Operator.

**Restore**:
Replacing an Instance's data with one of its own Backups, which also brings back the Actual version
that made it. Always followed by a Regroup.
_Avoid_: Rollback (that is the automatic undo of a failed Upgrade)

**Regroup**:
Giving a restored budget a new sync identity, so devices that synced after the Backup are told the
budget was reset instead of silently re-applying their newer changes.
_Avoid_: Reset sync (Actual's client-side action, which forces a re-upload instead)

**Upgrade**:
Moving one Instance to a newer stable Actual version, chosen by the Operator.

**Proxy upgrade**:
Moving the Proxy to a newer Caddy version, chosen by the Operator.
_Avoid_: Upgrade (that is for an Instance)

**Rollback**:
The automatic return of an Instance to its pre-Upgrade version and data when an Upgrade fails or was
interrupted. Going back to an older version later, by choice, is a Restore.

**Release**:
A published version of Abyss that Operators download and install. Actual and Caddy have versions, not
Releases.
_Avoid_: Build, version (on its own, it doesn't say whose)

**Maintainer**:
The person who owns the `abyssctl` repo and cuts Releases.
_Avoid_: Operator (the Host's administrator)

**Tool update**:
Replacing Abyss on a Host with a newer Release, chosen by the Operator.
_Avoid_: Upgrade (that is for an Instance), self-update

**Health check**:
The tool's periodic look at every Instance, the Proxy, the Certificates and the Host's free disk
space, to find Problems before the Operator does. It is the only thing that sends Alerts.
_Avoid_: Healthcheck (Docker's own container healthcheck, which is a different thing)

**Problem**:
Something wrong that the Health check currently counts against the Host or an Instance. A **Live
Problem** is found afresh on every Health check, such as an Instance down or low disk space. An **Event
Problem** comes from something that happened, such as a failed Backup or an automatic Rollback, and
stays until the same operation later succeeds or the Operator clears it.
_Avoid_: Incident, issue

**Secret**:
Anything that must never appear in what the tool shows or sends: the Operator's Alert credentials,
Certificate private keys, and everything an Instance stores, which is why every Backup is one too.
_Avoid_: Credentials (only part of it), sensitive data

**Alert**:
A message telling the Operator that a Problem started or ended, such as a failed Backup, an
Instance down, an automatic Rollback or low disk space.
_Avoid_: Notification (too broad; update-available notices are not Alerts)

**MOTD**:
The summary of current Problems and available updates that the Host shows at login, drawn from the
latest Health check. It is a view, not an Alert.
_Avoid_: Banner, Alert channel
