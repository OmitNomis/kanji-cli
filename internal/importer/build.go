package importer

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/OmitNomis/cli-kanji/internal/kana"
	"github.com/OmitNomis/cli-kanji/internal/store"
)

// Options configures a dictionary build.
type Options struct {
	KanjidicPath string
	JMdictPath   string
	DictPath     string

	// MinWordCommon is the lowest JMdict commonness tier that becomes a review
	// item: 2 for the ~18k entries dictionaries call common, 1 to include the
	// second tier, 0 for every compound in JMdict. Words below the threshold
	// are still stored and still searchable; they are simply never scheduled.
	MinWordCommon int

	// Log receives human-readable progress. nil discards it.
	Log func(format string, args ...any)
}

// Stats is what a build produced, for the summary line and for the meta table.
type Stats struct {
	Kanji        int
	JoyoKanji    int
	Words        int
	WordReadings int
	WordKanji    int
	KanjiItems   int
	WordItems    int
	Elapsed      time.Duration
}

// gloss formatting limits. A terminal prompt has no room for a full dictionary
// entry, and the point of the gloss is recognition, not completeness.
const (
	maxItemMeanings = 3
	maxItemSenses   = 2
)

// nonJoyoGrade is the max_grade stand-in for a word containing a character
// outside the joyo list, so one query can exclude them without a join.
const nonJoyoGrade = 99

// unrankedKanjiFreq sorts kanji with no newspaper frequency rank last within
// their grade.
const unrankedKanjiFreq = 99999

// Build creates the dictionary database from the two XML files.
//
// It writes to a temporary file and renames on success, so a failed or
// interrupted build never leaves a half-populated dictionary where the review
// loop will find it.
func Build(ctx context.Context, opt Options) (*Stats, error) {
	logf := opt.Log
	if logf == nil {
		logf = func(string, ...any) {}
	}

	started := time.Now()
	tmpPath := opt.DictPath + ".building"
	if err := os.Remove(tmpPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("clear stale build file: %w", err)
	}

	st, err := store.OpenDict(ctx, tmpPath)
	if err != nil {
		return nil, err
	}
	defer st.Close()

	b := &builder{
		st: st, logf: logf, opt: opt,
		grades:     make(map[rune]int, 14000),
		joyoKanji:  make(map[rune]bool, 2200),
		kanjiWords: make(map[rune][]string),
	}

	if err := b.run(ctx); err != nil {
		st.Close()
		os.Remove(tmpPath)
		return nil, err
	}
	if err := st.Close(); err != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("close built dictionary: %w", err)
	}

	if err := os.Remove(opt.DictPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("replace existing dictionary: %w", err)
	}
	if err := os.Rename(tmpPath, opt.DictPath); err != nil {
		return nil, fmt.Errorf("move dictionary into place: %w", err)
	}

	b.stats.Elapsed = time.Since(started)
	return &b.stats, nil
}

type builder struct {
	st     *store.Store
	opt    Options
	logf   func(string, ...any)
	stats  Stats
	grades map[rune]int // every kanji's grade, for scoring word difficulty
	joyo   []Kanji      // joyo kanji held back for item generation

	// joyoKanji marks the characters that get a kanji card, and kanjiWords
	// holds the readings of words spelled with one of them alone, which go on
	// that card instead of on a word card of their own.
	joyoKanji  map[rune]bool
	kanjiWords map[rune][]string
}

func (b *builder) run(ctx context.Context) error {
	if err := b.importKanji(ctx); err != nil {
		return err
	}
	if err := b.importWords(ctx); err != nil {
		return err
	}
	if err := b.buildKanjiItems(ctx); err != nil {
		return err
	}
	return b.finish(ctx)
}

