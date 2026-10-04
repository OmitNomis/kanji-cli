# Telegram front-end — state, plan, and deployment

A handoff document. Everything in **Where things stand** is built, deployed and
measured. Everything in **The plan** is not written yet.

The question that prompted this: **will Telegram work without a service?** No.
Telegram has exactly two delivery models, and both need a process alive between
your messages:

- **Long polling** — your process calls `getUpdates` and holds the connection
  open. No inbound ports, no TLS, no public address.
- **Webhooks** — Telegram POSTs to you, which needs a public HTTPS endpoint
  with a valid certificate.

The Atom sits behind Tailscale with no public address and no certificate, so
**long polling is the only practical option** — and that is the better one
anyway: nothing to expose, nothing to renew. But it does mean a daemon, which
is why the CLI never needed a systemd unit and the bot does.

---

## Where things stand

The CLI is installed and running on the Atom (`root@10.0.0.5`, Intel Atom
N280 @ 1.66 GHz, 988 MB DDR2, Debian 12, i686):

| | |
|---|---|
| `/usr/local/bin/kanji` | 6.2 MB static binary, ELF 32-bit |
| `/root/.config/cli-kanji/dict.db` | 44 MB, read-only, 2,136 jōyō kanji and 25,406 reviewable compounds |
| `/root/.config/cli-kanji/progress.db` | created on first run; **currently absent — clean slate** |

Deploy or update with `scripts/deploy.sh`, which is passwordless (a deploy key
is installed at `/root/.ssh/authorized_keys`) and skips the dictionary transfer
when it has not changed.

Measured on that machine: 76 ms startup, 8 MB peak RSS, 48 ms for
`kanji info 国境`.

### What the Atom gives us

Probed, not assumed:

| | |
|---|---|
| systemd | 252 (252.39-1~deb12u2) — unit files work, `/etc/systemd/system` exists |
| `api.telegram.org` | reachable, HTTP 302 |
| `curl` | present |
| `loginctl` linger | supported, though a system unit is simpler here |
| `sqlite3` CLI | **absent** — see [Debugging without sqlite3](#debugging-without-sqlite3) |

### Why the bot is a second front-end, not a rewrite

`internal/review` holds the queue and the grading and contains **no terminal
code at all**. That was deliberate, and it is what makes this cheap:

```go
sess, err := review.Begin(ctx, st, srs.Default(), cfg, time.Now())
card, ok := sess.Current()                       // what to ask
res, err := sess.Answer(ctx, given, elapsed, now) // grade, persist, advance
sess.Skip()
sess.Remaining(); sess.Total(); sess.Position(); sess.Accuracy()
```

`Answer` already does everything a front-end needs: it matches the answer
through `kana.MatchesAny`, grades it with the scheduler, writes the new state
and appends to the answer log — all before returning. A front-end only has to
decide how to *display* a card and how to *read* a line of text.

`internal/store` is equally reusable: `Lookup`, `CompoundsFor`, `Counts`,
`Activity` and `NextDue` are what `/info` and `/stats` would be built from.

**What is not reusable**, and is the one real refactor:

| | |
|---|---|
| `cmd/kanji/main.go:180` `app.loop` | the ask → grade → drill cycle |
| `cmd/kanji/main.go:273` `app.drill` | the retype-before-continuing requirement |
| `cmd/kanji/main.go:437` `command` | `?` / `:s` / `:i` / `:q` dispatch |

These are logic, not rendering, but they currently live in the CLI's `main`.
Both front-ends want the drill rule and the command vocabulary, so hoist them
into `internal/review` (say `review.Turn`, a small state machine with states
*asking* and *drilling*) rather than reimplementing them in the bot. A bug fixed
in one should not need fixing twice.

`internal/ui` is terminal-specific and the bot will not use it. Telegram needs
its own formatter — see [Formatting](#formatting-for-telegram).

---

## The plan

### Shape

```
cmd/kanji-bot/          long-polling daemon, one new main
internal/review/turn.go the hoisted ask/drill/command state machine
internal/telegram/      Bot API client and message formatting
```

Keep `internal/telegram` free of dependencies. The Bot API is plain JSON over
HTTPS and the two endpoints that matter — `getUpdates` and `sendMessage` — are
perhaps 150 lines with `net/http` and `encoding/json`. Adding a bot library
would pull a dependency tree onto a machine whose whole appeal is that it needs
none, and the CGO-free single-binary property is worth protecting.

### Session state

One `review.Session` per chat, held in memory, keyed by chat ID.

The important consequence: **a restart loses the in-memory queue but never
loses progress**, because `Answer` persists each answer as it is given. After a
restart the next message starts a fresh session, which re-reads what is due and
carries on. That is a perfectly acceptable failure mode, and it is worth
understanding before anyone is tempted to serialise the queue.

Expire idle sessions — an hour is reasonable — so a long-abandoned chat does not
pin a queue in memory on a 988 MB box.

### Sharing progress with the CLI

Both processes should use the **same** `progress.db`, so a review done on the
train and one done at the desk are the same queue. That is the whole point of
the feature.

This is safe as it stands, and not by accident:

- `progress.db` runs in **WAL** mode, so a reader never blocks a writer.
- `busy_timeout` is **5000 ms**, so when both processes do try to write at once
  the second waits rather than failing.
- Each process keeps a **single connection** (`SetMaxOpenConns(1)`), so it
  serialises its own access before SQLite has to.

One wrinkle to accept rather than fix: a card answered in Telegram stays in a
desk session's already-assembled in-memory queue, so it may be asked again
there. It will simply be graded again. Not worth solving.

If the bot runs as a different user from the CLI, point both at one file with
`CLI_KANJI_PROGRESS`, and make sure that user can write the **directory** —
WAL needs to create `-wal` and `-shm` beside the database.

### Access control — do this first, not last

**A bot token is a public endpoint.** Anyone who learns the bot's username can
message it, and without a check they would be drilling *your* deck and mutating
*your* progress.

Require an allowlist before any session is created:

```
TELEGRAM_ALLOWED_CHAT_IDS=123456789
```

Reject anything else with a flat refusal and no detail — do not confirm that
the bot exists, do not echo back what was sent. Empty or unset must mean *deny
everyone*, never *allow everyone*: the insecure default is the one that gets
shipped by accident.

Get your own chat ID by messaging the bot once and reading the rejected update
out of the log.

### Formatting for Telegram

Telegram is not a terminal, and the differences matter:

- **No ANSI.** `internal/ui`'s colours and box-drawing are meaningless. Use
  Telegram's MarkdownV2 or HTML, and remember MarkdownV2 requires escaping a
  long list of characters — glosses contain `;`, `(`, `)`, `-`, `.` and `!`
  constantly. HTML with three escapes (`&`, `<`, `>`) is far less error-prone.
- **A message is one unit.** The terminal reveals a card then waits; here the
  prompt, the hint and the instruction should arrive as a single message.
- **Monospace for the prompt.** `<code>写 真</code>` keeps the kanji from being
  reflowed and renders large enough to read on a phone.
- **No hint leakage.** Levels 0 and 1 show the reading (`srs.ShowHint`); above
  that the reading must not appear in the message at all, including in any
  footer or preview.
- **Keep the drill.** "Type it to carry on" is natural in a chat and preserves
  the one rule the whole program is built around: you produce the kana, or you
  do not.

Inline keyboards are tempting and should be **avoided for answers** — tapping a
button is multiple choice, which is the thing this program exists not to be.
They are fine for `/skip` and `/reveal`.

### Commands

The CLI's colon prefix exists because single letters collide with readings — い
is a reading of 五. Telegram's `/` prefix solves that already, so use it:

| | |
|---|---|
| `/review` | start or resume a session |
| `/reveal` | show the reading, graded as wrong |
| `/skip` | skip without grading |
| `/info 国境` | an entry and the compounds that use it |
| `/stats` | progress |
| `/stop` | end the session |

Anything not starting with `/` is an answer. `kana.Normalize` already handles
romaji, kana, and whatever an autocorrecting phone keyboard does to them.

---

## Deployment

### Today

```sh
scripts/deploy.sh
```

Builds for the target's architecture, sends the dictionary only if it changed,
installs the binary. Nothing runs in the background.

### When the bot lands

`scripts/deploy.sh` **already handles the new binary** and needs no editing.
It takes `EXTRA_BINARIES`, which names further `cmd/` packages to ship and
*skips any that do not exist in the tree* — so this command works right now
(warning that `cmd/kanji-bot` is absent) and starts shipping the bot the moment
the package appears:

```sh
EXTRA_BINARIES=kanji-bot scripts/deploy.sh
```

That property is the point: the deploy path does not change when the feature
arrives. What still has to be added, once there is a daemon to supervise:

**1. The token, out of the repository.** `/etc/cli-kanji/bot.env`, mode `0600`,
root-owned:

```
TELEGRAM_BOT_TOKEN=123456:ABC...
TELEGRAM_ALLOWED_CHAT_IDS=123456789
```

Never commit it, never pass it as a command-line argument — arguments are
visible in `ps` to every user on the box. systemd's `EnvironmentFile` reads it
without it ever appearing in a process listing.

**2. A unit file.** `/etc/systemd/system/kanji-bot.service`:

```ini
[Unit]
Description=cli-kanji Telegram bot
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/kanji-bot
EnvironmentFile=/etc/cli-kanji/bot.env
Restart=always
RestartSec=5

# The box has 988 MB and other work to do.
MemoryMax=96M
Nice=5

# It needs the network, one database directory, and nothing else.
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=/root/.config/cli-kanji

[Install]
WantedBy=multi-user.target
```

`ProtectHome=read-only` with an explicit `ReadWritePaths` is what keeps a
network-facing daemon from writing anywhere but its own database. Note it needs
widening if the progress file moves.

**3. Reload, enable, restart.** Idempotent, so a re-deploy is just a restart:

```sh
systemctl daemon-reload
systemctl enable --now kanji-bot
systemctl restart kanji-bot      # on every subsequent deploy
```

A deploy must restart the service: replacing the binary underneath a running
process changes nothing until it restarts. `deploy.sh` renames binaries into
place rather than writing over them, so a running bot is never half-replaced —
it keeps executing the old inode until restarted, which is correct and safe.

**4. Verify, including the negative case.** `systemctl is-active` says only that
the process survived five seconds. Also confirm it authenticated — `getMe`
against the Bot API — and that **an unknown chat ID is rejected**. An allowlist
nobody tested is an allowlist that does not work.

### Rollback

Keep it trivial: `systemctl stop kanji-bot` leaves a fully working CLI, because
the bot adds a binary and a unit and changes nothing the CLI depends on. The
dictionary and the progress file are untouched by any of this.

---

## Debugging without sqlite3

**The Atom has no `sqlite3` binary**, so inspecting state there means either
using the program's own views:

```sh
ssh root@10.0.0.5 kanji stats
ssh root@10.0.0.5 kanji info 国境
```

or copying the file off and reading it on a machine that has the tooling:

```sh
scp -i ~/.ssh/id_ed25519_atom root@10.0.0.5:/root/.config/cli-kanji/progress.db /tmp/
```

Copy `progress.db` only — the 44 MB dictionary is already in the repo's build
output. If the bot is running, stop it first or copy the `-wal` file too, or the
snapshot will be missing the most recent answers.

Bot logs go to the journal: `journalctl -u kanji-bot -f`. Keep the token out of
them — log that a rejected chat ID *occurred*, never the token, and prefer not
to log message text.

---

## Checklist

- [ ] Hoist `app.loop` / `app.drill` / `command` into `internal/review`
- [ ] `internal/telegram`: `getUpdates` and `sendMessage`, stdlib only
- [ ] `cmd/kanji-bot`: long-poll loop, one session per chat, idle expiry
- [ ] Chat-ID allowlist, denying by default, with a test for the deny path
- [ ] HTML formatting; no reading shown above level 1; no answer keyboards
- [ ] `/etc/cli-kanji/bot.env` at `0600`, token never in `ps` or logs
- [ ] `kanji-bot.service` with `MemoryMax` and `ReadWritePaths`
- [ ] Teach `deploy.sh` to install the unit and restart when the bot is shipped
- [ ] Verify: authenticates, answers a card, persists it, rejects a stranger
