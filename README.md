# cli-kanji

A terminal kanji SRS that makes you *type* the reading. No multiple choice.

Target hardware is deliberately low: a 1 GB DDR2 Intel Atom. So: Go, SQLite
(pure-Go driver, no CGO), line-based I/O, and a database that is built once on
a fast machine and shipped ready to use.

## Status

Under construction.

## Data sources

Both from the [Electronic Dictionary Research and Development Group](https://www.edrdg.org/),
used under [CC BY-SA 4.0](https://creativecommons.org/licenses/by-sa/4.0/):

- **KANJIDIC2** — 13k kanji with on/kun readings, meanings, grade and stroke count.
  All 2,136 jōyō are imported with their grade, so "elementary 1-6" (grades 1-6,
  the 1,026 kyōiku kanji) and "middle school" (the remaining 1,110) are both
  addressable.
- **JMdict** (English edition) — ~200k entries, imported in full. Frequency tags
  (`ichi1`, `news1`, `spec1`) are preserved so the review queue can be limited to
  common vocabulary while the dictionary stays complete.
