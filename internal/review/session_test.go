package review

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/OmitNomis/cli-kanji/internal/srs"
	"github.com/OmitNomis/cli-kanji/internal/store"
)

var now = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

// exact is the deterministic scheduler, so due times in assertions are exact.
var exact srs.Scheduler

// fixture builds a dictionary of three kanji and two compounds and opens it
// against a fresh progress database.
//
// 日 and 本 are grade 1 and 語 is grade 2; 日本 needs the first two and 日本語
// needs all three, which is enough for the gate to matter.
func fixture(t *testing.T) *store.Store {
	t.Helper()

	dir := t.TempDir()
	dictPath := filepath.Join(dir, "dict.db")

	dict, err := store.OpenDict(context.Background(), dictPath)
	if err != nil {
		t.Fatalf("OpenDict: %v", err)
	}

	kanji := []struct {
		literal  string
		grade    int
		freq     int
		readings string
	}{
		{"日", 1, 1, "にち"},
		{"本", 1, 10, "ほん"},
		{"語", 2, 300, "ご"},
	}
	for _, k := range kanji {
		cp := int64([]rune(k.literal)[0])
		exec(t, dict.DB(), `INSERT INTO kanji (id, literal, grade, stroke_count) VALUES (?, ?, ?, 4)`,
			cp, k.literal, k.grade)
		exec(t, dict.DB(), `
			INSERT INTO item (key, kind, ref_id, prompt, answers, normalized, hint, gloss,
			                  grade, max_grade, kanji_count, common, sort_key)
			VALUES (?, 'kanji', ?, ?, ?, ?, ?, 'gloss', ?, ?, 1, 2, ?)`,
			"k:"+k.literal, cp, k.literal, k.readings, k.readings, k.readings,
			k.grade, k.grade, k.grade*100000+k.freq)
	}

	words := []struct {
		id       int64
		surface  string
		reading  string
		maxGrade int
		rank     int
	}{
		{1, "日本", "にほん", 1, 1},
		{2, "日本語", "にほんご", 2, 5},
	}
	for _, w := range words {
		runes := []rune(w.surface)
		exec(t, dict.DB(), `
			INSERT INTO word (id, surface, reading, glosses, common, freq_rank, kanji_count, max_grade)
			VALUES (?, ?, ?, 'gloss', 2, ?, ?, ?)`,
			w.id, w.surface, w.reading, w.rank, len(runes), w.maxGrade)
		for _, r := range runes {
			exec(t, dict.DB(), `INSERT INTO word_kanji (kanji_id, word_id) VALUES (?, ?)`, int64(r), w.id)
		}
		exec(t, dict.DB(), `
			INSERT INTO item (key, kind, ref_id, prompt, answers, normalized, hint, gloss,
			                  grade, max_grade, kanji_count, common, sort_key)
			VALUES (?, 'word', ?, ?, ?, ?, ?, 'gloss', 0, ?, ?, 2, ?)`,
			wordKey(w.id), w.id, w.surface, w.reading, w.reading, w.reading,
			w.maxGrade, len(runes), w.rank*1000)
	}

	if err := dict.Close(); err != nil {
		t.Fatalf("close dict: %v", err)
	}

	s, err := store.Open(context.Background(), dictPath, filepath.Join(dir, "progress.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func wordKey(id int64) string {
	if id == 1 {
		return "w:1"
	}
	return "w:2"
}

func exec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func testConfig() Config {
	cfg := DefaultConfig()
	cfg.NewKanji = 3
	cfg.NewWords = 0
	cfg.NewEvery = 1
	cfg.RelearnGap = 2
	return cfg
}

func TestBeginIntroducesKanjiInTeachingOrder(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)

	sess, err := Begin(ctx, s, exact, testConfig(), now)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if got := sess.Total(); got != 3 {
		t.Fatalf("queue size = %d, want 3", got)
	}

	for _, want := range []string{"日", "本", "語"} {
		card, ok := sess.Current()
		if !ok {
			t.Fatalf("queue ended early, expected %s", want)
		}
		if card.Prompt != want {
			t.Fatalf("card = %s, want %s", card.Prompt, want)
		}
		if !card.New {
			t.Errorf("%s should be marked new", want)
		}
		if !srs.ShowHint(card.State.Level) {
			t.Errorf("%s is new and should be hinted", want)
		}
		sess.Skip()
	}
	if _, ok := sess.Current(); ok {
		t.Error("queue should be exhausted")
	}
}

func TestDailyBudgetIsSharedAcrossSessions(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)
	cfg := testConfig()
	cfg.NewKanji = 2

	first, err := Begin(ctx, s, exact, cfg, now)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if first.Total() != 2 {
		t.Fatalf("first session queue = %d, want 2", first.Total())
	}
	// Answering is what records an introduction.
	for range 2 {
		if _, err := first.Answer(ctx, "wrong", 0, now); err != nil {
			t.Fatalf("Answer: %v", err)
		}
	}

	// Later the same day the budget is spent, so nothing new is offered even
	// though a third kanji is waiting.
	second, err := Begin(ctx, s, exact, cfg, now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	for _, card := range queueOf(second) {
		if card.New {
			t.Errorf("%s was introduced after the daily budget was spent", card.Prompt)
		}
	}

	// Tomorrow the budget resets.
	third, err := Begin(ctx, s, exact, cfg, now.Add(26*time.Hour))
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	var fresh int
	for _, card := range queueOf(third) {
		if card.New {
			fresh++
		}
	}
	if fresh != 1 {
		t.Errorf("tomorrow introduced %d new items, want the 1 remaining kanji", fresh)
	}
}

func TestCorrectAnswerAcceptsRomajiAndAdvances(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)

	sess, err := Begin(ctx, s, exact, testConfig(), now)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	// 日 reads にち; typed as romaji it must still register.
	res, err := sess.Answer(ctx, "nichi", 1200*time.Millisecond, now)
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if !res.Correct {
		t.Fatalf("answer %q was rejected", "nichi")
	}
	if res.Matched != "にち" {
		t.Errorf("matched = %q, want にち", res.Matched)
	}
	if res.Before.Level != 0 || res.After.Level != 1 {
		t.Errorf("level %d -> %d, want 0 -> 1", res.Before.Level, res.After.Level)
	}
	if res.Requeued {
		t.Error("a correct answer must not be requeued")
	}
	if sess.Answered != 1 || sess.Correct != 1 || sess.Incorrect != 0 {
		t.Errorf("session tallies = %d/%d/%d, want 1/1/0", sess.Answered, sess.Correct, sess.Incorrect)
	}
	if sess.Introduced.Kanji != 1 {
		t.Errorf("introduced kanji = %d, want 1", sess.Introduced.Kanji)
	}

	// The state must be on disk, not just in the session.
	due, err := s.Due(ctx, now.Add(5*time.Hour), 10)
	if err != nil {
		t.Fatalf("Due: %v", err)
	}
	var found bool
	for _, c := range due {
		if c.Key == "k:日" {
			found = true
			if c.State.Level != 1 || c.State.Correct != 1 {
				t.Errorf("persisted state = level %d, correct %d; want 1 and 1", c.State.Level, c.State.Correct)
			}
		}
	}
	if !found {
		t.Error("the answered card was not persisted")
	}
}

