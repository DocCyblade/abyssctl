# Contributing to Abyss

## Before you start

## How Abyss is built

## Build and test

You need Go and `make`, on Linux or macOS. Nothing here needs root.

```
make tools    # once: installs the pinned dev tools into ./bin/
make lint     # golangci-lint with the repo's .golangci.yml
make test     # every module test, with the race detector; offline and unprivileged
make build    # dist/abyssctl-linux-amd64 and dist/abyssctl-linux-arm64
```

Run `make lint` and `make test` before every commit. The dev tools' versions are pinned in the Makefile
(`GOLANGCI_LINT_VERSION`, `GOTESTSUM_VERSION`); they never enter `go.mod`. Run `make tools` again after a pin
moves.

`make test` needs cgo for the race detector, so it sets `CGO_ENABLED=1`; only tests use cgo, and every build is
`CGO_ENABLED=0`. The journeys are not part of `make test`; see [Running the journeys](#running-the-journeys).

### Trying a build on a test Host

A plain `make build` stamps the version `0.0.0-dev`. That binary runs `version` and `--help`, and refuses every
other command, because it is an unstamped development build. To try a build on a test
[Host](GLOSSARY.md), stamp it with a pre-release version:

```
make build VERSION=0.2.0-dev.3
scp dist/abyssctl-linux-amd64 testhost:
ssh testhost sudo ./abyssctl-linux-amd64 version
```

`COMMIT` defaults to `git rev-parse HEAD`. A [Release](GLOSSARY.md) is built by the same `make build`, with
`VERSION` set to its tag without the `v`.

Use a scratch test Host, never one with Instances you care about. A pre-release build writes its version as
`TOOL_VERSION` in the [Instance records](GLOSSARY.md) it writes, so a later Release with a lower version is
refused there as a downgrade.

### The toolchain pin

`go.mod` has a `go 1.27` line and a `toolchain go1.27.N` line. With the default `GOTOOLCHAIN=auto`, the `go`
command fetches that toolchain if yours is older, so every build and CI run uses the same one. Moving the
toolchain is a commit of its own.

### The dependency rule

`go.mod` requires only `github.com/spf13/cobra`, `modernc.org/sqlite` and `golang.org/x/term`. A new dependency
needs a written reason in its owning spec's Decisions section and an entry in the import-rules test
(`internal/archtest`), which fails `make test` for any module outside the allowed list. Cobra is used only in
`internal/cli`; [AGENTS.md](AGENTS.md) has the rest of the import and coding rules.

## Docker fixtures

## Running the journeys

## Docs

## Pull requests

## Releases

## License
