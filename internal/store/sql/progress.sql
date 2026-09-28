-- Progress schema. Small, writable, and keyed by item.key rather than by rowid
-- so the dictionary underneath can be rebuilt or replaced without losing a
-- single review.

CREATE TABLE IF NOT EXISTS review (
    item_key    TEXT    PRIMARY KEY,
    level       INTEGER NOT NULL DEFAULT 0,
    due_at      INTEGER NOT NULL,            -- unix seconds
    reps        INTEGER NOT NULL DEFAULT 0,
    lapses      INTEGER NOT NULL DEFAULT 0,
    streak      INTEGER NOT NULL DEFAULT 0,
    best_streak INTEGER NOT NULL DEFAULT 0,
    correct     INTEGER NOT NULL DEFAULT 0,
    incorrect   INTEGER NOT NULL DEFAULT 0,
    added_at    INTEGER NOT NULL,
    last_at     INTEGER
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS review_by_due ON review (due_at);
CREATE INDEX IF NOT EXISTS review_by_level ON review (level);

-- Every answer ever given. Cheap to append and makes honest statistics (and
-- leech detection) possible later.
CREATE TABLE IF NOT EXISTS answer_log (
    id           INTEGER PRIMARY KEY,
    item_key     TEXT    NOT NULL,
    at           INTEGER NOT NULL,
    given        TEXT    NOT NULL,
    correct      INTEGER NOT NULL,
    level_before INTEGER NOT NULL,
    level_after  INTEGER NOT NULL,
    elapsed_ms   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS answer_log_by_at ON answer_log (at);
CREATE INDEX IF NOT EXISTS answer_log_by_item ON answer_log (item_key, at);

CREATE TABLE IF NOT EXISTS setting (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) WITHOUT ROWID;
