# Spec: alerts

Build step 11 of 16. The [overview](README.md) has the module map, the call graph and the Host layout. Shared
packages (`internal/hostfs`, `internal/op`, `internal/buildinfo`) are in [foundation.md](foundation.md);
`abyssctl.env`, `ReadSettings` and `SetPushover` are in [host.md](host.md); the locks are in
[docker-and-locks.md](docker-and-locks.md). The Health check, [Problems](../../GLOSSARY.md), `health.json` and
the [MOTD](../../GLOSSARY.md) are in [health.md](health.md). Capitalised terms are defined in
[GLOSSARY.md](../../GLOSSARY.md).

## 1. Purpose

Build the Alerts module (`internal/alerts`) and the verbs `alerts setup` and `alerts test`. Alerts owns:

- **the [Alert](../../GLOSSARY.md) value**: what one message about a Problem starting or ending carries, and the
  rules on its content;
- **`Send(alert)`**: the one seam through which any message leaves the Host, so a second channel is one more
  Adapter behind the same interface;
- **the Pushover Adapter**: the request it makes to Pushover's message API, the priorities, the length limits,
  and how Pushover's answers become errors;
- **the test message** and the two verbs: `alerts setup` sets the Pushover pair after a test message gets through,
  and `alerts test` sends one with the pair already set.

Alerts decides nothing. The [Health check](../../GLOSSARY.md) is the only thing that sends Alerts: it decides
which Problems start and end, writes each Alert's title and text, and keeps track of which Alerts were delivered
and retries the rest ([health.md](health.md)). The MOTD is a view Health renders, not an Alert channel. The one
message that does not come from Health is the test message, which the CLI sends directly; it is not an Alert.

Pushover is optional. With no Pushover pair set, no channel exists, nothing is sent, and the MOTD still shows the
current Problems.

When this spec is done, `sudo abyssctl alerts setup --pushover-user <user> --pushover-token -` delivers a test
message to the Operator's Pushover devices and only then saves the pair, `sudo abyssctl alerts test` delivers
another, and Health has a `Sender` to build on. The Alerts part of journey 7 passes.

## 2. Interface

Signature sketches, not final code.

```go
package alerts

// Phase says whether the Problem started or ended.
type Phase string

const (
    Started Phase = "started"
    Ended   Phase = "ended"
)

// Alert is one message about one Problem. Health builds it (health.md owns the kinds and the wording);
// the CLI builds the test message with TestMessage.
type Alert struct {
    Kind    string    // the Problem kind, snake_case, e.g. "instance_down"; "test" for the test message,
                      // which uses this type but is not an Alert in the glossary's sense
    Subject string    // what the Problem is about: an Instance name, a Certificate id, an Orphan's name, or "host"
    Phase   Phase
    Title   string    // one line, no newline; never a Secret
    Text    string    // the body; may hold newlines; never a Secret
    Since   time.Time // when the Problem started
    At      time.Time // when the Health check saw this change (the start, or the end)
}

// Sender is the seam Health takes. The Pushover Adapter and alertsfake.Sender implement it.
type Sender interface {
    // Send delivers a to every channel, once. It makes one attempt and never retries; the caller decides
    // what a failure means. It never prompts and never prints.
    Send(ctx context.Context, a Alert) error
}

// Config is what the CLI passes from host.Settings and buildinfo.
type Config struct {
    PushoverUser  string       // host.Settings.PushoverUser; a Secret
    PushoverToken string       // host.Settings.PushoverToken; a Secret
    PushoverURL   string       // buildinfo.PushoverURL, e.g. "https://api.pushover.net"
    HTTP          *http.Client // nil: the production client (Go's default transport, 10 s per request)
}

// New returns the Sender for the channels Config sets. In v1 that is the Pushover Adapter.
// ErrNotConfigured when neither Pushover key is set; a Refused error when only one is set.
// It does not check the keys' form: host.ValidPushoverKey is the one rule (host.md), and
// ReadSettings and the CLI's flags already apply it.
func New(c Config) (Sender, error)

// TestMessage is the message alerts setup and alerts test send. at is the CLI's Clock, loc the Host's
// time zone (time.Local in production), baseDomain from host.Settings.
func TestMessage(at time.Time, loc *time.Location, baseDomain string) Alert

var (
    // Matches op.ErrRefused, hint "abyssctl alerts setup".
    ErrNotConfigured = errors.New("no Alert channel is set up")
    // Pushover answered that the request is wrong: a bad user or token, a disabled application,
    // or a message it won't take. Wrapped with Pushover's own reasons, Secrets removed.
    ErrRejected = errors.New("Pushover rejected the message")
    // Pushover answered 429: the Pushover account's monthly message limit is used up. It resets on the 1st.
    ErrLimited = errors.New("Pushover's monthly message limit for this Pushover account is used up")
    // Send got an Alert that breaks the rules below. A bug in the caller; no request is made.
    ErrInvalid = errors.New("invalid Alert")
)
```