func TestWrongAnswerIsRequeuedWithinTheSession(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)

	sess, err := Begin(ctx, s, exact, testConfig(), now)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	before := sess.Total()

	// ほん is a real reading, just not of 日.
	res, err := sess.Answer(ctx, "hon", 0, now)
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if res.Correct {
		t.Fatal("ほん was accepted as a reading of 日")
	}
	if !res.Requeued {
		t.Error("a wrong answer should come back in the same session")
	}
	// The normalised form is reported so the reviewer is shown the kana they
	// actually typed, not the romaji they typed it as.
	if res.Normalized != "ほん" {
		t.Errorf("normalized = %q, want ほん", res.Normalized)
	}
	if sess.Total() != before+1 {
		t.Errorf("queue grew to %d, want %d", sess.Total(), before+1)
	}

	// RelearnGap is 2, so the card reappears two slots after the next one.
	q := queueOf(sess)
	if q[3].Prompt != "日" {
		t.Errorf("requeued card landed at %s, want 日 at index 3 (queue: %v)", q[3].Prompt, promptsOf(q))
	}
	// Coming back must not count as a second introduction.
	if q[3].New {
		t.Error("a requeued card is no longer new")
	}
}

func TestRequeueIsCapped(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)
	cfg := testConfig()
	cfg.NewKanji = 1
	cfg.MaxRelearns = 2

	sess, err := Begin(ctx, s, exact, cfg, now)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	// Get it wrong forever; the queue must stop growing after MaxRelearns.
	var requeues int
	for range 6 {
		if _, ok := sess.Current(); !ok {
			break
		}
		res, err := sess.Answer(ctx, "zzz", 0, now)
		if err != nil {
			t.Fatalf("Answer: %v", err)
		}
		if res.Requeued {
			requeues++
		}
	}
	if requeues != cfg.MaxRelearns {
		t.Errorf("card was requeued %d times, want the cap of %d", requeues, cfg.MaxRelearns)
	}
	if sess.Total() != 1+cfg.MaxRelearns {
		t.Errorf("queue size = %d, want %d", sess.Total(), 1+cfg.MaxRelearns)
	}
}

