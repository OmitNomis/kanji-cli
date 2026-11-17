package review

import (
	"context"
	"testing"
	"time"

	"github.com/OmitNomis/cli-kanji/internal/srs"
)

// Reviewing ahead reaches as far as the window and no further: a card four
// hours out is asked, one three days out waits, which is what keeps it a
// session of the usual size rather than a cram.
func TestAheadAsksWhatIsComingDueWithinTheWindow(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)

	soon := srs.State{Level: 1, DueAt: now.Add(4 * time.Hour), AddedAt: now}
	later := srs.State{Level: 4, DueAt: now.Add(72 * time.Hour), AddedAt: now}
	if err := s.Save(ctx, "k:日", soon); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save(ctx, "k:本", later); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cfg := testConfig()
	cfg.NewKanji = 0

	normal, err := Begin(ctx, s, exact, cfg, now)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if normal.Total() != 0 {
		t.Fatalf("a normal session asked %v, want nothing yet", promptsOf(queueOf(normal)))
	}

	cfg.Pace = Ahead
	ahead, err := Begin(ctx, s, exact, cfg, now)
	if err != nil {
		t.Fatalf("Begin ahead: %v", err)
	}
	if got := promptsOf(queueOf(ahead)); !equalStrings(got, []string{"日"}) {
		t.Errorf("ahead asked %v, want 日 (due in 4h) and not 本 (due in 3d)", got)
	}
}

func TestAheadCapsTheReviews(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)

	for i, key := range []string{"k:日", "k:本", "k:語"} {
		st := srs.State{Level: 1, DueAt: now.Add(time.Duration(i+1) * time.Hour), AddedAt: now}
		if err := s.Save(ctx, key, st); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	cfg := testConfig()
	cfg.NewKanji = 0
	cfg.Pace = Ahead
	cfg.AheadReviews = 2
	sess, err := Begin(ctx, s, exact, cfg, now)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if got := promptsOf(queueOf(sess)); !equalStrings(got, []string{"日", "本"}) {
		t.Errorf("ahead asked %v, want the two soonest, 日 and 本", got)
	}
}

// A new card asked bare and read right is already known, and skips the bottom
// of the ladder.
func TestAProbeReadRightIsPlaced(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)

	cfg := testConfig()
	cfg.NewKanji = 1
	cfg.Pace = Ahead
	sess, err := Begin(ctx, s, exact, cfg, now)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	card, _ := sess.Current()
	if !card.Probe || card.HintShown() {
		t.Fatalf("new card in an ahead session: probe=%v hint=%v, want a probe with no hint", card.Probe, card.HintShown())
	}

	res, err := sess.Answer(ctx, "nichi", time.Second, now)
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if !res.Placed || res.After.Level != srs.PlacementLevel {
		t.Errorf("placed=%v level %d, want placed at %d", res.Placed, res.After.Level, srs.PlacementLevel)
	}
	if want := now.Add(srs.Interval(srs.PlacementLevel)); !res.After.DueAt.Equal(want) {
		t.Errorf("due %s, want %s", res.After.DueAt, want)
	}
}

// A probe that misses is taught the ordinary way: graded wrong, and asked
// again later in the session with its reading on screen.
func TestAProbeMissedIsTaughtAsUsual(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)

	cfg := testConfig()
	cfg.NewKanji = 1
	cfg.Pace = Ahead
	sess, err := Begin(ctx, s, exact, cfg, now)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	res, err := sess.Answer(ctx, "", 0, now)
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if res.Placed || res.After.Level != 0 || !res.Requeued {
		t.Fatalf("placed=%v level %d requeued=%v, want an ordinary miss", res.Placed, res.After.Level, res.Requeued)
	}

	again, ok := sess.Current()
	if !ok || again.Key != "k:日" {
		t.Fatalf("the missed probe was not asked again")
	}
	if again.Probe || !again.HintShown() {
		t.Errorf("requeued card: probe=%v hint=%v, want an ordinary card with its reading", again.Probe, again.HintShown())
	}
}

// A normal session teaches new cards with the reading, as it always has.
func TestNormalSessionsDoNotProbe(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)

	cfg := testConfig()
	cfg.NewKanji = 1
	sess, err := Begin(ctx, s, exact, cfg, now)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	card, _ := sess.Current()
	if card.Probe || !card.HintShown() {
		t.Errorf("normal new card: probe=%v hint=%v, want the reading shown", card.Probe, card.HintShown())
	}
	res, _ := sess.Answer(ctx, "nichi", time.Second, now)
	if res.Placed || res.After.Level != 1 {
		t.Errorf("normal new card answered: placed=%v level %d, want level 1", res.Placed, res.After.Level)
	}
}

// A probe read right costs nothing to learn, so the next new item of the same
// kind is asked straight after it, and the chain runs until something is not
// known.
func TestAProbeReadRightBringsInTheNext(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)

	cfg := testConfig()
	cfg.NewKanji = 1
	cfg.Pace = Ahead
	sess, err := Begin(ctx, s, exact, cfg, now)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	for _, step := range []struct{ prompt, answer string }{{"日", "nichi"}, {"本", "hon"}} {
		card, ok := sess.Current()
		if !ok || card.Prompt != step.prompt || !card.Probe {
			t.Fatalf("current = %q (probe %v), want a probe for %s; queue %v", card.Prompt, card.Probe, step.prompt, promptsOf(queueOf(sess)))
		}
		if _, err := sess.Answer(ctx, step.answer, time.Second, now); err != nil {
			t.Fatalf("Answer %s: %v", step.prompt, err)
		}
	}

	card, ok := sess.Current()
	if !ok || card.Prompt != "語" || !card.Probe {
		t.Fatalf("after two known kanji, current = %q, want a probe for 語", card.Prompt)
	}
	if _, err := sess.Answer(ctx, "", 0, now); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	// The miss is requeued and nothing new is added: the fixture has no more
	// kanji, and a miss never refills anyway.
	if got := promptsOf(queueOf(sess)); !equalStrings(got, []string{"日", "本", "語", "語"}) {
		t.Errorf("queue = %v, want [日 本 語 語]", got)
	}
}

// A probe missed uses up its slot like any new card: nothing replaces it.
func TestAProbeMissedDoesNotBringInTheNext(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)

	cfg := testConfig()
	cfg.NewKanji = 1
	cfg.Pace = Ahead
	sess, err := Begin(ctx, s, exact, cfg, now)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := sess.Answer(ctx, "", 0, now); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	for _, c := range queueOf(sess) {
		if c.Key != "k:日" {
			t.Fatalf("queue = %v, want only 日 and its relearn copy", promptsOf(queueOf(sess)))
		}
	}
}
