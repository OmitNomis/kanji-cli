# Telegram front-end — how it works, and how it is deployed

This was a plan; it is now built. What follows is what exists, why it is shaped
that way, and what to do when it misbehaves. The CLI is unaffected by all of
it: stop the service and you have exactly the program you had before.

The question that prompted the design: **will Telegram work without a service?**
No. Telegram has exactly two delivery models, and both need a process alive
between your messages:

- **Long polling** — the process calls `getUpdates` and holds the connection
  open. No inbound ports, no TLS, no public address.
- **Webhooks** — Telegram POSTs to you, which needs a public HTTPS endpoint
  with a valid certificate.

The Atom sits behind Tailscale with no public address and no certificate, so
**long polling was the only practical option** — and the better one anyway:
nothing to expose, nothing to renew. But it does mean a daemon, which is why
the CLI never needed a systemd unit and the bot does.

---

## What is on the Atom

`root@10.0.0.5`, Intel Atom N280 @ 1.66 GHz, 988 MB DDR2, Debian 12, i686:

| | |
|---|---|
| `/usr/local/bin/kanji` | the review CLI, 6.2 MB static ELF 32-bit |
| `/usr/local/bin/kanji-bot` | the Telegram daemon, same build settings |
| `/root/.config/cli-kanji/dict.db` | 44 MB, read-only, 2,136 jōyō kanji and 25,406 reviewable compounds |
| `/root/.config/cli-kanji/progress.db` | created on first run, shared by both binaries |
| `/etc/cli-kanji/bot.env` | token and allowlist, mode `0600`, root-owned |
| `/etc/systemd/system/kanji-bot.service` | the unit, from `scripts/kanji-bot.service` |

Deploy or update with `WITH_BOT=1 scripts/deploy.sh`, which is passwordless (a
deploy key is installed at `/root/.ssh/authorized_keys`) and skips the
dictionary transfer when it has not changed.

Measured on that machine before the bot existed, and still true of the CLI:
76 ms startup, 8 MB peak RSS, 48 ms for `kanji info 国境`.

### What the Atom gives us

Probed, not assumed:

