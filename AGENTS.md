# AGENTS.md

Rules for agents building and changing Abyss (command `abyssctl`). `CONTRIBUTING.md` points human contributors
here.

## Start here

- Read `docs/specs/README.md` first: the problem, the standing constraints, the module map, the conventions every
  module follows, and the specs and ADRs in build order.
- Use the words in `GLOSSARY.md`, with its capitalisation, in code, tests, help text, docs and commit messages.
  Each entry's _Avoid_ list names the words to leave out.
- Respect the ADRs in `docs/adr/`. When a spec, a ticket or a request conflicts with an ADR, stop and put the
  conflict to the Maintainer. An ADR changes only by a new ADR, written only with the Maintainer's agreement.
- A format is written once, in its owner's spec. When a spec links to another spec for a format, rule or table,
  the linked spec is the one to follow.

## How a spec is built

- One spec at a time, in the overview's build order: `/to-tickets` on the spec, then `/implement-spec`. A module
  spec's tickets include its own CLI verb and journey slice, so each ticket is a vertical slice. `cli.md`,
  `testing.md` and the step-1 part of `docs.md` are ticketed with `foundation`, as the overview says.
- **The seams are pre-agreed.** Each spec's Interface section and acceptance list are its test seams: build to
  them without asking. Internals the spec leaves open are the implementer's choice.
- **Done** is the overview's "A spec is done", with two additions: each acceptance item's test has the name the
  item gives, and the docs the spec names are updated in the file and section that `docs.md`'s docs index gives
  them. Items marked **(review)** are done when `/code-review`'s Spec axis or the Maintainer has checked them
  against the spec.
- A change to what Abyss does updates the spec that owns it, and the guide rows that cover it, in the same
  change. Specs stay in final form: present tense, no history, and links only to other specs, the ADRs and repo
  files, never to tickets.

## Coding rules

- **Modules emit events and never print.** A module reports progress through the `Emitter` from its constructor
  and returns values, `Summary` plans and errors. Only `internal/cli` prints, prompts and picks exit codes.
  `make lint` enforces this (forbidigo).
- **Typed errors.** Use the `internal/op` errors: `Refused` only before anything has changed, `NeedsOperator`
  always with a hint, `RolledBack` only from Upgrade's Rollback; any other failure is a plain wrapped error
  (exit 1). `foundation.md` owns the rules.
- **Plans.** Each operation the CLI shows a panel for (`cli.md`'s verb table) has a `Plan…` twin that neither
  locks nor acts.
- **The CLI takes the locks**, from `cli.md`'s lock table; a module states "caller holds X" and never locks,
  except the two the overview names: Health try-locks each Instance in `Check`, and Lifecycle's `Create` locks
  the staged Instance.
- **Time** comes only from the `op.Clock` the module was given, never from `time.Now()`. `make lint` enforces
  this (forbidigo).
- **Every path comes from the paths value** (`internal/hostfs`). Every file write goes through
  `hostfs.WriteFile` or `hostfs.Create`, and every folder through `hostfs.EnsureDir`, except the cases
  `foundation.md` lists. `make lint` enforces this for file writes (forbidigo).
- **Secrets stay hidden.** A Secret never appears in an event field, error message, panel, `--json`, log line,
  manifest, Alert or test golden file; Pushover credentials appear only as `op.Mask(value)`.
- **Cobra only in `internal/cli`** (and its `gendocs`); `cmd/abyssctl/main.go` stays tiny.
- **Dependencies:** v1 allows Cobra, `modernc.org/sqlite` and `golang.org/x/term`, and `go.mod` has no other
  direct requirement. A new dependency needs a written reason in its owning spec's Decisions section and an entry
  in the import-rules test (`internal/archtest`). Dev tools are pinned in the Makefile.
- **Tests** are table-driven where cases vary by input, call only the module's exported interface, and follow
  `testing.md`'s rules: paths from `hostfs.Under(t.TempDir())`, time from `op.ManualClock`, HTTP against
  `httptest`, Docker through `dockerfake`, unprivileged and offline.
- **Help text** follows `cli.md`'s verb table and `docs.md`'s help text rules. After any help-text change, run
  `make docs` and commit `docs/reference/` with it; the reference is generated, never edited by hand.
- New text never names the earlier tools that managed Actual servers; the only exception is
  `TestNoPredecessorNames`'s name list.

## Commands

- `make tools` once, then `make lint` and `make test` before every commit. Both run on Linux and macOS without
  root.
- `make docs-check` checks the reference without writing; CI runs it.
- `make journey` runs the journeys as root (on this machine via `sudo`, or on `HOST=<ssh host>` over ssh) and
  **wipes all Abyss state on the target**. Run `make image-cache` first. Run it only on a CI runner or a throwaway
  VM, never on a Host with real Instances. CI runs the journeys for pull requests to `main` and pushes to `main`.
- Releases are cut by the Maintainer, as `release.md` describes. An agent may run `make release-check` when
  asked, and never pushes a tag or publishes a Release.

## Code review

`/code-review` takes its standards from this file and `CONTRIBUTING.md`.

## Agent skills

### Issue tracker

Issues live in GitHub Issues for `DocCyblade/abyssctl`, through the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

The five default labels: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See
`docs/agents/triage-labels.md`.

### Domain docs

Single-context: `GLOSSARY.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.
