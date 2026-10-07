# cli-kanji

A terminal kanji SRS that makes you **type** the reading. No multiple choice,
no self-grading, no "did you get that right?" — you either produce the kana or
you do not.

```
  ── 14/60 · word · learning ──────────────────────────────── 312 left

        写 真

  > shashin
    ✓  しゃしん   photograph; photo; picture
       learning → familiar · back in 8h
```

Target hardware is deliberately modest, and it is where this actually runs: an
Intel Atom N280 at 1.66 GHz with 988 MB of DDR2. So the whole thing is Go with
a pure-Go SQLite driver — no cgo, no runtime dependencies, one 6.2 MB static
binary. On that machine it starts in 76 ms and peaks at 8 MB of RSS.

## Quick start

```sh
make build        # compile bin/kanji, bin/kanjidata and bin/kanji-bot
make dict         # download the dictionaries and build data/dict.db (~10s, 43 MB)
bin/kanji         # review
```

To put it on another machine instead, skip to [Deploying](#deploying-to-another-machine).

Without `make`:

```sh
go build -o bin/ ./cmd/kanji ./cmd/kanjidata
bin/kanjidata                 # writes data/dict.db
bin/kanji
```

The Telegram front-end is `./cmd/kanji-bot`, and needs a token rather than a
terminal — see [Reviewing from Telegram](#reviewing-from-telegram).

## Deploying to another machine

```sh
scripts/deploy.sh root@10.0.0.5
```

That is the whole install. The script detects the target's architecture, builds
for it, sends the dictionary compressed (18 MB rather than 43 MB, which matters
over a relayed link), checksums it on arrival, and installs two files:

| | |
|---|---|
| `/usr/local/bin/kanji` | the static binary |
| `~/.config/cli-kanji/dict.db` | the dictionary, in a place the binary already looks |

Nothing else is configured, and nothing is left running. The target needs only
an SSH server and `gzip`.

You are asked for the target's password **once**, on the first run, so the
script can install a deploy key; every run afterwards is passwordless. It is
safe to re-run — the dictionary is only re-sent when it has actually changed,
so a routine update moves 6 MB instead of 18 MB.

Put the target in `scripts/deploy.env` (untracked) to stop typing it:

```sh
echo 'KANJI_HOST=root@10.0.0.5' > scripts/deploy.env
scripts/deploy.sh
```

Options: `WITH_IMPORTER=1` also installs `kanjidata`, so the target can rebuild
its own dictionary later; `WITH_BOT=1` also installs the Telegram bot and its
systemd unit; `WITH_GREETING=1` prints your due count when you log in;
`NO_KEY=1` skips key setup and lets ssh prompt each time;
`EXTRA_BINARIES="kanji-bot"` ships further `cmd/` packages, skipping any that
do not exist yet.

### The CLI is not a service; the bot is

`kanji` has nothing to run as a daemon — it is an interactive program that
exits when you are done, so a systemd unit would have nothing to supervise.
`WITH_GREETING=1` is the useful version of that idea: it appends a guarded
block to the target's shell profile so logging in tells you how many reviews
are waiting. Remove the `>>> cli-kanji greeting >>>` block to undo it.

The Telegram front-end is the exception, because long polling means a process
has to be alive between your messages. `WITH_BOT=1` installs it as
`kanji-bot.service` — see [Reviewing from Telegram](#reviewing-from-telegram).

### Measured on the target it was built for

An Intel Atom N280 at 1.66 GHz, 988 MB of DDR2, Debian 12, 32-bit:

| | |
|---|---|
| startup (`kanji version`) | 76 ms |
| `kanji stats` | 79 ms |
| `kanji info 国境` | 48 ms |
| peak RSS | 8 MB |
| binary | 6.2 MB |

## What it teaches

*Elementary 1-6 plus middle school* and *jōyō* are the same list, so the
dictionary carries all of it with grades attached:

| Grade | Count | |
|---|---|---|
| 1-6 | 1,026 | the kyōiku kanji, taught in elementary school |
| 8 | 1,110 | the rest of the jōyō list, taught from middle school |
| **total** | **2,136** | the jōyō list exactly |

On top of that: **175,594 compounds** — every JMdict entry with a kanji
spelling, with all 475,564 kanji-to-word links. Of those, 25,406 are scheduled
for review by default (JMdict's two "common" frequency tiers). The rest stay in
the dictionary for `kanji info` and become reviewable with `-tier 0`.

Grades 9 and 10 (jinmeiyō, the name kanji) are imported so compounds can
reference them, but never scheduled.

## How a session works

**Compounds are gated behind their kanji.** A word only enters the queue once
*every* kanji in it is known, so a new compound is always a new combination
rather than a new wall. Knowing just 日, 本 and 人 unlocks 日本人, 本人, 本日,
日々 and 人々 — and nothing you have not met.

**New items are woven into the backlog**, one every fourth card by default, so
three hundred due reviews never stand between you and something new.

**New kanji come first**, in school-grade order and then by how often the
character appears in newspapers, because every compound is gated behind them.
The queue is assembled when the session opens, so compounds unlocked by kanji
you learn today turn up in the next session rather than the current one.

**Daily budgets are per day, not per session**: starting the program twice does
not double your intake.

### The scheduler

Ten levels. A correct answer climbs one rung; a wrong answer drops one near the
bottom and two from level three up, because forgetting something you had held
for a month costs more than forgetting yesterday's.

| Level | Back in | | Level | Back in |
|---|---|---|---|---|
| 0 new | 10m | | 5 known | 7d |
| 1 learning | 4h | | 6 known | 14d |
| 2 learning | 8h | | 7 solid | 30d |
| 3 familiar | 1d | | 8 solid | 90d |
| 4 familiar | 3d | | 9 burned | 180d |

**The reading is shown at levels 0 and 1.** A brand-new item has nothing to
recall yet, so the first encounters teach it and the later ones test it.

**A missed card has to be retyped before the session moves on.** Reading the
answer and pressing Enter teaches nothing — which is the same reason this
program has no multiple choice. The retype is not graded; the card was already
graded on the attempt that failed. It also comes back a few cards later in the
same session.

This is SM-2 with the ease factor removed. SM-2 earns its per-item ease from a
four-way self-grade, and typing a reading has no self-grade to earn it from: an
answer either is the reading or it is not. What is left is a fixed interval
ladder, which also reduces an item's entire schedule to one integer on disk.

### Catching up, if you already read some of this

Spaced repetition is the wrong tool for a list you partly know already. Five
new characters a day and four hours between sittings is the right pace for
learning something; it is a waste of a fortnight if what you actually need is
to tell the scheduler which hundred kanji you can already read.

`-cram` skips the clock:

```sh
kanji -cram -new-kanji 40 -new-words 60     # a catch-up sitting
kanji -cram                                 # again, and again
```

Two things change, and nothing else. Everything in the queue is asked whether
or not it has come round, and the daily new-item budget becomes a per-session
one, so running it four times introduces four times as much. Answers are still
graded, still persisted, and still given real due times — it is the *waiting*
that is skipped, not the ladder. So a correct answer in a cram session climbs a
rung exactly as it would otherwise, and a few passes leave a queue that
ordinary review can take over from. Things you miss drop back down and come
round quickly, which is the point: cramming sorts the list into what you know
and what you do not.

When you are up to speed, stop passing `-cram` and the clock comes back.

## Typing the answer

Romaji and kana both work, and you need no IME:

```
shashin   →  しゃしん     syasin    →  しゃしん     しゃしん  ✓
gakkou    →  がっこう     gakkoo    →  がっこう     ガッコウ  ✓
matcha    →  まっちゃ     shimbun   →  しんぶん     kin'en    →  きんえん
```

Hepburn, Kunrei and the wapuro spellings an IME accepts all land on the same
kana. Long vowels are canonicalised, so `ー` expands to the vowel it repeats
and `おう`/`おお` compare equal — `koohii`, `kouhii` and `コーヒー` are one
answer. Length still matters: こうこう and ここう stay different readings.

For a bare kanji, any on or kun reading counts, and a kun reading's stem counts
too: 写 accepts しゃ, じゃ, うつ, うつす, うつる or うつし. For a compound, only
that compound's readings count — 国境 takes こっきょう or くにざかい, and 日本
takes にほん or にっぽん. Prompts always say which kind they are, because a bare
本 is otherwise ambiguous in a way that changes the right answer.

### Commands at the prompt

Commands take a colon prefix, because single letters collide with real answers
— い is a reading of 五, so a bare `i` has to mean い and not "info".

| | |
|---|---|
| `?` | reveal the reading (graded as wrong, because being told is not knowing) |
| `:s` | skip without grading |
| `:i` / `:i 国境` | the full entry for this card, or for anything else |
| `:stats` | progress, mid-session |
| `:q` | end the session |

Ctrl-D also ends it. Every answer is written to disk as it is given, so
quitting — or losing power — costs nothing but the summary.

## Commands

```
kanji                      review
kanji stats                progress without reviewing
kanji info 国境            an entry, and the compounds that use it
kanji version              which dictionary build is installed
```

Useful flags:

| Flag | Default | |
|---|---|---|
| `-new-kanji` | 5 | new kanji per day |
| `-new-words` | 15 | new compounds per day |
| `-max` | 0 | stop after N cards (0 = everything due) |
| `-grades` | `1-8` | `1-6` for elementary only, `3` for one grade |
| `-tier` | 1 | 2 common compounds only, 1 also second tier, 0 all 175k |
| `-gate` | 1 | level a kanji must reach before its compounds appear |
| `-cram` | off | ignore due times, and spend the whole new-item budget now |
| `-width` | 72 | terminal columns |
| `-dict`, `-progress` | | database locations |

`CLI_KANJI_DICT` and `CLI_KANJI_PROGRESS` override the paths.

## Two databases

Progress lives in its own file, keyed by a stable string — `k:写`, `w:1321400`
— rather than a row id. Rebuilding or replacing the dictionary therefore costs
nothing: a kanji is identified by the character and a compound by its JMdict
sequence number, both of which outlive any given release.

| | |
|---|---|
| `data/dict.db` | 43 MB, read-only, built once by `kanjidata`, ships with the binary |
| `~/.config/cli-kanji/progress.db` | a few hundred kB, yours, never overwritten |

They are attached to one connection, so a query can join review state against
dictionary rows in a single statement. Everything the review loop needs is
precomputed at build time — including each answer's normalised form — so the
Atom never converts kana to decide whether you were right. Selecting the next
unlocked compound out of 25,406 candidates takes **under a millisecond**.

## Rebuilding the dictionary

```sh
bin/kanjidata -refresh          # re-download and rebuild
bin/kanjidata -tier 0           # make every JMdict compound reviewable
bin/kanjidata -out /tmp/dict.db # somewhere else
```

The build writes to a temporary file and renames on success, so an interrupted
run never leaves a half-populated dictionary where the review loop will find
it. Sources are read straight from their gzip, which keeps 78 MB of XML off
disk entirely.

## Data sources

Both dictionaries come from the
[Electronic Dictionary Research and Development Group](https://www.edrdg.org/)
and are used under
[CC BY-SA 4.0](https://creativecommons.org/licenses/by-sa/4.0/):

- **KANJIDIC2** — kanji with on/kun readings, meanings, school grade, stroke
  count and newspaper frequency rank.
- **JMdict** (English edition) — compounds, with the `ichi1`/`news1`/`spec1`
  frequency tags the review tiers are built from.

The importer honours the restriction machinery a naive parse ignores:
search-only and rare spellings are not taught, `re_nokanji` readings are not
accepted as readings of the kanji form, `re_restr` keeps 夕's reading out of
夕方, and `stagk` drops senses belonging to a spelling that was not chosen.
Entries whose every surviving sense is archaic are not scheduled, however often
a newspaper printed them.

Any redistribution of `dict.db` carries the CC BY-SA 4.0 attribution and
share-alike terms with it.

## Reviewing from Telegram

`kanji-bot` is the same queue on a phone. It is a second front-end over the
same `review.Session` and the same `progress.db`, not a second program: a card
answered on a commute is answered for the desk too, and the drill, the
grading and the command vocabulary are the hoisted ones, so there is no second
copy of the rules to get out of step.

```
14/60 · word · learning · 312 left

写 真

> shashin

✓ しゃしん
photograph; photo; picture
learning → familiar · back in 8h
```

| | |
|---|---|
| `/review` | start or resume a session |
| `/cram` | ignore due times and work through the queue now |
| `/reveal` | show the reading, graded as wrong |
| `/skip` | skip without grading |
| `/info 国境` | an entry and the compounds that use it |
| `/stats` | progress |
| `/stop` | end the session |

Anything not starting with `/` is an answer, and romaji works, so no IME is
needed on a phone keyboard. There are no answer buttons, deliberately: tapping
one is multiple choice, which is the thing this program exists not to be.

### Setting it up

```sh
WITH_BOT=1 scripts/deploy.sh
```

That ships the binary, installs `/etc/systemd/system/kanji-bot.service` and
creates `/etc/cli-kanji/bot.env` at mode `0600` with placeholders. The token
never goes in the repository and is never passed as an argument — arguments are
visible in `ps` to every user on the box, and systemd's `EnvironmentFile` is
not. Fill it in:

```
TELEGRAM_BOT_TOKEN=123456:ABC...        # from @BotFather
TELEGRAM_ALLOWED_CHAT_IDS=123456789     # who may use it
```

**The allowlist is not optional.** A bot token is a public endpoint: anyone who
learns the username can message it, and an unguarded bot is a stranger drilling
your deck and writing to your progress file. An empty list therefore denies
everybody rather than permitting everybody — and that is how you find your own
chat ID in the first place:
start the bot with the list empty, message it once, and read the refusal out of
the journal.

```sh
ssh root@host "journalctl -u kanji-bot | grep 'rejected chat'"
```

Put that number in `bot.env`, `systemctl restart kanji-bot`, and you are the
only person it will talk to.

### Why it needs a daemon

Telegram has exactly two delivery models, and both need a process alive between
your messages. Webhooks need a public HTTPS endpoint with a valid certificate;
long polling needs nothing but outbound HTTP. The target sits behind Tailscale
with no public address, so long polling is both the only practical option and
the better one — nothing exposed, nothing to renew.

A restart loses the assembled queue and nothing else, because every answer is
written to disk as it is given. The next message simply starts a fresh session
over whatever is due. Idle sessions are dropped after an hour so an abandoned
chat does not pin a queue in memory, and `systemctl stop kanji-bot` leaves a
completely working CLI behind: the bot adds a binary and a unit, and changes
nothing the CLI depends on.

The design notes, the measurements behind them and the deployment detail are in
**[telegram_update.md](telegram_update.md)**.

## Layout

```
cmd/kanji           the review CLI
cmd/kanji-bot       the Telegram daemon: long poll, one session per chat
cmd/kanjidata       the dictionary importer
internal/kana       romaji → hiragana, and answer normalisation
internal/importer   KANJIDIC2 and JMdict parsers, and the dictionary build
internal/store      both SQLite databases, the schemas and every query
internal/srs        the scheduler
internal/review     queue assembly, grading and the ask/drill state machine —
                    no terminal code at all, which is what makes two
                    front-ends cheap
internal/telegram   Bot API client (stdlib only) and HTML message formatting
internal/ui         the line-based interface
```

Both front-ends are thin. `internal/review` decides what a typed line *means*
— `review.ParseCommand` classifies it, `review.Turn` applies it and says what
happened — and a front-end only chooses how to draw the result. `cmd/kanji`
draws it with ANSI and indents; `cmd/kanji-bot` draws it as one HTML message.

`make check` runs gofmt, vet and the tests the way CI would.