func TestMaxCardsCapsTheSession(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)
	cfg := testConfig()
	cfg.MaxCards = 2

	sess, err := Begin(ctx, s, exact, cfg, now)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if sess.Total() != 2 {
		t.Errorf("queue size = %d, want the cap of 2", sess.Total())
	}
}

func TestWordsAppearOnceTheirKanjiAreKnown(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)

	// Learn 日 and 本 but not 語.
	for _, k := range []string{"日", "本"} {
		if err := s.Save(ctx, "k:"+k, srs.State{Level: 3, DueAt: now.Add(48 * time.Hour), AddedAt: now.Add(-48 * time.Hour)}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	cfg := testConfig()
	cfg.NewKanji = 0
	cfg.NewWords = 5

	sess, err := Begin(ctx, s, exact, cfg, now)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if got := promptsOf(queueOf(sess)); len(got) != 1 || got[0] != "日本" {
		t.Fatalf("queue = %v, want just 日本 (日本語 needs 語)", got)
	}

	// The compound is a word, not a kanji, and the reviewer has to be able to
	// tell: 本 alone would accept ほん or もと, 日本 accepts only にほん.
	card, _ := sess.Current()
	if card.IsKanji() {
		t.Error("日本 should be a word item")
	}
	if res, err := sess.Answer(ctx, "nihon", 0, now); err != nil {
		t.Fatalf("Answer: %v", err)
	} else if !res.Correct {
		t.Error("nihon should be accepted for 日本")
	}
	if sess.Introduced.Words != 1 {
		t.Errorf("introduced words = %d, want 1", sess.Introduced.Words)
	}
}

func TestInterleavePlacesNewItemsAtTheGivenRate(t *testing.T) {
	due := cardsNamed("d1", "d2", "d3", "d4", "d5", "d6")
	fresh := cardsNamed("n1", "n2")

	got := promptsOf(interleave(due, fresh, 3))
	want := []string{"d1", "d2", "n1", "d3", "d4", "n2", "d5", "d6"}
	if !equalStrings(got, want) {
		t.Errorf("interleave = %v, want %v", got, want)
	}

	// With nothing due, the new items are the whole session.
	if got := promptsOf(interleave(nil, fresh, 4)); !equalStrings(got, []string{"n1", "n2"}) {
		t.Errorf("interleave with no reviews = %v, want [n1 n2]", got)
	}
	// With nothing new, the reviews are untouched and none are lost.
	if got := promptsOf(interleave(due, nil, 4)); !equalStrings(got, promptsOf(due)) {
		t.Errorf("interleave with no new items = %v, want %v", got, promptsOf(due))
	}
	// A nonsensical rate must not divide by zero or drop cards.
	if got := interleave(due, fresh, 0); len(got) != len(due)+len(fresh) {
		t.Errorf("interleave with rate 0 produced %d cards, want %d", len(got), len(due)+len(fresh))
	}
}

func TestAnswerOnAnExhaustedSessionErrors(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)
	cfg := testConfig()
	cfg.NewKanji = 1

	sess, err := Begin(ctx, s, exact, cfg, now)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := sess.Answer(ctx, "nichi", 0, now); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if _, err := sess.Answer(ctx, "nichi", 0, now); err == nil {
		t.Error("answering an exhausted session should fail rather than panic")
	}
}

func TestAccuracy(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)

	sess, err := Begin(ctx, s, exact, testConfig(), now)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if got := sess.Accuracy(); got != 0 {
		t.Errorf("accuracy before any answer = %v, want 0", got)
	}

	if _, err := sess.Answer(ctx, "nichi", 0, now); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if _, err := sess.Answer(ctx, "zzz", 0, now); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if got := sess.Accuracy(); got != 0.5 {
		t.Errorf("accuracy = %v, want 0.5", got)
	}
}

func queueOf(s *Session) []store.Card { return s.queue }

func cardsNamed(names ...string) []store.Card {
	out := make([]store.Card, len(names))
	for i, n := range names {
		out[i] = store.Card{Item: store.Item{Key: n, Prompt: n}}
	}
	return out
}

func promptsOf(cards []store.Card) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = c.Prompt
	}
	return out
}

func equalStrings(a, b []string) bool {
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
