# Instances have no Internet access unless the Operator turns it on

Every Instance joins the `abyssctl` network, which is created `--internal`, so it can be reached only through the
Proxy and can reach nothing outside the Host. [Internet access](../../GLOSSARY.md) is a per-Instance yes/no, off
by default, turned on at `create --internet` or later with `set <name> --internet=on|off`. An Instance with it
also joins a second, ordinary bridge network, `abyssctl-external`, and leaves through it; it still publishes no
port and is reached by the Proxy as before. The Proxy joins both networks, because Docker publishes ports only
from a network that is not internal. The record holds `INTERNET=1` when it is on.

We chose this over one shared network that reaches the internet, because most Instances never need to reach out,
and an Instance that can't reach out can't leak budget data even if something inside it goes wrong. Bank sync
(GoCardless, Enable Banking, SimpleFIN, Pluggy, Akahu), OpenID login and Actual's plugin CORS proxy are called
from the server, so an Instance that uses any of them needs Internet access; the rest of Actual works without it.
We chose one yes/no over finer access (LAN only, or a list of allowed hosts), because that needs firewall rules
on the Host, which Abyss leaves to the Operator. An Instance whose OpenID provider is on the LAN therefore needs
Internet access too. We chose this before v1, because changing a Host's network later means recreating it with
every container off it.

## Consequences

- The Docker Engine minimum is 25.0.5. Docker 24's embedded DNS forwards external lookups for containers on an
  internal network only, so an Instance without Internet access could still send data out through DNS
  (CVE-2024-29018); 25.0.5 and 26.0.0 forward only for containers with external access. The Compose minimum
  stays 2.24.
- No gateway setting is needed. On every Engine from 24 to 28, a container on an internal and an ordinary bridge
  network takes its default route from the ordinary one, whatever the names or join order. Compose's
  `gw_priority` (Engine 28, Compose 2.33.1) matters only with two ordinary networks.
- `ACTUAL_TRUSTED_PROXIES` lists both networks' subnets for an Instance with Internet access, since the Proxy may
  reach it over either.
- Docker picks both subnets. An Operator with an address plan creates the two networks before `host setup`, which
  uses an existing network of the right kind as it is and refuses one of the wrong kind; it never recreates a
  network.
- An Instance container whose networks don't match its record is drift for `host setup` and a Problem for the
  Health check.