Test helpers, imported only by tests and the journey harness:

- `internal/alerts/alertsfake`: `New() *Sender` records every `Alert` it is given (`Sent()`), and
  `FailNext(err)` / `FailAll(err)` make `Send` return that error, for Health's module tests.
- `internal/alerts/pushovertest`: a fake of the part of Pushover's message API the Adapter uses.
  - `New(t testing.TB) *Server` starts it on a random local port, for module and CLI tests; `URL()` goes in
    `Config.PushoverURL`.
  - `Listen(t testing.TB, addr string) *Server` starts it on a fixed address. The journey harness calls
    `Listen(t, pushovertest.JourneyAddr)`, with `const JourneyAddr = "127.0.0.1:18089"`, because the journey build
    stamps `buildinfo.PushoverURL=http://127.0.0.1:18089` at build time, before any server runs
    ([testing.md](testing.md) owns the stamp).
  - `Messages()`: every request's form fields, in order.
  - `RejectUser(user)`: answer that user with Pushover's 400 `{"status":0,"errors":["user identifier is invalid"]}`.
  - `ReplyNext(status int, body string)`: answer the next request with exactly this status and body (a 3xx, a 429,
    a 5xx, a non-JSON body, a body over 64 KiB, or a 200 with `"status": 0`).
  - `EchoCredentials()`: from now on, put the request's user and token in every error answer, as a careless
    server might.
  - `Delay(d)`: wait `d` before each answer, for timeouts.

### Invariants and error modes

- **No Secret leaves in a message.** An Alert carries only Instance names, Problem kinds and descriptions, times,
  Hostnames, Certificate ids and the Host's free disk space as a percentage. It never carries a Pushover
  credential, a private key, a file path (the Data root is named as "the Data root", not by its path) or anything
  from inside an Instance's `data/`. Health is held to this by its own tests; Alerts cannot check free text.
- **The credentials go only in the request body.** The user and token are sent as form fields of a `POST` to
  `PushoverURL` (HTTPS in the release build), never in the URL, a header, an event, an error or a log line. An error that would hold either value
  (Pushover echoing one back, for example) has each occurrence replaced by `op.Mask(value)`.
- **One request per `Send`, no retry.** A failure is returned at once. Health keeps the Alert unsent and tries it
  again on its next run ([health.md](health.md)).
- **`Send` checks the Alert first.** An empty `Kind`, `Subject`, `Title` or `Text`, a `Kind` that is not
  snake_case, a `Phase` other than `Started` or `Ended`, a `Title` with a newline, or a zero `At` returns
  `ErrInvalid` with no request.
- **Error classes.** `ErrNotConfigured` and a lone key from `New` are `op.Refused` (exit 2). `ErrRejected`,
  `ErrLimited` and every other `Send` failure (no network, a timeout, a redirect, Pushover's 5xx, a malformed
  answer) are plain errors;
  what exit code they become is the caller's (below, and [health.md](health.md)).
- **Alerts is a leaf.** It calls no other module and reads no file: the CLI passes the settings in.

## 3. Behaviour

### The Pushover Adapter

`Send(ctx, a)`:

1. Check the Alert (above).
2. Build the form, in this order: `token`, `user`, `title` (`a.Title`), `message` (`a.Text`), `priority`
   (`0` for `Started`, `-1` for `Ended`) and `timestamp` (`a.At` as Unix seconds, so a message retried on a later
   run still shows when the change was seen). Nothing else is sent: no `html`, `url`, `sound` or `device`, so
   devices use their defaults and the text is plain.
3. Pushover's limits are 250 characters for a title and 1024 for a message. A longer `Title` or `Text` is cut to
   one character under the limit, at a rune boundary, and ends with `…`, so a long text never fails a send.
