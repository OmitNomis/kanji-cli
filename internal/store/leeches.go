package store

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/OmitNomis/cli-kanji/internal/kana"
	"github.com/OmitNomis/cli-kanji/internal/srs"
)

// Leech is an item forgotten often enough to be worth looking at on its own,
// with what was typed for it instead of the reading.
type Leech struct {
	Card  Card
	Wrong []Miss // most often typed first
}

// Miss is one wrong answer and how many times it was given.
type Miss struct {
	Kana  string // as typed, converted to kana
	Count int
}

// leechMisses bounds how many distinct wrong answers a leech lists. Past the
// top few they are typos, not a pattern.
const leechMisses = 3

// Leeches lists the items forgotten srs.LeechLapses times or more, the most
// forgotten first.
//
// The wrong answers are what make the list worth reading: a leech you keep
// answering with the same wrong reading is a confusion, and one you answer
// differently every time is a reading that never stuck. They are grouped by
// the kana they become, so shashin and しゃしん count as one answer, but not
// normalised further: こくきょう and こっきょう are exactly the difference that
// matters here.
func (s *Store) Leeches(ctx context.Context) ([]Leech, error) {
	cards, err := s.LeechCards(ctx, -1)
	if err != nil {
		return nil, err
	}

	out := make([]Leech, 0, len(cards))
	for _, c := range cards {
		misses, err := s.misses(ctx, c.Key)
		if err != nil {
			return nil, err
		}
		out = append(out, Leech{Card: c, Wrong: misses})
	}
	return out, nil
}

// LeechCards returns the leeches as cards to review, the most forgotten
// first. A negative limit means all of them.
func (s *Store) LeechCards(ctx context.Context, limit int) ([]Card, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+itemColumns+`, `+reviewColumns+`
		FROM review r
		JOIN `+DictAlias+`.item i ON i.key = r.item_key
		WHERE r.lapses >= ?
		ORDER BY r.lapses DESC, r.level, i.sort_key
		LIMIT ?`, srs.LeechLapses, limit)
	if err != nil {
		return nil, fmt.Errorf("query leeches: %w", err)
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

func (s *Store) misses(ctx context.Context, key string) ([]Miss, error) {
	// A reveal is logged as an empty answer: a miss, but not something typed.
	rows, err := s.db.QueryContext(ctx, `
		SELECT given FROM answer_log
		WHERE item_key = ? AND correct = 0 AND given != ''`, key)
	if err != nil {
		return nil, fmt.Errorf("misses for %s: %w", key, err)
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var given string
		if err := rows.Scan(&given); err != nil {
			return nil, err
		}
		counts[kana.ToHiragana(given)]++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]Miss, 0, len(counts))
	for k, n := range counts {
		out = append(out, Miss{Kana: k, Count: n})
	}
	slices.SortFunc(out, func(a, b Miss) int {
		if c := cmp.Compare(b.Count, a.Count); c != 0 {
			return c
		}
		return cmp.Compare(a.Kana, b.Kana)
	})
	return out[:min(len(out), leechMisses)], nil
}

// KnownWithKanji lists compounds you already know that share a kanji with the
// item: for a kanji, words that use it; for a word, other words that use any
// of its kanji. "Know" means reached familiar, three levels up.
//
// It is what a leech gets shown after a miss. A reading that will not stick on
// its own often sticks once it is seen in words that already have.
func (s *Store) KnownWithKanji(ctx context.Context, it Item, limit int) ([]Item, error) {
	var kanji string
	var args []any
	if it.IsKanji() {
		kanji = `SELECT ?`
		args = append(args, int64([]rune(it.Prompt)[0]))
	} else {
		kanji = `SELECT kanji_id FROM ` + DictAlias + `.word_kanji WHERE word_id = ?`
		args = append(args, it.RefID)
	}
	args = append(args, it.Key, knownLevel, limit)

	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT `+itemColumns+`
		FROM `+DictAlias+`.word_kanji wk
		JOIN `+DictAlias+`.item i ON i.kind = 'word' AND i.ref_id = wk.word_id
		JOIN review r ON r.item_key = i.key
		WHERE wk.kanji_id IN (`+kanji+`)
		  AND i.key != ?
		  AND r.level >= ?
		ORDER BY r.level DESC, i.sort_key
		LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("known words with the kanji of %s: %w", it.Prompt, err)
	}
	defer rows.Close()
	return scanItems(rows)
}

// knownLevel is the level from which an item counts as known well enough to
// lean on: familiar, a day or more between sightings.
const knownLevel = 3
