package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/OmitNomis/cli-kanji/internal/srs"
)

// Item is one reviewable prompt as the dictionary defines it.
type Item struct {
	Key        string
	Kind       string // "kanji" or "word"
	RefID      int64
	Prompt     string
	Answers    []string // accepted readings in display form
	Normalized []string // the same readings, precomputed for comparison
	Hint       string
	Gloss      string
	Grade      int
	MaxGrade   int
	KanjiCount int
	Common     int
}

// IsKanji reports whether the prompt is a bare kanji rather than a compound.
// The reviewer needs to be told which, because 本 as a kanji accepts any of its
// readings while 本 as a word is ほん and nothing else.
func (i Item) IsKanji() bool { return i.Kind == "kanji" }

// Card is an item together with its review state.
type Card struct {
	Item
	State srs.State
	New   bool // true if this is the first time the item has been seen

	// Probe marks a new card asked without its reading, to find out whether
	// it is already known. A right answer places it high on the ladder; a
	// wrong one teaches it as usual, reading shown.
	Probe bool
}

// HintShown reports whether the reading goes on screen with the prompt: while
// the item is new, unless it is being probed.
func (c Card) HintShown() bool { return !c.Probe && srs.ShowHint(c.State.Level) }

// Selection controls which items are eligible to be introduced.
type Selection struct {
	// MinGrade and MaxGrade bound the school grades taught: 1-6 is elementary,
	// 8 is the middle-school remainder of the joyo list.
	MinGrade int
	MaxGrade int

	// WordTier is the lowest item.common a compound may have: 2 for the entries
	// dictionaries call common, 1 to include the second tier, 0 for everything
	// the dictionary was built with.
	WordTier int

	// GateLevel is the level a kanji must reach before compounds containing it
	// are introduced. 1 means one correct answer is enough.
	GateLevel int
}

// DefaultSelection teaches the whole joyo list and both common word tiers,
// unlocking a compound as soon as every kanji in it has been answered once.
func DefaultSelection() Selection {
	return Selection{MinGrade: 1, MaxGrade: 8, WordTier: 1, GateLevel: 1}
}

// itemColumns is the projection every item query shares, aliased i.
const itemColumns = `i.key, i.kind, i.ref_id, i.prompt, i.answers, i.normalized,
	i.hint, i.gloss, i.grade, i.max_grade, i.kanji_count, i.common`

// reviewColumns is the review-state projection, aliased r.
const reviewColumns = `r.level, r.due_at, r.reps, r.lapses, r.streak,
	r.best_streak, r.correct, r.incorrect, r.added_at, r.last_at`

// Due returns the items that have come round again, soonest first.
func (s *Store) Due(ctx context.Context, now time.Time, limit int) ([]Card, error) {
	return s.queue(ctx, `WHERE r.due_at <= ?`, []any{now.Unix(), limit})
}

// Scheduled returns every item in the queue whatever its due time, the most
// overdue first.
//
// This is what a cram session asks for: someone who already reads much of the
// list wants to get it into the scheduler now rather than meet five characters
// a day and wait four hours between sittings. Answers are graded and persisted
// exactly as they are in a normal session -- the clock is skipped, not the
// ladder -- so a few cram passes leave the queue in a state real SRS can take
// over from.
func (s *Store) Scheduled(ctx context.Context, limit int) ([]Card, error) {
	return s.queue(ctx, ``, []any{limit})
}