4. `POST <PushoverURL>/1/messages.json` with `Content-Type: application/x-www-form-urlencoded`, under `ctx` and
   the client's 10 s limit. The Adapter follows no redirect: its client's `CheckRedirect` returns an error (on its
   own copy of a client passed in `Config.HTTP`). The answer's body is read up to 64 KiB; more is an error.
5. The answer:
   - **200** with JSON `"status": 1`: delivered; `Send` returns nil.
   - **200** with `"status"` other than 1, or **4xx other than 429** with a JSON body: `ErrRejected`, wrapped with
     Pushover's `errors` strings joined by "; " ("Pushover rejected the message: user identifier is invalid").
   - **4xx other than 429** with a body that is not JSON: `ErrRejected`, wrapped with "HTTP <code>".
   - **429**: `ErrLimited`: "Pushover's monthly message limit for this Pushover account is used up; it resets on
     the 1st".
   - **3xx**, **5xx**, no answer, a timeout, or a 200 whose body is not JSON: a plain error naming the status or
     the cause.

   In every error, each occurrence of the user or the token is replaced by `op.Mask(value)`.

Priority `0` is Pushover's normal priority: outside the Operator's Pushover quiet hours, a started Problem makes a
sound and shows. Priority `-1` is quiet: a recovery shows without a sound. No message uses emergency priority
(`2`), which repeats until acknowledged.

### The test message

`TestMessage(at, loc, baseDomain)` returns an Alert with `Kind` `test`, `Subject` `host`, `Phase` `Started`,
`Since` and `At` both `at`, and:

```
Title: Abyss test message
Text:  Test message from Abyss on budget.example.com, sent 2026-11-18 14:05 EST.
       Alerts from this Host arrive like this.
```

The time is `at` in `loc`, in the form `2006-01-02 15:04 MST` (Host local time with the zone abbreviation). The
message names the [Base domain](../../GLOSSARY.md) so an Operator with Pushover on more than one machine can tell
where it came from. It carries priority `0`, like a started Problem, so outside the Operator's Pushover quiet
hours the Operator hears what an Alert sounds like.

### What the CLI does

The CLI builds one `Config` per run from `host.ReadSettings` and `buildinfo.PushoverURL`, with nil `HTTP`.

| Verb | Locks | Prompt | `--dry-run` | `--json` | Exit |
|---|---|---|---|---|---|
| `alerts setup --pushover-user U [--pushover-token -]` | Host, only to save | the token, hidden, on a TTY | no | no | 0, 1, 2 |
| `alerts test` | none | none | no | no | 0, 1, 2 |

Neither verb shows a summary panel or asks y/N, so neither takes `--dry-run` or `--yes`. Both write the activity
log ([cli.md](cli.md)). Its header records the command line, and the `--pushover-user` value in it is a Secret:
cli.md masks it to `…abcd` there, for `alerts setup` and `host setup` alike. The token never appears on the command
line (only `-`), and every event and log line shows the user only as `…abcd`.

**`alerts setup`.** It takes the same two Pushover flags as `host setup` ([host.md](host.md)) and nothing else.

1. `--pushover-user` is required; without it the run is refused (exit 2). `--pushover-token` takes only `-`; any
   other value is refused (exit 2) without being echoed, so a token never sits in shell history or the activity
   log's command line.
2. `host.ReadSettings`, before any prompt: a Host that is not set up is refused (exit 2, hint
   `abyssctl host setup`); a broken `abyssctl.env` exits 4 with its `SettingsError`. Warnings are shown.
3. The token: with `--pushover-token -` the CLI reads one line from stdin and strips its trailing newline. With no
   `--pushover-token` on a TTY, it prompts for the token with echo off; with no TTY it refuses (exit 2).
4. Both values must pass `host.ValidPushoverKey` ([host.md](host.md)), or the run is refused (exit 2), naming
   `--pushover-user` or the token, never the value.
5. `New` with the **new** pair, then `Send(TestMessage(…))`, before any lock:
   - `ErrRejected` → refused (exit 2), "Pushover rejected these credentials: <Pushover's reason>", and
     `abyssctl.env` is not touched;
   - any other failure → exit 1, `abyssctl.env` not touched, hint: check the Host's outbound HTTPS to
     `api.pushover.net`, or save the pair without a test with `abyssctl host setup --pushover-user … --pushover-token -`.

   Event: `sent what=test channel=pushover user=…abcd`.
