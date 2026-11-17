package store

import (
	"context"
	"testing"
)

// withJLPT gives the fixture's kanji old JLPT levels that cut across their
// school grades: 語 is grade 2 but the easiest level, and 日 is outside a
// level-2 floor altogether.
func withJLPT(t *testing.T, s *Store) {
	t.Helper()
	for lit, lvl := range map[string]int{"日": 1, "本": 2, "語": 4} {
		exec(t, s.DB(), `UPDATE `+DictAlias+`.kanji SET jlpt = ? WHERE literal = ?`, lvl, lit)
	}
}

func TestParseJLPT(t *testing.T) {
	for in, want := range map[string]string{"N2": "N2", "n2": "N2", "2": "N2", " N5 ": "N5", "off": "", "": ""} {
		got, err := ParseJLPT(in)
		if err != nil || got != want {
			t.Errorf("ParseJLPT(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"N6", "N0", "x"} {
		if _, err := ParseJLPT(bad); err == nil {
			t.Errorf("ParseJLPT(%q) should fail", bad)
		}
	}
}

func TestNextKanjiWithJLPTFloorFiltersAndPutsEasiestFirst(t *testing.T) {
	s := fixture(t)
	withJLPT(t, s)
	sel := DefaultSelection()
	sel.JLPTFloor = JLPTFloor("N2")

	got, err := s.NextKanji(context.Background(), sel, 10)
	if err != nil {
		t.Fatalf("NextKanji: %v", err)
	}
	// 語 (old level 4) before 本 (level 2) despite its later grade; 日 (level 1)
	// is not part of N2.
	if want := []string{"語", "本"}; !equal(prompts(got), want) {
		t.Errorf("NextKanji with N2 = %v, want %v", prompts(got), want)
	}

	sel.JLPTFloor = 0
	got, err = s.NextKanji(context.Background(), sel, 10)
	if err != nil {
		t.Fatalf("NextKanji: %v", err)
	}
	if want := []string{"日", "本", "語"}; !equal(prompts(got), want) {
		t.Errorf("NextKanji without a target = %v, want %v", prompts(got), want)
	}
}

func TestJLPTTargetPersistsAndCounts(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)
	withJLPT(t, s)
	learn(t, s, "本", 3)
	learn(t, s, "日", 3)

	if err := s.SetJLPTTarget(ctx, "N2"); err != nil {
		t.Fatalf("SetJLPTTarget: %v", err)
	}
	c, err := s.Counts(ctx, testNow)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if c.JLPTTarget != "N2" || c.TargetKanji != 2 || c.TargetKanjiInProgress != 1 {
		t.Errorf("target = %q %d/%d, want N2 1/2", c.JLPTTarget, c.TargetKanjiInProgress, c.TargetKanji)
	}

	if err := s.SetJLPTTarget(ctx, ""); err != nil {
		t.Fatalf("clear target: %v", err)
	}
	c, err = s.Counts(ctx, testNow)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if c.JLPTTarget != "" || c.TargetKanji != 0 {
		t.Errorf("after clearing, target = %q with %d kanji, want none", c.JLPTTarget, c.TargetKanji)
	}
}