| | |
|---|---|
| systemd | 252 (252.39-1~deb12u2) — unit files work, `/etc/systemd/system` exists |
| `api.telegram.org` | reachable, HTTP 302 |
| `curl` | present |
| `loginctl` linger | supported, though a system unit is simpler here |
| `sqlite3` CLI | **absent** — see [Debugging without sqlite3](#debugging-without-sqlite3) |

---

## How it is put together

```
cmd/kanji-bot/          the long-polling daemon, one session per chat
internal/review/turn.go the ask/drill/command state machine, shared
internal/telegram/      Bot API client and HTML formatting
scripts/kanji-bot.service the unit, with @BIN_DIR@ and @DATA_DIR@ substituted
```

### The bot is a second front-end, not a second program

`internal/review` already held the queue and the grading with **no terminal
code at all**, which is what made this cheap:

```go
sess, err := review.Begin(ctx, st, srs.Default(), cfg, time.Now())
turn := review.NewTurn(sess, time.Now())
out, err := turn.Submit(ctx, review.ParseCommand(line, review.SlashPrefix), time.Now())
```

What used to live in the CLI's `main` — the ask → grade → drill cycle, the
retype-before-continuing rule, and the command dispatch — is now
`review.Turn` and `review.ParseCommand`. `Turn` has two working phases,
*asking* and *drilling*, and reports an `Outcome` saying what a line did;
a front-end decides only how to draw that. `cmd/kanji` lost a hundred lines to
the move and behaves identically.

The one thing the two front-ends do not share is the vocabulary's prefix.
The CLI needs `:` because single letters collide with readings — い is a
reading of 五 — and Telegram clients already treat `/` as the command marker,
with a menu and completion built around it. `ParseCommand` takes the prefix,
so that is the whole of the difference; `/skip@kanji_bot` is also understood,
because that is how Telegram addresses one bot among several in a group.

### Why there is no bot library

`internal/telegram` is stdlib only. `getUpdates`, `sendMessage` and `getMe`
are perhaps 200 lines with `net/http` and `encoding/json`; a library would pull
a dependency tree onto a machine whose whole appeal is that it needs none, and
the CGO-free single-binary property is worth protecting.

Two details in there that are not obvious:

- **The token is scrubbed from every error.** `net/http` puts the request URL
  in every error it returns, and the token is in the path, so an unscrubbed
  poll failure would write the credential straight to the journal. The scrubbed
  error keeps its `Unwrap`, so `errors.Is(err, context.Canceled)` still works
  and the poll loop can still tell a shutdown from a network failure.
- **Messages are split on line boundaries.** The API limit is 4096 characters
  and counts UTF-16 code units; splitting at 3000 runes on a newline keeps
  every HTML tag whole, which matters because a half-open `<b>` is rejected
  outright.

### Session state

One `review.Session` per chat, held in memory, keyed by chat ID, driven by one
`review.Turn`.

Updates are handled **one at a time on the poll goroutine**. The store holds a
single SQLite connection by design and there is exactly one reviewer, so a
sequential loop needs no locking — worth more on a 1.66 GHz Atom than any
concurrency would be.

A restart loses the in-memory queue but **never loses progress**, because
`Session.Answer` persists each answer as it is given. After a restart the next
message starts a fresh session, which re-reads what is due and carries on. That
is a perfectly acceptable failure mode, and worth understanding before anyone
is tempted to serialise the queue.

Idle sessions are dropped after an hour (`-idle`), so a long-abandoned chat
does not pin a queue in memory on a 988 MB box. Updates sent more than two
minutes before the process started are ignored: Telegram holds undelivered
updates for about a day, and grading cards against readings typed yesterday is
worse than ignoring them.

### Sharing progress with the CLI

Both processes use the **same** `progress.db`, so a review done on the train
and one done at the desk are the same queue. That is the whole point.

This is safe as it stands, and not by accident:

- `progress.db` runs in **WAL** mode, so a reader never blocks a writer.
- `busy_timeout` is **5000 ms**, so when both processes do try to write at once
  the second waits rather than failing.
- Each process keeps a **single connection** (`SetMaxOpenConns(1)`), so it
  serialises its own access before SQLite has to.

One wrinkle to accept rather than fix: a card answered in Telegram stays in a
desk session's already-assembled in-memory queue, so it may be asked again
there. It will simply be graded again. Not worth solving.

If the bot ever runs as a different user from the CLI, point both at one file
with `CLI_KANJI_PROGRESS`, and make sure that user can write the **directory** —
WAL needs to create `-wal` and `-shm` beside the database. The unit's
`ReadWritePaths` has to widen to match.

### Access control

**A bot token is a public endpoint.** Anyone who learns the bot's username can
message it, and without a check they would be drilling your deck and mutating
your progress. So:

```
TELEGRAM_ALLOWED_CHAT_IDS=123456789
```

is consulted before a session can exist. The refusal is flat — `Not available.`
— and says nothing about what this is or what was sent. The rejected chat ID is
logged, because that is how you find your own.

The two states are deliberately different:

| | |
|---|---|
| variable **unset** | a configuration mistake: the bot refuses to start and says so |
| variable **set and empty** | "not filled in yet": the bot runs and denies *everyone*, logging each refused ID |

Never *allow everyone*: the insecure default is the one that gets shipped by
accident. `cmd/kanji-bot/allowlist_test.go` tests the deny path, including the
nil list, because an allowlist nobody tested is an allowlist that does not
work.

### Formatting

Telegram is not a terminal, and the differences drove the formatter:

- **No ANSI.** `internal/ui`'s colours and box-drawing are meaningless here, so
  messages are HTML. HTML rather than MarkdownV2 because MarkdownV2 needs a
  long list of punctuation escaped and glosses are full of `;`, `(`, `)`, `-`,
  `.` and `!`; HTML needs three escapes and is far harder to get subtly wrong.
  What *is* borrowed from `internal/ui` is prose rather than drawing —
  `Duration`, `Spaced`, `Count` — because a second copy of those would be a
  second place to fix.
- **A message is one unit.** The terminal reveals a card and then waits; here
  the feedback and whatever is being asked next go out as a single message
  rather than as two notifications. That is what `telegram.Join` is for.
- **Monospace for the prompt.** `<code>写 真</code>` keeps the characters from
  being reflowed and renders them big enough to read on a phone.
- **No hint leakage.** Levels 0 and 1 show the reading (`srs.ShowHint`); above
  that the reading is not in the message at all, and a test walks every level
  to prove it.
- **The drill is kept.** "Type it to carry on" reads naturally in a chat and
  preserves the one rule the whole program is built around.

**No answer keyboards.** Tapping a button is multiple choice, which is the
thing this program exists not to be. Inline keyboards would be fine for
`/skip` and `/reveal`; they are not used for answers.

### Commands

| | |
|---|---|
| `/review` | start or resume a session |
| `/cram` | ignore due times and work through the queue now |
| `/reveal` | show the reading, graded as wrong |
| `/skip` | skip without grading |
| `/info 国境` | an entry and the compounds that use it |
| `/stats` | progress, with or without a session |
| `/stop` | end the session |
| `/help` | all of the above |

Anything not starting with `/` is an answer. `kana.Normalize` already handles
romaji, kana, and whatever an autocorrecting phone keyboard does to them.

`/review` on an open session re-asks the current card rather than rebuilding
the queue, so it doubles as "what was I on?" after a while away.

### Cram, which arrived with the bot

`/cram`, and `kanji -cram` in the terminal, skip the clock: everything in the
queue is asked whether or not it has come round, and the new-item budget
applies to the session rather than to the day. It is for the state the queue is
in before any spaced repetition is useful — someone who already reads much of
the list and wants it *in* the scheduler rather than being drip-fed five
characters a day.

Nothing else changes. Answers are graded, persisted and given real due times
exactly as otherwise, so repeated cram passes leave a queue that ordinary
review can take over from. It is the waiting that is skipped, not the ladder.

In the store this is `Scheduled` beside `Due`: the same projection and the same
ordering, without the `due_at <= now` filter.

---

## Deployment

```sh
WITH_BOT=1 scripts/deploy.sh
```

`EXTRA_BINARIES=kanji-bot scripts/deploy.sh` still works and does the same
thing; `WITH_BOT=1` is the same request said more plainly, and is also what
installs the unit. A name asked for twice is only built once.

The deploy, in order: build for the target's architecture, send the dictionary
only if it changed, install the binaries, then — only when `kanji-bot` is among
them, and only when the target says `uid=0`:

1. **The token file.** `/etc/cli-kanji/bot.env`, mode `0600`, root-owned,
   created with placeholders if it does not exist and **never touched again**.
   The token is never committed and never passed as an argument: arguments are
   visible in `ps` to every user on the box, and `EnvironmentFile` is not.
2. **The unit.** `scripts/kanji-bot.service` with `@BIN_DIR@` and `@DATA_DIR@`
   substituted for the paths this deploy actually used, so `ReadWritePaths`
   matches where the databases really are. `ProtectHome=read-only` with an
   explicit `ReadWritePaths` is what keeps a network-facing daemon from writing
   anywhere but its own database; `MemoryMax=96M` means a leak is a restart
   rather than an OOM on the whole machine.
3. **Reload, enable, restart.** Idempotent, so a re-deploy is just a restart.
   A deploy *must* restart: `deploy.sh` renames binaries into place rather than
   writing over them, so a running bot keeps executing the old inode until it
   is restarted — which is correct and safe, and also means nothing changes
   until the restart happens.

   If the token is still the placeholder, the service is installed but **not
   started**, because starting it would just crash-loop. The script says what
   to fill in and how.
4. **Verify.** `systemctl is-active` only says the process survived five
   seconds, which a bot with a rejected token does not: it logs the refusal and
   exits. So the real check is that it got as far as logging
   `authenticated as @…`, which the script greps out of the journal. It also
   warns when the allowlist is still empty, since the symptom otherwise is a
   bot that answers nothing.

The one thing that cannot be automated is the negative case: message the bot
from another account and confirm it answers nothing but a flat refusal. The
deny path is unit-tested, but the deployed allowlist is yours to check.

### Rollback

`systemctl stop kanji-bot` leaves a fully working CLI, because the bot adds a
binary and a unit and changes nothing the CLI depends on. The dictionary and
the progress file are untouched by any of this.

### Building on Windows

Microsoft Defender flags the *Linux* `kanji-bot` binary as
`Trojan:Linux/Kaiji.A!MTB` and deletes it out of Go's build directory, so the
cross-build fails with "the file contains a virus or potentially unwanted
software". Kaiji is a Go-written Linux botnet, and the heuristic hits a static
Go ELF that speaks HTTP; `kanji` itself is not flagged. Either exclude the
build from Defender or build the bot somewhere else — it is a false positive,
not a signal.

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

Bot logs go to the journal:

```sh
journalctl -u kanji-bot -f
journalctl -u kanji-bot | grep 'rejected chat'     # someone else, or you before the allowlist
```

The log never contains the token and never contains message text. What it does
record: the authenticated username at startup, refused chat IDs, poll failures,
and expired sessions.

---

## What was built

- [x] Hoist `app.loop` / `app.drill` / `command` into `internal/review`
      (`review.Turn`, `review.ParseCommand`, `review.ParseGrades`)
- [x] `internal/telegram`: `getMe`, `getUpdates`, `sendMessage`, stdlib only,
      with the token scrubbed from errors
- [x] `cmd/kanji-bot`: long-poll loop with backoff, one session per chat,
      idle expiry, stale-update guard, `SIGTERM` shutdown
- [x] Chat-ID allowlist, denying by default, with tests for the deny path
- [x] HTML formatting; no reading above level 1; no answer keyboards
- [x] `/etc/cli-kanji/bot.env` at `0600`, token never in `ps` or logs
- [x] `kanji-bot.service` with `MemoryMax` and `ReadWritePaths`
- [x] `deploy.sh` installs the unit, restarts, and verifies authentication
- [x] `-cram` / `/cram`, for a queue you are still catching up on
- [x] Tests: the drill rule, the reveal grade, command parsing for both
      prefixes, the hint-leak rule at every level, message splitting, token
      scrubbing, and an end-to-end ask → answer → persisted round trip

Left to the person deploying it: fill in `bot.env`, and message the bot from
another account to see the refusal for yourself.
