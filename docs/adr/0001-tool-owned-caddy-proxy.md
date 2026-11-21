# The tool runs its own Caddy Proxy container

The tool runs one Caddy container as the Host's only Proxy. It owns ports 80 and 443, joins a private
Docker network with every Instance, and is re-rendered, validated and reloaded by the tool on every
create, remove and Certificate change. Instances publish no Host ports.

We chose this over publish-only (the Operator routes each Instance in their own proxy), because
adding an Instance stays one command and the Proxy can never drift from the Instance list. We chose
Caddy over nginx even though the Operator has always used nginx: the generated per-Instance config is
three lines with no Actual-specific workarounds (COOP/COEP headers, body size, upstream DNS), a
stopped Instance can't block a reload, and ACME later is a small step. Traefik was rejected because
it needs the Docker socket in an internet-facing container and can't take Certificates via labels.

## Consequences

- The Proxy sits behind one deep module (fed the Instances and Certificates; renders, validates,
  reloads), so swapping Caddy for nginx later stays contained.
- With Operator-supplied Certificates, Caddy's automatic certificate fetching must stay disabled
  (`auto_https disable_certs`).