// queue runs the card projection with a caller-supplied filter. The ORDER BY is
// shared on purpose: due first, then teaching order, so a cram pass sees the
// same sequence a normal session would once everything has come round.
func (s *Store) queue(ctx context.Context, where string, args []any) ([]Card, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+itemColumns+`, `+reviewColumns+`
		FROM review r
		JOIN `+DictAlias+`.item i ON i.key = r.item_key
		`+where+`
		ORDER BY r.due_at, i.sort_key, i.key
		LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("query queued items: %w", err)
	}
	defer rows.Close()

	var out []Card
	for rows.Next() {
		var c Card
		if err := scanCard(rows, &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// NextKanji returns kanji not yet in the queue, in teaching order: by school
// grade, then by how often the character appears in print.
func (s *Store) NextKanji(ctx context.Context, sel Selection, limit int) ([]Item, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+itemColumns+`
		FROM `+DictAlias+`.item i
		WHERE i.kind = 'kanji'
		  AND i.grade BETWEEN ? AND ?
		  AND NOT EXISTS (SELECT 1 FROM review r WHERE r.item_key = i.key)
		ORDER BY i.sort_key, i.key
		LIMIT ?`, sel.MinGrade, sel.MaxGrade, limit)
	if err != nil {
		return nil, fmt.Errorf("query new kanji: %w", err)
	}
	defer rows.Close()
	return scanItems(rows)
}

// NextWords returns compounds not yet in the queue whose every kanji is already
// known, most frequent first.
//
// The gate is the whole point of introducing words this way: you are never
// shown a compound built from a character you have not met, so a new word is
// always a new combination rather than a new wall.
func (s *Store) NextWords(ctx context.Context, sel Selection, limit int) ([]Item, error) {
	if err := s.refreshLearnedKanji(ctx, sel.GateLevel); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT `+itemColumns+`
		FROM `+DictAlias+`.item i
		WHERE i.kind = 'word'
		  AND i.common >= ?
		  AND i.max_grade <= ?
		  AND i.kanji_count > 0
		  AND NOT EXISTS (SELECT 1 FROM review r WHERE r.item_key = i.key)
		  AND NOT EXISTS (
		        SELECT 1 FROM `+DictAlias+`.word_kanji wk
		        WHERE wk.word_id = i.ref_id
		          AND wk.kanji_id NOT IN (SELECT kanji_id FROM temp.learned_kanji)
		      )
		ORDER BY i.sort_key, i.key
		LIMIT ?`, sel.WordTier, sel.MaxGrade, limit)
	if err != nil {
		return nil, fmt.Errorf("query new words: %w", err)
	}
	defer rows.Close()
	return scanItems(rows)
}

// refreshLearnedKanji materialises the set of known kanji into an indexed temp
// table.
//
// Deriving it inline instead would make the gate a correlated subquery over
// every candidate compound, and the target machine cannot afford that. The item
// key carries the character itself, so unicode(substr(key, 3)) recovers the
// code point that word_kanji is keyed by.
func (s *Store) refreshLearnedKanji(ctx context.Context, gateLevel int) error {
	stmts := []string{
		`DROP TABLE IF EXISTS temp.learned_kanji`,
		`CREATE TEMP TABLE learned_kanji (kanji_id INTEGER PRIMARY KEY)`,
	}
	for _, q := range stmts {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("prepare learned-kanji set: %w", err)
		}
	}

	if _, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO temp.learned_kanji (kanji_id)
		SELECT unicode(substr(r.item_key, 3))
		FROM review r
		WHERE r.item_key LIKE 'k:%' AND r.level >= ?`, gateLevel); err != nil {
		return fmt.Errorf("populate learned-kanji set: %w", err)
	}
	return nil
}

// Save writes an item's review state, inserting it if this was its first answer.
func (s *Store) Save(ctx context.Context, key string, st srs.State) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO review
			(item_key, level, due_at, reps, lapses, streak, best_streak,
			 correct, incorrect, added_at, last_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (item_key) DO UPDATE SET
			level = excluded.level,
			due_at = excluded.due_at,
			reps = excluded.reps,
			lapses = excluded.lapses,
			streak = excluded.streak,
			best_streak = excluded.best_streak,
			correct = excluded.correct,
			incorrect = excluded.incorrect,
			last_at = excluded.last_at`,
		key, st.Level, st.DueAt.Unix(), st.Reps, st.Lapses, st.Streak, st.BestStreak,
		st.Correct, st.Incorrect, st.AddedAt.Unix(), nullUnix(st.LastAt))
	if err != nil {
		return fmt.Errorf("save review %s: %w", key, err)
	}
	return nil
}

// LogAnswer appends to the answer history. Every answer is kept: it costs a row
// and it is the only way to say anything honest about progress later.
func (s *Store) LogAnswer(ctx context.Context, key, given string, correct bool, levelBefore, levelAfter int, at time.Time, elapsed time.Duration) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO answer_log
			(item_key, at, given, correct, level_before, level_after, elapsed_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		key, at.Unix(), given, boolInt(correct), levelBefore, levelAfter, elapsed.Milliseconds())
	if err != nil {
		return fmt.Errorf("log answer for %s: %w", key, err)
	}
	return nil
}