// importKanji loads KANJIDIC2. Rows are keyed by Unicode code point rather than
// a synthetic id, which makes word_kanji links derivable from the text itself.
func (b *builder) importKanji(ctx context.Context) error {
	f, err := open(b.opt.KanjidicPath)
	if err != nil {
		return fmt.Errorf("open kanjidic2: %w", err)
	}
	defer f.Close()

	tx, err := b.st.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	ins, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO kanji
			(id, literal, grade, stroke_count, freq, jlpt, meanings, on_readings, kun_readings, nanori)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer ins.Close()

	err = ParseKanjidic(f, func(k Kanji) error {
		runes := []rune(k.Literal)
		if len(runes) != 1 {
			// Variant sequences and the like are not single characters and have
			// nothing to teach.
			return nil
		}
		cp := runes[0]
		b.grades[cp] = k.Grade

		_, err := ins.ExecContext(ctx,
			int64(cp), k.Literal, nullInt(k.Grade), k.StrokeCount, nullInt(k.Freq), nullInt(k.JLPT),
			strings.Join(k.Meanings, "\n"),
			strings.Join(k.On, "\n"),
			strings.Join(k.Kun, "\n"),
			strings.Join(k.Nanori, "\n"),
		)
		if err != nil {
			return fmt.Errorf("insert kanji %s: %w", k.Literal, err)
		}

		b.stats.Kanji++
		if k.IsJoyo() {
			b.stats.JoyoKanji++
			b.joyo = append(b.joyo, k)
			b.joyoKanji[cp] = true
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	b.logf("kanji: %d imported, %d of them joyo", b.stats.Kanji, b.stats.JoyoKanji)
	return nil
}

// importWords loads JMdict, writing the word, its readings, its kanji links and
// its review item in one pass so the 63 MB file is only read once.
func (b *builder) importWords(ctx context.Context) error {
	f, err := open(b.opt.JMdictPath)
	if err != nil {
		return fmt.Errorf("open jmdict: %w", err)
	}
	defer f.Close()

	tx, err := b.st.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	insWord, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO word
			(id, surface, alt_surfaces, reading, glosses, pos, tags, common, freq_rank, kanji_count, max_grade)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer insWord.Close()

	insReading, err := tx.PrepareContext(ctx,
		`INSERT OR IGNORE INTO word_reading (word_id, reading) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer insReading.Close()

	insLink, err := tx.PrepareContext(ctx,
		`INSERT OR IGNORE INTO word_kanji (kanji_id, word_id) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer insLink.Close()

	insItem, err := prepareItemInsert(ctx, tx)
	if err != nil {
		return err
	}
	defer insItem.Close()

	var reviewable []Word
	err = ParseJMdict(f, func(w Word) error {
		runes := KanjiRunes(w.Surface)
		maxGrade := b.wordMaxGrade(runes)

		_, err := insWord.ExecContext(ctx,
			w.Seq, w.Surface, strings.Join(w.AltSurfaces, "\n"), w.Reading,
			strings.Join(w.Glosses, "\n"),
			strings.Join(w.POS, " "),
			strings.Join(w.Tags, " "),
			w.Common, w.FreqRank, len(runes), maxGrade,
		)
		if err != nil {
			return fmt.Errorf("insert word %d (%s): %w", w.Seq, w.Surface, err)
		}
		b.stats.Words++

		for _, r := range w.Readings {
			if _, err := insReading.ExecContext(ctx, w.Seq, r); err != nil {
				return fmt.Errorf("insert reading %s for %s: %w", r, w.Surface, err)
			}
			b.stats.WordReadings++
		}

		for _, r := range runes {
			// Only link kanji the dictionary knows about; a link to a character
			// with no row would gate the word behind something unlearnable.
			if _, ok := b.grades[r]; !ok {
				continue
			}
			if _, err := insLink.ExecContext(ctx, int64(r), w.Seq); err != nil {
				return fmt.Errorf("link %c to %s: %w", r, w.Surface, err)
			}
			b.stats.WordKanji++
		}

		// Review items wait until every entry is in, because entries that
		// share a spelling become one item and JMdict does not list them
		// together.
		if w.Common >= b.opt.MinWordCommon {
			reviewable = append(reviewable, w)
		}

		if b.stats.Words%25000 == 0 {
			b.logf("words: %d imported ...", b.stats.Words)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := b.insertWordItems(ctx, insItem, reviewable); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	b.logf("words: %d imported, %d reviewable, %d kanji links", b.stats.Words, b.stats.WordItems, b.stats.WordKanji)
	return nil
}

// wordMaxGrade scores how advanced a word's characters are: the highest school
// grade among them, or nonJoyoGrade if any character is off the joyo list.
func (b *builder) wordMaxGrade(runes []rune) int {
	if len(runes) == 0 {
		return nonJoyoGrade
	}
	max := 0
	for _, r := range runes {
		g, ok := b.grades[r]
		// Grade 8 is the middle-school joyo block and sorts above grade 6;
		// 9 and 10 are jinmeiyo, which is not joyo.
		if !ok || !((g >= 1 && g <= 6) || g == 8) {
			return nonJoyoGrade
		}
		if g > max {
			max = g
		}
	}
	return max
}

func prepareItemInsert(ctx context.Context, tx *sql.Tx) (*sql.Stmt, error) {
	return tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO item
			(key, kind, ref_id, prompt, answers, normalized, hint, gloss,
			 grade, max_grade, kanji_count, common, sort_key)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
}

// insertWordItems writes one review item per spelling.
//
// A spelling that is a single joyo kanji gets no word item at all: its readings
// go onto the kanji's own card instead. 年 as a word is とし or ねん, and 年 as
// a kanji is ねん or とし; asking both, with nothing on screen to tell them
// apart, is asking the same question twice and grading one answer wrong.
func (b *builder) insertWordItems(ctx context.Context, ins *sql.Stmt, words []Word) error {
	for _, w := range MergeHomographs(words) {
		if r := []rune(w.Surface); len(r) == 1 && b.joyoKanji[r[0]] {
			b.kanjiWords[r[0]] = append(b.kanjiWords[r[0]], w.Readings...)
			continue
		}

		runes := KanjiRunes(w.Surface)
		inserted, err := b.insertWordItem(ctx, ins, w, len(runes), b.wordMaxGrade(runes))
		if err != nil {
			return err
		}
		if inserted {
			b.stats.WordItems++
		}
	}
	return nil
}

func (b *builder) insertWordItem(ctx context.Context, ins *sql.Stmt, w Word, kanjiCount, maxGrade int) (bool, error) {
	answers := w.Readings
	normalized := normalizeAll(answers)
	if len(normalized) == 0 {
		return false, nil
	}

	// A merged entry brought one sense per word it was made from, and each of
	// those is the meaning of one of its readings, so none is dropped.
	gloss := strings.Join(firstN(w.Glosses, max(maxItemSenses, w.entries)), " · ")

	// The hint is the one reading a new card gives away, but a merged card
	// has several right answers and showing one would teach that it is the
	// only one.
	hint := w.Reading
	if w.entries > 1 {
		hint = strings.Join(answers, "・")
	}

	// Frequency leads the ordering: once a compound's kanji are known, how
	// often it actually appears matters more than which grade taught them.
	sortKey := w.FreqRank*1000 + maxGrade*10 + min(kanjiCount, 9)

	_, err := ins.ExecContext(ctx,
		wordKey(w.Seq), "word", w.Seq, w.Surface,
		strings.Join(answers, "\n"), strings.Join(normalized, "\n"),
		hint, gloss,
		0, maxGrade, kanjiCount, w.Common, sortKey,
	)
	if err != nil {
		return false, fmt.Errorf("insert item for word %d (%s): %w", w.Seq, w.Surface, err)
	}
	return true, nil
}

// buildKanjiItems writes one review item per joyo kanji. These are the unlock
// path: a compound is only scheduled once every kanji in it is known.
func (b *builder) buildKanjiItems(ctx context.Context) error {
	tx, err := b.st.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	ins, err := prepareItemInsert(ctx, tx)
	if err != nil {
		return err
	}
	defer ins.Close()

	for _, k := range b.joyo {
		// On readings arrive in katakana and kun readings carry KANJIDIC's
		// okurigana markers; Variants turns both into typeable hiragana and adds
		// the bare stem of a kun reading as an acceptable answer.
		answers := kana.VariantsOf(append(append([]string{}, k.On...), k.Kun...))

		// A word spelled with this kanji alone was folded in here rather than
		// given its own card, so its readings are answers here too. They are
		// nearly always among KANJIDIC's already.
		answers = appendNew(answers, b.kanjiWords[[]rune(k.Literal)[0]])
		normalized := normalizeAll(answers)
		if len(normalized) == 0 {
			// A handful of characters carry no Japanese reading at all.
			continue
		}

		freq := k.Freq
		if freq == 0 {
			freq = unrankedKanjiFreq
		}

		_, err := ins.ExecContext(ctx,
			kanjiKey(k.Literal), "kanji", int64([]rune(k.Literal)[0]), k.Literal,
			strings.Join(answers, "\n"), strings.Join(normalized, "\n"),
			kanjiHint(k), strings.Join(firstN(k.Meanings, maxItemMeanings), "; "),
			k.Grade, k.Grade, 1, 2,
			k.Grade*100000+freq,
		)
		if err != nil {
			return fmt.Errorf("insert item for kanji %s: %w", k.Literal, err)
		}
		b.stats.KanjiItems++
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	b.logf("items: %d kanji, %d words", b.stats.KanjiItems, b.stats.WordItems)
	return nil
}

// kanjiHint is what gets revealed at low levels: the on readings as KANJIDIC
// writes them, then the kun readings with their okurigana boundary intact,
// because knowing where the kanji stops is part of knowing the reading.
func kanjiHint(k Kanji) string {
	var parts []string
	if len(k.On) > 0 {
		parts = append(parts, strings.Join(k.On, "・"))
	}
	if len(k.Kun) > 0 {
		parts = append(parts, strings.Join(k.Kun, "・"))
	}
	return strings.Join(parts, "  ")
}

func (b *builder) finish(ctx context.Context) error {
	meta := map[string]string{
		"built_at":        time.Now().UTC().Format(time.RFC3339),
		"kanji_count":     fmt.Sprint(b.stats.Kanji),
		"joyo_count":      fmt.Sprint(b.stats.JoyoKanji),
		"word_count":      fmt.Sprint(b.stats.Words),
		"kanji_items":     fmt.Sprint(b.stats.KanjiItems),
		"word_items":      fmt.Sprint(b.stats.WordItems),
		"min_word_common": fmt.Sprint(b.opt.MinWordCommon),
		"sources":         "KANJIDIC2 and JMdict, EDRDG, CC BY-SA 4.0",
	}
	for k, v := range meta {
		if _, err := b.st.DB().ExecContext(ctx,
			`INSERT OR REPLACE INTO meta (key, value) VALUES (?, ?)`, k, v); err != nil {
			return fmt.Errorf("write meta %s: %w", k, err)
		}
	}

	// The dictionary is read-only from here on, so it is worth paying once for
	// statistics the query planner can use and for a compacted file.
	b.logf("optimising ...")
	if _, err := b.st.DB().ExecContext(ctx, `ANALYZE`); err != nil {
		return fmt.Errorf("analyze: %w", err)
	}
	if _, err := b.st.DB().ExecContext(ctx, `VACUUM`); err != nil {
		return fmt.Errorf("vacuum: %w", err)
	}
	return nil
}

// Item keys are stable strings rather than rowids, so progress survives a
// rebuild: a kanji is identified by the character and a word by its JMdict
// sequence number, both of which outlive any given release.
func kanjiKey(literal string) string { return "k:" + literal }
func wordKey(seq int64) string       { return fmt.Sprintf("w:%d", seq) }

func normalizeAll(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		n := kana.Normalize(s)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// appendNew appends the strings of more that dst does not already hold.
func appendNew(dst, more []string) []string {
	for _, s := range more {
		if !slices.Contains(dst, s) {
			dst = append(dst, s)
		}
	}
	return dst
}

func firstN(in []string, n int) []string {
	if len(in) <= n {
		return in
	}
	return in[:n]
}

func nullInt(v int) any {
	if v == 0 {
		return nil
	}
	return v
}
