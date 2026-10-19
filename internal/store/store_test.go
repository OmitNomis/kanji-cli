package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/OmitNomis/cli-kanji/internal/srs"
)

var testNow = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

// fixture builds a miniature dictionary on disk and opens it alongside a fresh
// progress database. The kanji are 日 and 本 (grade 1) and 語 (grade 2); the
// compounds are 日本 and 日本語, so the gate has something to gate.
func fixture(t *testing.T) *Store {
	t.Helper()

	dir := t.TempDir()
	dictPath := filepath.Join(dir, "dict.db")

	dict, err := OpenDict(context.Background(), dictPath)
	if err != nil {
		t.Fatalf("OpenDict: %v", err)
	}

	type kanjiRow struct {
		literal string
		grade   int
		freq    int
	}
	kanji := []kanjiRow{{"日", 1, 1}, {"本", 1, 10}, {"語", 2, 300}}
	for _, k := range kanji {
		cp := int64([]rune(k.literal)[0])
		exec(t, dict.DB(), `INSERT INTO kanji (id, literal, grade, stroke_count) VALUES (?, ?, ?, 4)`,
			cp, k.literal, k.grade)
		exec(t, dict.DB(), `
			INSERT INTO item (key, kind, ref_id, prompt, answers, normalized, hint, gloss,
			                  grade, max_grade, kanji_count, common, sort_key)
			VALUES (?, 'kanji', ?, ?, ?, ?, ?, ?, ?, ?, 1, 2, ?)`,
			"k:"+k.literal, cp, k.literal, "よみ", "よみ", "hint", "gloss",
			k.grade, k.grade, k.grade*100000+k.freq)
	}

	type wordRow struct {
		id      int64
		surface string
		reading string
		kanji   string
		rank    int
	}
	words := []wordRow{
		{1, "日本", "にほん", "日本", 1},
		{2, "日本語", "にほんご", "日本語", 5},
	}
	for _, w := range words {
		runes := []rune(w.kanji)
		maxGrade := 1
		for _, r := range runes {
			if r == '語' {
				maxGrade = 2
			}
		}
		exec(t, dict.DB(), `
			INSERT INTO word (id, surface, reading, glosses, common, freq_rank, kanji_count, max_grade)
			VALUES (?, ?, ?, 'gloss', 2, ?, ?, ?)`,
			w.id, w.surface, w.reading, w.rank, len(runes), maxGrade)
		for _, r := range runes {
			exec(t, dict.DB(), `INSERT INTO word_kanji (kanji_id, word_id) VALUES (?, ?)`, int64(r), w.id)
		}
		exec(t, dict.DB(), `
			INSERT INTO item (key, kind, ref_id, prompt, answers, normalized, hint, gloss,
			                  grade, max_grade, kanji_count, common, sort_key)
			VALUES (?, 'word', ?, ?, ?, ?, ?, 'gloss', 0, ?, ?, 2, ?)`,
			wordItemKey(w.id), w.id, w.surface, w.reading, w.reading, w.reading,
			maxGrade, len(runes), w.rank*1000)
	}

	if err := dict.Close(); err != nil {
		t.Fatalf("close dict: %v", err)
	}

	s, err := Open(context.Background(), dictPath, filepath.Join(dir, "progress.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func wordItemKey(id int64) string {
	switch id {
	case 1:
		return "w:1"
	default:
		return "w:2"
	}
}

func exec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func prompts(items []Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Prompt
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// learn puts a kanji into the queue at the given level.
func learn(t *testing.T, s *Store, literal string, level int) {
	t.Helper()
	st := srs.State{Level: level, DueAt: testNow.Add(time.Hour), AddedAt: testNow, Reps: 1, Correct: 1}
	if err := s.Save(context.Background(), "k:"+literal, st); err != nil {
		t.Fatalf("Save k:%s: %v", literal, err)
	}
}

func TestOpenRejectsAnEmptyDictionary(t *testing.T) {
	dir := t.TempDir()
	dictPath := filepath.Join(dir, "dict.db")

	dict, err := OpenDict(context.Background(), dictPath)
	if err != nil {
		t.Fatalf("OpenDict: %v", err)
	}
	dict.Close()

	// The schema exists but there are no items. Failing here is much kinder
	// than failing mid-session.
	if _, err := Open(context.Background(), dictPath, filepath.Join(dir, "progress.db")); err == nil {
		t.Error("Open accepted a dictionary with no review items")
	}
}

func TestOpenRejectsAMissingDictionary(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(context.Background(), filepath.Join(dir, "absent.db"), filepath.Join(dir, "progress.db")); err == nil {
		t.Error("Open accepted a dictionary path that does not exist")
	}
}

func TestNextKanjiFollowsTeachingOrder(t *testing.T) {
	s := fixture(t)
	got, err := s.NextKanji(context.Background(), DefaultSelection(), 10)
	if err != nil {
		t.Fatalf("NextKanji: %v", err)
	}
	// Grade first, then newspaper frequency within the grade.
	if want := []string{"日", "本", "語"}; !equal(prompts(got), want) {
		t.Errorf("NextKanji = %v, want %v", prompts(got), want)
	}
}

func TestNextKanjiRespectsGradeBounds(t *testing.T) {
	s := fixture(t)
	sel := DefaultSelection()
	sel.MaxGrade = 1

	got, err := s.NextKanji(context.Background(), sel, 10)
	if err != nil {
		t.Fatalf("NextKanji: %v", err)
	}
	if want := []string{"日", "本"}; !equal(prompts(got), want) {
		t.Errorf("NextKanji with MaxGrade 1 = %v, want %v", prompts(got), want)
	}
}

func TestNextKanjiSkipsWhatIsAlreadyQueued(t *testing.T) {
	s := fixture(t)
	learn(t, s, "日", 3)

	got, err := s.NextKanji(context.Background(), DefaultSelection(), 10)
	if err != nil {
		t.Fatalf("NextKanji: %v", err)
	}
	if want := []string{"本", "語"}; !equal(prompts(got), want) {
		t.Errorf("NextKanji = %v, want %v", prompts(got), want)
	}
}

func TestWordGateOpensOnlyWhenEveryKanjiIsKnown(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)
	sel := DefaultSelection()

	got, err := s.NextWords(ctx, sel, 10)
	if err != nil {
		t.Fatalf("NextWords: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("NextWords with nothing learned = %v, want none", prompts(got))
	}

	// 日 alone is not enough for 日本.
	learn(t, s, "日", 1)
	if got, err = s.NextWords(ctx, sel, 10); err != nil {
		t.Fatalf("NextWords: %v", err)
	} else if len(got) != 0 {
		t.Fatalf("NextWords knowing only 日 = %v, want none", prompts(got))
	}

	// With 本 as well, 日本 unlocks but 日本語 still does not.
	learn(t, s, "本", 1)
	if got, err = s.NextWords(ctx, sel, 10); err != nil {
		t.Fatalf("NextWords: %v", err)
	} else if want := []string{"日本"}; !equal(prompts(got), want) {
		t.Fatalf("NextWords knowing 日 and 本 = %v, want %v", prompts(got), want)
	}

	learn(t, s, "語", 1)
	if got, err = s.NextWords(ctx, sel, 10); err != nil {
		t.Fatalf("NextWords: %v", err)
	} else if want := []string{"日本", "日本語"}; !equal(prompts(got), want) {
		t.Errorf("NextWords knowing all three = %v, want %v", prompts(got), want)
	}
}

func TestWordGateRespectsGateLevel(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)

	// Both kanji are in the queue but neither has been answered correctly.
	learn(t, s, "日", 0)
	learn(t, s, "本", 0)

	sel := DefaultSelection() // GateLevel 1
	got, err := s.NextWords(ctx, sel, 10)
	if err != nil {
		t.Fatalf("NextWords: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("NextWords = %v, want none while both kanji are still at level 0", prompts(got))
	}

	// Lowering the gate to zero makes merely having met them enough.
	sel.GateLevel = 0
	if got, err = s.NextWords(ctx, sel, 10); err != nil {
		t.Fatalf("NextWords: %v", err)
	} else if want := []string{"日本"}; !equal(prompts(got), want) {
		t.Errorf("NextWords with GateLevel 0 = %v, want %v", prompts(got), want)
	}
}

func TestWordGateRespectsTierAndSkipsQueued(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)
	learn(t, s, "日", 1)
	learn(t, s, "本", 1)
	learn(t, s, "語", 1)

	// Nothing in the fixture reaches tier 3, so an impossible tier empties the
	// result rather than ignoring the filter.
	sel := DefaultSelection()
	sel.WordTier = 3
	if got, err := s.NextWords(ctx, sel, 10); err != nil {
		t.Fatalf("NextWords: %v", err)
	} else if len(got) != 0 {
		t.Errorf("NextWords at tier 3 = %v, want none", prompts(got))
	}

	// Once 日本 is queued it stops being a candidate for introduction.
	if err := s.Save(ctx, "w:1", srs.State{Level: 1, DueAt: testNow, AddedAt: testNow}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got, err := s.NextWords(ctx, DefaultSelection(), 10); err != nil {
		t.Fatalf("NextWords: %v", err)
	} else if want := []string{"日本語"}; !equal(prompts(got), want) {
		t.Errorf("NextWords = %v, want %v", prompts(got), want)
	}
}

func TestSaveRoundTripsAndDueOrders(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)

	early := srs.State{
		Level: 4, DueAt: testNow.Add(-2 * time.Hour), Reps: 9, Lapses: 2, Streak: 3,
		BestStreak: 5, Correct: 7, Incorrect: 2, AddedAt: testNow.Add(-72 * time.Hour),
		LastAt: testNow.Add(-26 * time.Hour),
	}
	late := srs.State{Level: 1, DueAt: testNow.Add(-time.Minute), AddedAt: testNow.Add(-time.Hour)}
	future := srs.State{Level: 6, DueAt: testNow.Add(48 * time.Hour), AddedAt: testNow}

	if err := s.Save(ctx, "k:日", early); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save(ctx, "k:本", late); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save(ctx, "k:語", future); err != nil {
		t.Fatalf("Save: %v", err)
	}

	due, err := s.Due(ctx, testNow, 10)
	if err != nil {
		t.Fatalf("Due: %v", err)
	}
	if want := []string{"日", "本"}; !equal(prompts(cards(due)), want) {
		t.Fatalf("Due = %v, want %v (soonest first, nothing from the future)", prompts(cards(due)), want)
	}

	got := due[0].State
	if got.Level != early.Level || got.Reps != early.Reps || got.Lapses != early.Lapses ||
		got.Streak != early.Streak || got.BestStreak != early.BestStreak ||
		got.Correct != early.Correct || got.Incorrect != early.Incorrect {
		t.Errorf("round-tripped state = %+v, want %+v", got, early)
	}
	if !got.DueAt.Equal(early.DueAt) || !got.AddedAt.Equal(early.AddedAt) || !got.LastAt.Equal(early.LastAt) {
		t.Errorf("round-tripped times = %v/%v/%v, want %v/%v/%v",
			got.DueAt, got.AddedAt, got.LastAt, early.DueAt, early.AddedAt, early.LastAt)
	}
	if !equal(due[0].Answers, []string{"よみ"}) {
		t.Errorf("answers = %v, want [よみ]", due[0].Answers)
	}
}

func TestSaveUpdatesWithoutResettingAddedAt(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)

	added := testNow.Add(-100 * time.Hour)
	if err := s.Save(ctx, "k:日", srs.State{Level: 1, DueAt: testNow.Add(-time.Hour), AddedAt: added}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save(ctx, "k:日", srs.State{Level: 2, DueAt: testNow.Add(-time.Hour), AddedAt: testNow, Reps: 2}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	due, err := s.Due(ctx, testNow, 10)
	if err != nil {
		t.Fatalf("Due: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("Due returned %d cards, want 1", len(due))
	}
	if due[0].State.Level != 2 {
		t.Errorf("level = %d, want the updated 2", due[0].State.Level)
	}
	// When an item entered the queue is history, not something an update revises.
	if !due[0].State.AddedAt.Equal(added) {
		t.Errorf("addedAt = %v, want the original %v", due[0].State.AddedAt, added)
	}
}

func TestCountsAndIntroducedSince(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)

	yesterday := testNow.Add(-26 * time.Hour)
	if err := s.Save(ctx, "k:日", srs.State{Level: srs.MaxLevel, DueAt: testNow.Add(time.Hour), AddedAt: yesterday}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save(ctx, "k:本", srs.State{Level: 2, DueAt: testNow.Add(-time.Minute), Lapses: srs.LeechLapses, AddedAt: testNow}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save(ctx, "w:1", srs.State{Level: 1, DueAt: testNow.Add(-time.Minute), AddedAt: testNow}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	c, err := s.Counts(ctx, testNow)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if c.DictKanjiItems != 3 || c.DictWordItems != 2 {
		t.Errorf("dictionary totals = %d kanji, %d words; want 3 and 2", c.DictKanjiItems, c.DictWordItems)
	}
	if c.InProgress != 3 || c.KanjiInProgress != 2 || c.WordsInProgress != 1 {
		t.Errorf("in progress = %d (%d kanji, %d words), want 3 (2, 1)", c.InProgress, c.KanjiInProgress, c.WordsInProgress)
	}
	if c.Due != 2 {
		t.Errorf("due = %d, want 2", c.Due)
	}
	if c.Burned != 1 {
		t.Errorf("burned = %d, want 1", c.Burned)
	}
	if c.Leeches != 1 {
		t.Errorf("leeches = %d, want 1", c.Leeches)
	}
	if c.ByLevel[2] != 1 || c.ByLevel[srs.MaxLevel] != 1 {
		t.Errorf("byLevel = %v, want one item at level 2 and one at %d", c.ByLevel, srs.MaxLevel)
	}

	// Only today's two introductions count against a daily budget.
	kanji, words, err := s.IntroducedSince(ctx, testNow.Add(-time.Hour))
	if err != nil {
		t.Fatalf("IntroducedSince: %v", err)
	}
	if kanji != 1 || words != 1 {
		t.Errorf("IntroducedSince = %d kanji, %d words; want 1 and 1", kanji, words)
	}
}

// A rebuild can retire an item, merging it into another. Its progress row is
// kept but must not count, or the banner would promise cards that never come.
func TestCountsIgnoreRetiredItems(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)

	learn(t, s, "日", 2)
	if err := s.Save(ctx, "w:999999", srs.State{Level: 1, DueAt: testNow.Add(-time.Minute), AddedAt: testNow}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save(ctx, "w:999998", srs.State{Level: 1, DueAt: testNow.Add(time.Minute), AddedAt: testNow}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	c, err := s.Counts(ctx, testNow)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if c.InProgress != 1 || c.WordsInProgress != 0 || c.Due != 0 || c.ByLevel[1] != 0 {
		t.Errorf("counts = %+v, want only k:日 in progress and nothing due", c)
	}

	next, ok, err := s.NextDue(ctx, testNow)
	if err != nil {
		t.Fatalf("NextDue: %v", err)
	}
	if !ok || !next.Equal(testNow.Add(time.Hour)) {
		t.Errorf("NextDue = %v (%v), want k:日's %v", next, ok, testNow.Add(time.Hour))
	}
}

func TestNextDue(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)

	if _, ok, err := s.NextDue(ctx, testNow); err != nil {
		t.Fatalf("NextDue: %v", err)
	} else if ok {
		t.Error("NextDue reported a due time with an empty queue")
	}

	soon := testNow.Add(90 * time.Minute)
	if err := s.Save(ctx, "k:日", srs.State{Level: 2, DueAt: soon, AddedAt: testNow}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save(ctx, "k:本", srs.State{Level: 5, DueAt: testNow.Add(72 * time.Hour), AddedAt: testNow}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, ok, err := s.NextDue(ctx, testNow)
	if err != nil {
		t.Fatalf("NextDue: %v", err)
	}
	if !ok || !got.Equal(soon) {
		t.Errorf("NextDue = %v (%v), want %v", got, ok, soon)
	}
}

func TestAnswerLogAndSettings(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)

	if _, err := s.LogAnswer(ctx, "k:日", "nichi", true, 0, 1, testNow, 1400*time.Millisecond); err != nil {
		t.Fatalf("LogAnswer: %v", err)
	}

	var given string
	var correct, before, after int
	var ms int64
	err := s.DB().QueryRow(`SELECT given, correct, level_before, level_after, elapsed_ms FROM answer_log`).
		Scan(&given, &correct, &before, &after, &ms)
	if err != nil {
		t.Fatalf("read answer_log: %v", err)
	}
	if given != "nichi" || correct != 1 || before != 0 || after != 1 || ms != 1400 {
		t.Errorf("logged %q correct=%d %d->%d %dms; want nichi 1 0->1 1400ms", given, correct, before, after, ms)
	}

	if v, err := s.Setting(ctx, "new-kanji", "3"); err != nil || v != "3" {
		t.Errorf("Setting default = %q, %v; want 3, nil", v, err)
	}
	if err := s.SetSetting(ctx, "new-kanji", "7"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := s.SetSetting(ctx, "new-kanji", "9"); err != nil {
		t.Fatalf("SetSetting overwrite: %v", err)
	}
	if v, err := s.Setting(ctx, "new-kanji", "3"); err != nil || v != "9" {
		t.Errorf("Setting = %q, %v; want 9, nil", v, err)
	}
}

func TestLookupAndCompoundsFor(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)

	// 日 exists only as a kanji here, 日本 only as a word.
	if got, err := s.Lookup(ctx, "日", 10); err != nil {
		t.Fatalf("Lookup: %v", err)
	} else if len(got) != 1 || !got[0].IsKanji() {
		t.Errorf("Lookup(日) = %v, want a single kanji item", prompts(got))
	}

	got, err := s.CompoundsFor(ctx, '語', 10)
	if err != nil {
		t.Fatalf("CompoundsFor: %v", err)
	}
	if want := []string{"日本語"}; !equal(prompts(got), want) {
		t.Errorf("CompoundsFor(語) = %v, want %v", prompts(got), want)
	}

	if got, err = s.CompoundsFor(ctx, '日', 10); err != nil {
		t.Fatalf("CompoundsFor: %v", err)
	} else if want := []string{"日本", "日本語"}; !equal(prompts(got), want) {
		t.Errorf("CompoundsFor(日) = %v, want %v", prompts(got), want)
	}
}

func TestMeta(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)

	// An absent key is not an error; the dictionary may predate the key.
	if v, err := s.Meta(ctx, "built_at"); err != nil || v != "" {
		t.Errorf("Meta(built_at) = %q, %v; want empty, nil", v, err)
	}
}

func cards(cs []Card) []Item {
	out := make([]Item, len(cs))
	for i, c := range cs {
		out[i] = c.Item
	}
	return out
}

func TestActivitySummarisesTheAnswerLog(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)

	// Two answers today, one yesterday, one three weeks ago.
	log := []struct {
		at      time.Time
		correct bool
	}{
		{testNow, true},
		{testNow.Add(-2 * time.Hour), false},
		{testNow.Add(-26 * time.Hour), true},
		{testNow.Add(-21 * 24 * time.Hour), true},
	}
	for i, l := range log {
		if _, err := s.LogAnswer(ctx, "k:日", "nichi", l.correct, 1, 2, l.at, 0); err != nil {
			t.Fatalf("LogAnswer %d: %v", i, err)
		}
	}

	week, err := s.Activity(ctx, testNow.AddDate(0, 0, -7))
	if err != nil {
		t.Fatalf("Activity: %v", err)
	}
	if week.Answered != 3 {
		t.Errorf("answered = %d, want 3 (the three within the week)", week.Answered)
	}
	if week.Correct != 2 {
		t.Errorf("correct = %d, want 2", week.Correct)
	}
	if week.Days != 2 {
		t.Errorf("days = %d, want 2 distinct days", week.Days)
	}
	if got := week.Accuracy(); got < 0.66 || got > 0.67 {
		t.Errorf("accuracy = %v, want about 0.667", got)
	}

	// An empty period must report nothing rather than divide by zero.
	empty, err := s.Activity(ctx, testNow.Add(time.Hour))
	if err != nil {
		t.Fatalf("Activity: %v", err)
	}
	if empty.Answered != 0 || empty.Days != 0 || empty.Accuracy() != 0 {
		t.Errorf("empty period = %+v, want zeroes", empty)
	}
}

func TestStreakCountsConsecutiveDays(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)
	// Noon local, so no answer sits near a midnight the zone could move.
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.Local)
	day := func(n int) time.Time { return now.AddDate(0, 0, -n) }

	if days, today, err := s.Streak(ctx, now); err != nil || days != 0 || today {
		t.Fatalf("empty log: %d days, today %v, err %v; want 0, false", days, today, err)
	}

	// Yesterday and the two days before, then a gap, then more.
	for _, n := range []int{1, 2, 3, 5, 6} {
		if _, err := s.LogAnswer(ctx, "k:日", "nichi", true, 1, 2, day(n), 0); err != nil {
			t.Fatalf("LogAnswer: %v", err)
		}
	}
	days, today, err := s.Streak(ctx, now)
	if err != nil || days != 3 || today {
		t.Errorf("nothing yet today: %d days, today %v, err %v; want 3, false", days, today, err)
	}

	// Two answers today extend it by one day, not two.
	for range 2 {
		if _, err := s.LogAnswer(ctx, "k:日", "nichi", true, 1, 2, now, 0); err != nil {
			t.Fatalf("LogAnswer: %v", err)
		}
	}
	days, today, err = s.Streak(ctx, now)
	if err != nil || days != 4 || !today {
		t.Errorf("answered today: %d days, today %v, err %v; want 4, true", days, today, err)
	}

	// Two days on, the run is over.
	if days, _, _ := s.Streak(ctx, now.AddDate(0, 0, 2)); days != 0 {
		t.Errorf("after a missed day: %d days, want 0", days)
	}
}

func TestInsights(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)
	sched := srs.Default()

	// Forecast: one item due in an hour, one in three days, one in a fortnight,
	// and one already due, which is not "coming".
	for key, in := range map[string]time.Duration{
		"k:日": time.Hour, "k:本": 72 * time.Hour, "k:語": 14 * 24 * time.Hour, "w:1": -time.Hour,
	} {
		st := sched.Introduce(testNow)
		st.DueAt = testNow.Add(in)
		if err := s.Save(ctx, key, st); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	logs := []struct {
		key     string
		level   int
		correct bool
		ms      int64
		ago     time.Duration
	}{
		// 本 is right but slow every time; 日 is quick; 語 was slow once.
		{"k:本", 3, true, 12000, time.Hour},
		{"k:本", 3, true, 9000, 2 * time.Hour},
		{"k:本", 3, false, 3000, 3 * time.Hour},
		{"k:日", 3, true, 1500, time.Hour},
		{"k:日", 3, true, 1200, 2 * time.Hour},
		{"k:語", 5, true, 30000, time.Hour},
		// A reveal has no time, and an answer outside the window is ignored.
		{"k:語", 5, false, 0, time.Hour},
		{"k:日", 5, false, 1000, 40 * 24 * time.Hour},
	}
	for _, l := range logs {
		if _, err := s.LogAnswer(ctx, l.key, "x", l.correct, l.level, l.level, testNow.Add(-l.ago),
			time.Duration(l.ms)*time.Millisecond); err != nil {
			t.Fatalf("LogAnswer: %v", err)
		}
	}

	in, err := s.Insights(ctx, testNow)
	if err != nil {
		t.Fatalf("Insights: %v", err)
	}
	if in.Coming.Day != 1 || in.Coming.Week != 2 || in.Coming.Month != 3 {
		t.Errorf("coming = %+v, want 1 within a day, 2 a week, 3 a month", in.Coming)
	}
	if got := in.ByLevel[3]; got.Answered != 5 || got.Correct != 4 {
		t.Errorf("level 3 = %+v, want 4 of 5", got)
	}
	if got := in.ByLevel[5]; got.Answered != 2 || got.Correct != 1 {
		t.Errorf("level 5 = %+v, want 1 of 2: the old answer is outside the window", got)
	}

	// Only 本 has enough correct answers and is slow enough to list.
	if len(in.Slow) != 1 || in.Slow[0].Item.Prompt != "本" {
		t.Fatalf("slow = %+v, want just 本", in.Slow)
	}
	if got := in.Slow[0].Median; got != 10500*time.Millisecond {
		t.Errorf("median = %v, want 10.5s", got)
	}
}

func TestLeechesListTheirWrongAnswers(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)
	sched := srs.Default()

	leech := sched.Introduce(testNow)
	leech.Level, leech.Lapses = 2, srs.LeechLapses+1
	worse := leech
	worse.Lapses = srs.LeechLapses + 3
	fine := leech
	fine.Lapses = 1
	for key, st := range map[string]srs.State{"k:本": leech, "w:1": worse, "k:語": fine} {
		if err := s.Save(ctx, key, st); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	// Romaji and kana for the same wrong reading are one answer; a reveal is
	// not an answer at all.
	for _, given := range []string{"moto", "もと", "moto", "hon", "", "ほn"} {
		if _, err := s.LogAnswer(ctx, "k:本", given, false, 2, 1, testNow, 0); err != nil {
			t.Fatalf("LogAnswer: %v", err)
		}
	}

	got, err := s.Leeches(ctx)
	if err != nil {
		t.Fatalf("Leeches: %v", err)
	}
	if len(got) != 2 || got[0].Card.Key != "w:1" || got[1].Card.Key != "k:本" {
		t.Fatalf("leeches = %v, want w:1 then k:本", leechKeys(got))
	}
	misses := got[1].Wrong
	if len(misses) != 2 || misses[0] != (Miss{"もと", 3}) || misses[1] != (Miss{"ほん", 2}) {
		t.Errorf("misses = %+v, want もと ×3 then ほん ×2", misses)
	}
}

func leechKeys(ls []Leech) []string {
	var out []string
	for _, l := range ls {
		out = append(out, l.Card.Key)
	}
	return out
}

func TestKnownWithKanji(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)
	sched := srs.Default()

	known := sched.Introduce(testNow)
	known.Level = 4
	shaky := known
	shaky.Level = 1
	// 日本 is known, 日本語 is not yet.
	if err := s.Save(ctx, "w:1", known); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, "w:2", shaky); err != nil {
		t.Fatal(err)
	}

	items, err := s.Lookup(ctx, "本", 1)
	if err != nil || len(items) == 0 {
		t.Fatalf("Lookup 本: %v %v", items, err)
	}
	got, err := s.KnownWithKanji(ctx, items[0], 5)
	if err != nil {
		t.Fatalf("KnownWithKanji: %v", err)
	}
	if len(got) != 1 || got[0].Prompt != "日本" {
		t.Errorf("known words with 本 = %v, want just 日本", got)
	}

	// From a word, through its kanji, never back to itself.
	words, err := s.Lookup(ctx, "日本語", 1)
	if err != nil || len(words) == 0 {
		t.Fatalf("Lookup 日本語: %v %v", words, err)
	}
	got, err = s.KnownWithKanji(ctx, words[0], 5)
	if err != nil {
		t.Fatalf("KnownWithKanji: %v", err)
	}
	if len(got) != 1 || got[0].Prompt != "日本" {
		t.Errorf("known words sharing a kanji with 日本語 = %v, want just 日本", got)
	}
}