// Counts is the summary shown before and after a session.
type Counts struct {
	DictKanjiItems int
	DictWordItems  int

	InProgress      int // items with any review state
	KanjiInProgress int
	WordsInProgress int
	Due             int
	Burned          int
	Leeches         int

	ByLevel [srs.MaxLevel + 1]int
}

// inDict limits a query on review to items the dictionary still has.
//
// A rebuild can retire an item: entries that share a spelling are merged into
// one, and a word spelled with a single kanji is folded into that kanji's card.
// Its progress row stays, so pointing at an older dictionary brings it back,
// but it can never be asked again and must not be counted as due.
const inDict = `item_key IN (SELECT key FROM ` + DictAlias + `.item)`

// Counts gathers the queue summary in one round trip per figure. There are only
// a handful, and they run against indexed integer columns.
func (s *Store) Counts(ctx context.Context, now time.Time) (Counts, error) {
	var c Counts

	scalars := []struct {
		dst   *int
		query string
		args  []any
	}{
		{&c.DictKanjiItems, `SELECT count(*) FROM ` + DictAlias + `.item WHERE kind = 'kanji'`, nil},
		{&c.DictWordItems, `SELECT count(*) FROM ` + DictAlias + `.item WHERE kind = 'word'`, nil},
		{&c.InProgress, `SELECT count(*) FROM review WHERE ` + inDict, nil},
		{&c.KanjiInProgress, `SELECT count(*) FROM review WHERE item_key LIKE 'k:%' AND ` + inDict, nil},
		{&c.WordsInProgress, `SELECT count(*) FROM review WHERE item_key LIKE 'w:%' AND ` + inDict, nil},
		{&c.Due, `SELECT count(*) FROM review WHERE due_at <= ? AND ` + inDict, []any{now.Unix()}},
		{&c.Burned, `SELECT count(*) FROM review WHERE level >= ? AND ` + inDict, []any{srs.MaxLevel}},
		{&c.Leeches, `SELECT count(*) FROM review WHERE lapses >= ? AND ` + inDict, []any{srs.LeechLapses}},
	}
	for _, sc := range scalars {
		if err := s.db.QueryRowContext(ctx, sc.query, sc.args...).Scan(sc.dst); err != nil {
			return c, fmt.Errorf("count: %w", err)
		}
	}

	rows, err := s.db.QueryContext(ctx, `SELECT level, count(*) FROM review WHERE `+inDict+` GROUP BY level`)
	if err != nil {
		return c, fmt.Errorf("count by level: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var level, n int
		if err := rows.Scan(&level, &n); err != nil {
			return c, err
		}
		if level >= 0 && level <= srs.MaxLevel {
			c.ByLevel[level] = n
		}
	}
	return c, rows.Err()
}

// IntroducedSince counts the kanji and words added to the queue since t, so a
// daily budget can be enforced across several sessions in one day.
func (s *Store) IntroducedSince(ctx context.Context, t time.Time) (kanji, words int, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT
			coalesce(sum(item_key LIKE 'k:%'), 0),
			coalesce(sum(item_key LIKE 'w:%'), 0)
		FROM review WHERE added_at >= ?`, t.Unix()).Scan(&kanji, &words)
	if err != nil {
		return 0, 0, fmt.Errorf("count introductions: %w", err)
	}
	return kanji, words, nil
}

// Activity is what the answer log says about a stretch of time.
type Activity struct {
	Answered int
	Correct  int
	Days     int // distinct local days on which something was answered
}

// Accuracy is the share of answers in the period that were correct.
func (a Activity) Accuracy() float64 {
	if a.Answered == 0 {
		return 0
	}
	return float64(a.Correct) / float64(a.Answered)
}

// Activity summarises the answer log since t.
//
// This is what the log is for. The review table only remembers an item's
// current state, so without the log there is no way to say whether the last
// week went well, only where the queue happens to stand right now.
func (s *Store) Activity(ctx context.Context, since time.Time) (Activity, error) {
	var a Activity
	err := s.db.QueryRowContext(ctx, `
		SELECT count(*),
		       coalesce(sum(correct), 0),
		       count(DISTINCT date(at, 'unixepoch', 'localtime'))
		FROM answer_log WHERE at >= ?`, since.Unix()).Scan(&a.Answered, &a.Correct, &a.Days)
	if err != nil {
		return a, fmt.Errorf("summarise activity: %w", err)
	}
	return a, nil
}

// NextDue reports when the soonest not-yet-due item comes round, so an empty
// session can say how long the wait is instead of just "nothing to do".
func (s *Store) NextDue(ctx context.Context, now time.Time) (time.Time, bool, error) {
	var unix sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT min(due_at) FROM review WHERE due_at > ? AND `+inDict, now.Unix()).Scan(&unix)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("query next due: %w", err)
	}
	if !unix.Valid {
		return time.Time{}, false, nil
	}
	return time.Unix(unix.Int64, 0), true, nil
}

