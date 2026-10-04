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

Target hardware is deliberately modest: a 1 GB DDR2 Intel Atom. So the whole
thing is Go with a pure-Go SQLite driver — no cgo, no runtime dependencies, one
static binary. A full session over a 3,600-card backlog peaks at **22 MB**.

## Quick start

```sh
make build        # compile bin/kanji and bin/kanjidata
make dict         # download the dictionaries and build data/dict.db (~10s, 43 MB)
bin/kanji         # review
```

Without `make`:

```sh
go build -o bin/ ./cmd/kanji ./cmd/kanjidata
bin/kanjidata                 # writes data/dict.db
bin/kanji
```

To put it on another machine, cross-compile and copy two files — the binary and
the dictionary:

```sh
make dist                                     # dist/kanji-linux-386, -amd64, -arm64
scp dist/kanji-linux-386 data/dict.db atom:~/
```

The binary looks for `dict.db` next to itself, then in `./data/`, then in the
per-user config directory, so dropping both in the same folder is enough.

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

## Layout

```
cmd/kanji           the review CLI
cmd/kanjidata       the dictionary importer
internal/kana       romaji → hiragana, and answer normalisation
internal/importer   KANJIDIC2 and JMdict parsers, and the dictionary build
internal/store      both SQLite databases, the schemas and every query
internal/srs        the scheduler
internal/review     queue assembly and grading — no terminal code at all
internal/ui         the line-based interface
```

`make check` runs gofmt, vet and the tests the way CI would.
