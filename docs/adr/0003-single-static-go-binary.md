# The tool is one static Go binary

The tool ships as one statically linked Go binary (no cgo) for linux/amd64 and linux/arm64. Every file
it writes (compose, site, Caddyfile, systemd units, MOTD script) and every version it pins (Caddy image,
minimum Docker and Compose) is compiled into it. It reaches Docker only through the `docker` and
`docker compose` CLI.

The Host may need nothing beyond Docker, a few packages and systemd, and `host setup` must run before
Docker exists. That rules out Python or Node, which minimal Debian and TurnKey don't guarantee, and
it rules out shipping the tool only as a container. We chose Go over Bash, which is the other
zero-install option. About 15 verbs with all-or-nothing create, automatic Rollback, Alert state
transitions and record validation need types, deep modules and fakes at the seams, and Bash offers
none of that or good tests. Bash also has no SQLite for Regroup without a container. We chose Go over
TypeScript compiled with Bun or Deno (a much larger binary and a younger toolchain), even though the
earlier tools used TypeScript and Python. We also chose it over Rust, which is slower to write and
gains nothing for a tool that mostly orchestrates. We chose the Docker CLI over the Engine API
because the tool already generates `compose.yaml`, so the API would mean rebuilding Compose. The CLI
also lets the Operator rerun any step by hand.

## Consequences

- The prior art is reference only. No code is ported from the earlier tools.
- A tool version fixes one set of generated files and pins. A Proxy image bump ships as a new Release.
- Distribution means getting one file onto the Host. `host setup` copies it to `/usr/local/bin/abyssctl`.
- Tests fake the Docker adapter (an implicit Go interface), and the journey tests run the real binary
  against real Docker.
- v1 allows three dependencies: Cobra (with `cobra/doc`), `modernc.org/sqlite` (for the offline Regroup) and
  `golang.org/x/term` (for hidden input and TTY detection). Only the CLI layer uses Cobra. Each one's reason
  is in its owning spec's Decisions section.