// Meta reads a value written by the importer, such as when the dictionary was
// built.
func (s *Store) Meta(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM `+DictAlias+`.meta WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read meta %s: %w", key, err)
	}
	return v, nil
}

// Setting reads a persisted user preference, returning def when it is unset.
func (s *Store) Setting(ctx context.Context, key, def string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM setting WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return def, nil
	}
	if err != nil {
		return def, fmt.Errorf("read setting %s: %w", key, err)
	}
	return v, nil
}

// SetSetting persists a user preference.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO setting (key, value) VALUES (?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("write setting %s: %w", key, err)
	}
	return nil
}

// Lookup finds items whose prompt matches a kanji or compound exactly, for the
// browse and lookup commands.
//
// A prompt can have several entries: 日 is a kanji, and in a dictionary built
// before homographs were merged it is also two separate JMdict words, ひ and
// にち. The kanji sorts first, since it is the character the others are built
// from.
func (s *Store) Lookup(ctx context.Context, prompt string, limit int) ([]Item, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+itemColumns+`
		FROM `+DictAlias+`.item i
		WHERE i.prompt = ?
		ORDER BY i.kind, i.sort_key
		LIMIT ?`, prompt, limit)
	if err != nil {
		return nil, fmt.Errorf("lookup %s: %w", prompt, err)
	}
	defer rows.Close()
	return scanItems(rows)
}

// CompoundsFor lists the compounds in the dictionary that use a kanji, most
// frequent first, whether or not they are reviewable.
func (s *Store) CompoundsFor(ctx context.Context, kanji rune, limit int) ([]Item, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+itemColumns+`
		FROM `+DictAlias+`.word_kanji wk
		JOIN `+DictAlias+`.item i ON i.kind = 'word' AND i.ref_id = wk.word_id
		WHERE wk.kanji_id = ?
		ORDER BY i.sort_key, i.key
		LIMIT ?`, int64(kanji), limit)
	if err != nil {
		return nil, fmt.Errorf("compounds for %c: %w", kanji, err)
	}
	defer rows.Close()
	return scanItems(rows)
}

// scanner is satisfied by *sql.Row and *sql.Rows alike.
type scanner interface {
	Scan(dest ...any) error
}

func scanItem(row scanner, it *Item) error {
	var answers, normalized string
	err := row.Scan(&it.Key, &it.Kind, &it.RefID, &it.Prompt, &answers, &normalized,
		&it.Hint, &it.Gloss, &it.Grade, &it.MaxGrade, &it.KanjiCount, &it.Common)
	if err != nil {
		return fmt.Errorf("scan item: %w", err)
	}
	it.Answers = splitLines(answers)
	it.Normalized = splitLines(normalized)
	return nil
}

func scanItems(rows *sql.Rows) ([]Item, error) {
	var out []Item
	for rows.Next() {
		var it Item
		if err := scanItem(rows, &it); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func scanCard(row scanner, c *Card) error {
	var answers, normalized string
	var dueAt, addedAt int64
	var lastAt sql.NullInt64

	err := row.Scan(
		&c.Key, &c.Kind, &c.RefID, &c.Prompt, &answers, &normalized,
		&c.Hint, &c.Gloss, &c.Grade, &c.MaxGrade, &c.KanjiCount, &c.Common,
		&c.State.Level, &dueAt, &c.State.Reps, &c.State.Lapses, &c.State.Streak,
		&c.State.BestStreak, &c.State.Correct, &c.State.Incorrect, &addedAt, &lastAt,
	)
	if err != nil {
		return fmt.Errorf("scan card: %w", err)
	}

	c.Answers = splitLines(answers)
	c.Normalized = splitLines(normalized)
	c.State.DueAt = time.Unix(dueAt, 0)
	c.State.AddedAt = time.Unix(addedAt, 0)
	if lastAt.Valid {
		c.State.LastAt = time.Unix(lastAt.Int64, 0)
	}
	return nil
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func nullUnix(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Unix()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
