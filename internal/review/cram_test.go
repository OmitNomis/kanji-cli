package review

import (
	"context"
	"sort"
	"testing"
	"time"
)

// Cram exists for the state the queue is in before any of it is true: someone
// who already reads much of the list wants it in the scheduler now, not five
// characters a day with four hours between sittings.
func TestCramIgnoresDueTimesAndTheDailyBudget(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)

	cfg := testConfig()
	cfg.NewKanji = 1

	sess, err := Begin(ctx, s, exact, cfg, now)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if sess.Total() != 1 {
		t.Fatalf("first session had %d cards, want 1", sess.Total())
	}
	if _, err := sess.Answer(ctx, "nichi", time.Second, now); err != nil {
		t.Fatalf("Answer: %v", err)
	}

	// A second session the same minute has nothing to offer: today's single new
	// kanji is spent, and 日 does not come round for four hours.
	again, err := Begin(ctx, s, exact, cfg, now)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if again.Total() != 0 {
		t.Fatalf("a second ordinary session had %d cards, want none", again.Total())
	}

	cfg.Cram = true
	cram, err := Begin(ctx, s, exact, cfg, now)
	if err != nil {
		t.Fatalf("Begin cram: %v", err)
	}

	got := promptsOf(queueOf(cram))
	sort.Strings(got)
	if len(got) != 2 || got[0] != "日" || got[1] != "本" {
		t.Fatalf("cram queue = %v, want 日 (not due) and 本 (over budget)", got)
	}
}

// The clock is skipped, not the ladder: a crammed answer climbs and is given a
// real due time, which is what leaves the queue usable by ordinary review
// afterwards.
func TestCramStillGradesAndSchedules(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)

	cfg := testConfig()
	cfg.NewKanji = 1
	sess, err := Begin(ctx, s, exact, cfg, now)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := sess.Answer(ctx, "nichi", time.Second, now); err != nil {
		t.Fatalf("Answer: %v", err)
	}

	cfg.Cram = true
	cfg.NewKanji = 0
	cram, err := Begin(ctx, s, exact, cfg, now)
	if err != nil {
		t.Fatalf("Begin cram: %v", err)
	}
	card, ok := cram.Current()
	if !ok || card.Key != "k:日" {
		t.Fatalf("cram session is on %q, want k:日", card.Key)
	}
	if card.State.Level != 1 {
		t.Fatalf("card came back at level %d, want the level it was left at (1)", card.State.Level)
	}

	res, err := cram.Answer(ctx, "nichi", time.Second, now)
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if !res.Correct || res.After.Level != 2 {
		t.Errorf("crammed answer gave correct=%v level %d, want true and 2", res.Correct, res.After.Level)
	}
	if want := now.Add(8 * time.Hour); !res.After.DueAt.Equal(want) {
		t.Errorf("due at %s, want %s: cramming must not stop setting real due times", res.After.DueAt, want)
	}
}