6. Take the Host lock (30 s), then `host.SetPushover(user, token)`, which rewrites `abyssctl.env` with only the
   two Pushover keys changed, or writes nothing when they are the same. A lock timeout or a failed `SetPushover`
   is exit 1 with nothing written, and the message says so plainly: "The test message was sent, but the Pushover
   pair was not saved: <cause>". The event `saved file=/etc/abyssctl/abyssctl.env keys=PUSHOVER_USER,PUSHOVER_TOKEN`
   is emitted only when the new pair differs from the one `ReadSettings` returned in step 2.
7. Outcome line: "✓ Pushover is set (user …abcd); a test message was sent", or, when the pair was already set,
   "✓ Pushover was already set (user …abcd); a test message was sent".

The next Health check uses the new pair, including for any Alerts it still holds unsent.

**`alerts test`.**

1. `host.ReadSettings` as above.
2. `New(Config)`: no pair set → refused (exit 2), "Pushover is not set up", hint `abyssctl alerts setup`.
3. `Send(TestMessage(…))`. Success: exit 0, "✓ Test message sent to Pushover (user …abcd)". `ErrRejected`: exit 1
   with Pushover's reason and hint `abyssctl alerts setup`. Any other failure: exit 1 naming it.

`alerts test` takes no lock, writes nothing under the Data root and does not touch `health.json`: a delivered
test message does not mark any Alert as delivered.

**Removing Pushover.** There is no verb for it in v1. The Operator deletes the `PUSHOVER_USER` and
`PUSHOVER_TOKEN` lines from `/etc/abyssctl/abyssctl.env` by hand; the next Health check then has no channel. The
alerts guide says so (Docs, below).

### What Health does with Alerts

Stated here so the seam is complete; [health.md](health.md) owns the detail. The CLI calls `New` for every
`health check` run and passes the `Sender` to Health, or no `Sender` on `ErrNotConfigured`. Health builds each
Alert, calls `Send` at most once per Alert per run, and records which were delivered in `health.json`. At the first
failed `Send` it stops sending for the rest of that run and keeps the rest unsent, so a broken channel costs at
most one failing request per interval (Pushover may block an address that keeps sending bad requests). While any
Alert is unsent the MOTD shows "Alerts not delivered since …: <reason>", the reason being the last `Send` error.

## 4. Files and formats it owns

No file on the Host. `abyssctl.env` and its Pushover keys are [host.md](host.md)'s; `buildinfo.PushoverURL` is
[foundation.md](foundation.md)'s. Alerts owns:

- **The `Alert` value** and its rules (above).
- **The Pushover request**: the endpoint, the form fields and their order, the priorities, the length limits and
  truncation, and how each answer maps to an error.
- **The test message**'s title and text.
- **The `alerts setup` and `alerts test` outcome lines**, in [cli.md](cli.md)'s style.

### Package layout

```
internal/alerts/
  alerts.go       Alert, Phase, Sender, Config, New, TestMessage, the sentinels
  pushover.go     the Pushover Adapter
  alertsfake/     the Sender fake
  pushovertest/   the httptest Pushover API
internal/cli/
  alerts.go       alerts setup, alerts test
```

## 5. Depends on

- [foundation](foundation.md): `buildinfo.PushoverURL`; `op.Refused`, `op.Mask`, `op.Emitter` and `op.Clock`
  (the last two in the CLI only).
- [host](host.md): `ReadSettings` (`PushoverUser`, `PushoverToken`, `BaseDomain`), `ValidPushoverKey` and
  `SetPushover`, called by the CLI.
- [docker-and-locks](docker-and-locks.md): the CLI's `locks.Locker`, for the Host lock in `alerts setup`.
- [cli.md](cli.md): hidden input and TTY detection (`golang.org/x/term`), the outcome line and the activity log.

[Health](health.md) builds on the `Sender` and `alertsfake`; [testing.md](testing.md) runs `pushovertest` in
journey 7 on `pushovertest.JourneyAddr` and stamps `PushoverURL` at that address in the journey build.

Import rule this spec adds to `internal/archtest`: `internal/alerts` imports only the standard library and
`internal/op`. Only `internal/cli` and `internal/health` import it; `internal/alerts/alertsfake` and
`internal/alerts/pushovertest` are imported only by `_test.go` files and the journey harness (`journey/...`).

## 6. Decisions

- **Health is the only Alert sender** and the MOTD is a view, not a channel. Backup and Upgrade record outcomes;
  Health turns them into Problems and Alerts, so there is one place that decides and one state to show
  ([health.md](health.md)). The test message is the one exception, sent by the CLI, and it is not an Alert.
