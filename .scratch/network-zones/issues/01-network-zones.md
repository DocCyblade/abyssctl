# Network zones and per-Instance internet access

Type: grilling
Status: needs-triage

**Grill before `host` and `render` are ticketed** (build steps 4 and 6), because the answer changes both.
`AGENTS.md` holds that gate. The specs are **not** changed for this yet. Raised 2026-11-21 in the design chat that
produced this spec pack, after its last review.

If this repo's tracker is GitHub Issues, file this as an issue, delete this file and point `AGENTS.md` at the issue.

## Question

Should Abyss split its Docker networking into zones, so that an Instance reaches the internet only when it needs to
(bank sync), instead of every Instance sharing one bridge network that can reach the internet?

## Today's spec

- One network, `abyssctl`, created by `host setup` as `network create --driver bridge <name>` with a subnet Docker
  picks ([docker-and-locks](../../../docs/specs/docker-and-locks.md), [host](../../../docs/specs/host.md)
  apply step 8). It is not `--internal`, so **every Instance can reach the internet**.
- The Proxy and every Instance join it. Only the Proxy publishes ports.
- `ACTUAL_TRUSTED_PROXIES` is that network's subnets, read back by the caller
  ([render](../../../docs/specs/render.md)).
- `host setup` never edits `daemon.json`; Docker settings are the Operator's.
- Docker 24 / Compose 2.24 are the minimums.

## Why

Bank sync (SimpleFIN, GoCardless, Pluggy) is called from the Actual server, not the browser, so an Instance that
uses it needs outbound internet. Most Instances don't. The Operator runs servers today with an internal-only
network for everything and an external network only for what must reach out, and wants Abyss to work the same way:
an Instance with no internet can't leak budget data out, even if something inside it goes wrong.

## Proposal (from the Operator, to grill)

Three zones:

| Zone | What it is | Who joins it |
|---|---|---|
| Management | `docker0` / the Operator's `bip` | nothing of Abyss's; the Operator's own address plan |
| Internal | `abyssctl`, created `--internal` (no internet) | the Proxy and every Instance |
| External | a second network, e.g. `abyssctl-external` (internet; published ports) | the Proxy (to publish 80/443 and leave through it), and only the Instances that need internet |

- `create` asks (or takes a flag such as `--internet`) whether the Instance needs internet access or bank sync.
  If yes, the Instance also joins the external zone, gets an address there only to get out, and is still reached
  by the Proxy only over the internal zone. It publishes nothing.
- The answer is stored in the Instance record, e.g. `INTERNET=1`, and rendered into `compose.yaml`.

## Questions to settle

1. Zone names, and whether the internal network keeps the name `abyssctl` (so the v1 names table barely changes).
2. The record key name and values (`INTERNET=1`? `ZONE=external`?), and the verb to change it later
   (`abyssctl set <name> --internet`? an edit + re-render?).
3. Is "needs internet" a yes/no, or should it be bank-sync-specific? Actual may also reach out for other things
   (version checks, OpenID). What breaks inside an Instance with no internet?
4. Default for `create`: no internet (safer), with a prompt on a TTY and a flag for scripts?
5. Default route: a two-network container must leave through the external zone. Compose `gw_priority` needs Docker
   28 / Compose 2.33, above today's minimums (24 / 2.24). Raise the minimums, or find a way that works on 24?
   (Does a container on an `--internal` network get any default route at all on Docker 24–28?)
6. `ACTUAL_TRUSTED_PROXIES` for a two-network Instance: the Proxy may reach it over either network (the container
   name resolves on both), so list both subnets, or pin the Proxy to the internal alias only?
7. Subnets: keep "Docker picks", or let `host setup` take `--internal-subnet` / `--external-subnet` for an Operator
   with an address plan (the field-test server pins 10.255.99.0/28 and 10.254.99.0/29)?
8. `host setup` drift: an existing `abyssctl` network that is not `--internal` (a Host from before this change)
   can't be switched in place; it has to be recreated with every container off it. Refuse with a hint, or do it?
   If this lands before v1 ships, there are no such Hosts.
9. Health check: should it check that a no-internet Instance really has none, and that an internet one can reach out?
10. Specs touched: overview (Host layout, Docker names, standing constraints), GLOSSARY (Zone), docker-and-locks
    (`CreateNetwork --internal`, subnet options, the fake), host (apply step 8, inspect conflicts, drift), render
    (compose golden files, trusted proxies), store (record key), lifecycle (`create` prompt/flag, `status`/`list`
    showing it), proxy (two networks), cli (verb table, `--json` schemas), testing (a journey that checks egress),
    docs (guides). ADR 0005 if adopted. When settled, remove the gate in `AGENTS.md`.

## Field test

The Operator's interim server (budgetapp00) is built by hand in the Abyss layout from a runbook kept in the design
chat (`Quick Actual on Docker runbook.md`, not in this repo), and already follows this proposal:

- `abyssctl` is `--internal`, subnet 10.255.99.0/28; the Proxy and every Instance join it.
- `abyssctl-external` (10.254.99.0/29) carries the Proxy and the one Instance that uses bank sync (`family`).
- `family`'s record has `INTERNET=1`, and its `ACTUAL_TRUSTED_PROXIES` lists both subnets.

What we learn running it feeds this ticket.
