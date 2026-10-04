-- Dictionary schema. This database is built once on a fast machine by
-- cmd/kanjidata and then shipped read-only, so everything the review loop needs
-- at runtime is precomputed here rather than derived on a 1 GB Atom.

CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) WITHOUT ROWID;

-- One row per kanji in KANJIDIC2. grade is 1-6 for the kyoiku kanji taught in
-- elementary school, 8 for the remaining joyo kanji (middle school), 9 and 10
-- for jinmeiyo, and NULL for everything outside those lists.
CREATE TABLE IF NOT EXISTS kanji (
    id           INTEGER PRIMARY KEY,
    literal      TEXT    NOT NULL UNIQUE,
    grade        INTEGER,
    stroke_count INTEGER NOT NULL DEFAULT 0,
    freq         INTEGER,          -- newspaper frequency rank, 1 = most common
    jlpt         INTEGER,
    meanings     TEXT    NOT NULL DEFAULT '',  -- English, newline separated
    on_readings  TEXT    NOT NULL DEFAULT '',  -- katakana, raw KANJIDIC form
    kun_readings TEXT    NOT NULL DEFAULT '',  -- hiragana with . and - markers
    nanori       TEXT    NOT NULL DEFAULT ''   -- name-only readings
);
CREATE INDEX IF NOT EXISTS kanji_by_grade ON kanji (grade, freq);

-- One row per JMdict entry that has at least one spelling containing a kanji.
-- Pure-kana entries are skipped: they are words, but not kanji compounds.
CREATE TABLE IF NOT EXISTS word (
    id           INTEGER PRIMARY KEY,          -- JMdict ent_seq, stable across releases
    surface      TEXT    NOT NULL,             -- preferred kanji spelling
    alt_surfaces TEXT    NOT NULL DEFAULT '',  -- remaining spellings, newline separated
    reading      TEXT    NOT NULL,             -- preferred reading for `surface`
    glosses      TEXT    NOT NULL DEFAULT '',  -- one sense per line, synonyms joined by '; '
    pos          TEXT    NOT NULL DEFAULT '',
    tags         TEXT    NOT NULL DEFAULT '',  -- JMdict misc tags: arch, obs, rare, uk ...
    common       INTEGER NOT NULL DEFAULT 0,   -- 2 = ichi1/news1/spec1/gai1, 1 = tier two, 0 = neither
    freq_rank    INTEGER NOT NULL DEFAULT 999, -- lower is more common; 999 = unranked
    kanji_count  INTEGER NOT NULL DEFAULT 0,
    max_grade    INTEGER NOT NULL DEFAULT 99   -- highest grade among its kanji; 99 = contains a non-joyo kanji
);
CREATE INDEX IF NOT EXISTS word_by_freq ON word (common DESC, freq_rank);
CREATE INDEX IF NOT EXISTS word_by_surface ON word (surface);

-- Every reading that is valid for a word's preferred spelling. Readings tagged
-- re_nokanji are excluded, since those are explicitly not readings of the
-- kanji form.
CREATE TABLE IF NOT EXISTS word_reading (
    word_id INTEGER NOT NULL,
    reading TEXT    NOT NULL,
    PRIMARY KEY (word_id, reading)
) WITHOUT ROWID;

-- Which kanji appear in which word. This is the gate: a compound only enters
-- the review queue once every kanji in it is known.
CREATE TABLE IF NOT EXISTS word_kanji (
    kanji_id INTEGER NOT NULL,
    word_id  INTEGER NOT NULL,
    PRIMARY KEY (kanji_id, word_id)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS word_kanji_by_word ON word_kanji (word_id);

-- Flattened, ready-to-display review items. `key` is deliberately a stable
-- string ('k:<literal>' or 'w:<ent_seq>') rather than a rowid, so progress
-- survives a dictionary rebuild.
CREATE TABLE IF NOT EXISTS item (
    key         TEXT    PRIMARY KEY,
    kind        TEXT    NOT NULL,            -- 'kanji' or 'word'
    ref_id      INTEGER NOT NULL,
    prompt      TEXT    NOT NULL,            -- what the reviewer sees
    answers     TEXT    NOT NULL,            -- accepted readings, newline separated, display form
    normalized  TEXT    NOT NULL,            -- the same readings run through kana.Normalize
    hint        TEXT    NOT NULL,            -- reading revealed at low levels
    gloss       TEXT    NOT NULL,
    grade       INTEGER NOT NULL DEFAULT 0,
    max_grade   INTEGER NOT NULL DEFAULT 99,
    kanji_count INTEGER NOT NULL DEFAULT 0,
    common      INTEGER NOT NULL DEFAULT 0,
    sort_key    INTEGER NOT NULL             -- teaching order: grade first, then frequency
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS item_by_order ON item (kind, sort_key);
CREATE INDEX IF NOT EXISTS item_by_ref ON item (kind, ref_id);

-- Lookup searches by the displayed text. Without this it scans all 27k items,
-- which costs 35 ms on a developer machine and two thirds of a second on the
-- Atom this is built for -- long enough to feel like a stall when you hit :i
-- in the middle of a session.
CREATE INDEX IF NOT EXISTS item_by_prompt ON item (prompt);