- **A narrow `Send(alert) error` seam.** An Alert is plain data and an Adapter only delivers it, so the Email
  channel on the roadmap is a second Adapter and Health does not change. `Send` makes one attempt and never
  retries, because Health already runs every few minutes and keeps the unsent ones.
- **Pushover with two priorities**: normal for a start, quiet for a recovery, never emergency. Choosing the
  priority per Problem kind is on the roadmap.
- **`alerts setup` sends a test before it saves**, so a mistyped key is never saved and the Operator knows the
  devices get messages. `host setup` saves the pair without a test, so a Host without outbound access can still be
  set up.
- **The token never goes on the command line.** It comes from stdin (`-`) or a hidden prompt, like `host setup`,
  so it stays out of shell history and the activity log. The user key stays a flag: on its own it cannot send a
  message, and the activity log masks it.
- **One key-format rule**, `host.ValidPushoverKey`, because Host owns the `abyssctl.env` key table. Alerts only
  checks that both keys or neither are set.
- **The Pushover base URL is compiled in** with no runtime override ([foundation.md](foundation.md)); the
  journey build stamps it at a fixed local address where the harness runs `pushovertest`.
- **No redirects.** A redirect would carry the form somewhere the build did not name, so the Adapter treats one as
  an error.
- **Messages pass through Pushover's servers**, a third party, so they carry only names, Problems, times and
  Hostnames.
- **Alerts use Host local time** with the zone abbreviation, like every time a person reads.
- No new dependency ([ADR 0003](../adr/0003-single-static-go-binary.md)): `net/http`, `net/url` and
  `encoding/json` are enough.

## 7. Later

- An Email Alert channel, as a second Adapter behind `Send` (ROADMAP Next).
- Per-kind Pushover priority (ROADMAP Next).
- A webhook Alert channel.

## 8. Docs it changes

- README: **Requirements**: outbound HTTPS to `api.pushover.net` when Pushover is used. **Quickstart**: an
  optional `sudo abyssctl alerts setup --pushover-user … --pushover-token -` line.
- `docs/guides/alerts.md` (the Pushover part; [host.md](host.md) covers the other settings and
  [health.md](health.md) what each Alert means and the MOTD): getting a Pushover user key and creating an
  application for its token; `alerts setup`, which sends a test first and saves only if it gets through; the
  token from stdin or the hidden prompt; `alerts test`; that `host setup --pushover-user … --pushover-token -`
  saves the pair without a test; that a start is a normal notification and a recovery a quiet one; that a failed
  delivery is tried again at the next Health check; changing the pair with `alerts setup`; removing Pushover by
  deleting its two lines from `abyssctl.env`; and that without Pushover no Alert is sent and the MOTD still shows
  the Problems.
- `docs/guides/security.md`: Alerts go through Pushover's servers and carry only Instance names, Problems, times
  and Hostnames; the token is read from stdin or a hidden prompt, never a command-line value; the user key is a
  command-line value, so it is visible in shell history and `ps` while the command runs, but on its own it cannot
  send a message, and the activity log shows it only as `…abcd`.
- Reference: the `alerts setup` and `alerts test` pages, from their help text.

## 9. Acceptance list

Module tests are in `internal/alerts`, with `pushovertest`, without root or network. CLI tests are in
`internal/cli`, with `pushovertest`, a temp `abyssctl.env` through `hostfs.Under`, real locks on a temp Data root,
an `op.ManualClock` and a scripted TTY. This spec's journey slice is journey 7's `alerts` steps
([testing.md](testing.md)).

1. `Send` of a started Alert posts to `/1/messages.json` the form fields `token`, `user`, `title`, `message`,
   `priority=0` and `timestamp` (`At` in Unix seconds) and nothing else; an ended Alert sends `priority=-1`. →
   `TestPushover_Send` (table)
2. A title over 250 characters and a message over 1024 are cut at a rune boundary to the limit, ending in `…`;
   shorter ones are sent unchanged. → `TestPushover_Truncates` (table, with multi-byte text)
3. An Alert with an empty or non-snake_case `Kind`, an empty `Subject`, `Title` or `Text`, a newline in `Title`,
   an unknown `Phase` or a zero `At` returns `ErrInvalid` and makes no request. → `TestSend_RejectsInvalid`
   (table)
4. Through `ReplyNext`: a 400 with `"status": 0` and a 200 with `"status": 0` give `ErrRejected` carrying the
   `errors` strings; a 403 with a non-JSON body gives `ErrRejected` with "HTTP 403"; a 429 gives `ErrLimited` with
   the monthly-limit text; a 302, a 500, a timeout (`Delay`), a 200 with a non-JSON body and a body over 64 KiB each
   give a plain error; `Send` makes exactly one request in every case. → `TestPushover_Errors` (table)
5. The user and token appear only in the request body: never in the URL or a header; a 3xx is not followed (the
   redirect target gets no request); and with `EchoCredentials` no error text holds either one in full. →
   `TestPushover_SecretsOnlyInBody` (table)
6. `New` gives `ErrNotConfigured` (exit 2, hint `abyssctl alerts setup`) with neither key, and a refusal (exit 2)
   with one key alone. → `TestNew` (table)
7. `TestMessage` gives the fixed title, the text with the Base domain and the time in the given zone with its
   abbreviation, `Kind` `test`, `Subject` `host` and `Phase` `Started`. → `TestTestMessage` (golden, two zones)
8. `alertsfake.Sender` records Alerts in order and returns the scripted failures. → `TestFake`
9. `alerts setup` refuses (exit 2), with no request and `abyssctl.env` unchanged: no `--pushover-user`, a
   `--pushover-token` other than `-`, no token and no TTY, a value failing `host.ValidPushoverKey` (named by flag,
   not value), and a Host that is not set up (refused before any token prompt). →
   `TestAlertsSetupCmd_Refusals` (table) in `internal/cli`
10. `alerts setup` reads the token from stdin with `-` and from a hidden prompt on a TTY, and saves through
    `SetPushover` after the test message gets through. With the test holding the Host lock, the test message is
    still sent, the save times out, the run exits 1 saying the test was sent but the pair not saved, and
    `abyssctl.env` is unchanged. A rejected pair is exit 2 and any other send failure exit 1, each with
    `abyssctl.env` unchanged. A failing `SetPushover` after a sent test is exit 1 with the same message. Saving
    the pair already set leaves `abyssctl.env`'s bytes and mtime unchanged, emits no `saved` event and prints the
    "already set" outcome. → `TestAlertsSetupCmd` (table) in `internal/cli`
11. `alerts test` with no pair is exit 2 with hint `abyssctl alerts setup`; it sends one test message with the
    pair in `abyssctl.env`; a rejection is exit 1 with the same hint; it takes no lock and leaves the Data root
    unchanged. → `TestAlertsTestCmd` (table) in `internal/cli`
12. Neither verb's output, events or activity log lines, including the log's command-line header, hold the user
    or token in full, only `…abcd`. → `TestAlertsCmd_NoSecrets` in `internal/cli`
13. `internal/alerts` imports only the standard library and `internal/op`; only `internal/cli` and
    `internal/health` import it; `alertsfake` and `pushovertest` are imported only by `_test.go` files and
    `journey/...`. → `TestImportRules` in `internal/archtest`
14. Journey 7, with the binary's `PushoverURL` stamped at `http://127.0.0.1:18089` and the harness's `pushovertest`
    listening on `JourneyAddr`: `alerts test` before `alerts setup` (Host already set up) exits 2; `alerts setup` with a rejected user exits 2 and leaves `abyssctl.env` without Pushover keys;
    `alerts setup --pushover-user … --pushover-token -` with the token on stdin exits 0, the server gets one test
    message at priority 0, and `abyssctl.env` holds both keys at root:root 0600; `alerts test` then delivers a
    second; the token appears nowhere in the activity log. → journey 7
15. Journey 7: the Instance-down Alert from the Health check reaches the `pushovertest` server at priority 0, and
    its recovery at priority -1. → journey 7 (checked by health.md's journey slice)
16. On the Release check's Debian 13 Host, the release binary's `alerts setup` with the Operator's real Pushover
    pair exits 0, meaning Pushover answered `"status": 1`. That is the scripted check; the message arriving on the
    Operator's device is an optional check by eye. How the real pair reaches the test VM without entering the repo
    is release.md's. → the Release check (checked by release.md's Release check)
17. Docs: the README lines, the Pushover part of `alerts.md`, `security.md` and the `alerts setup` and
    `alerts test` reference pages say what [Docs it changes](#8-docs-it-changes) lists. → (review); the reference
    pages also by the generated-reference check in CI
