package review

import (
	"context"
	"testing"
	"time"

	"github.com/OmitNomis/cli-kanji/internal/srs"
)

// A leech session asks the leeches and nothing else: not what is due, and
// nothing new.
func TestLeechSessionAsksOnlyLeeches(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)

	// 日 is a leech and not due for a month; 本 is due but fine.
	leech := exact.Introduce(now)
	leech.Level, leech.Lapses, leech.DueAt = 2, srs.LeechLapses, now.AddDate(0, 1, 0)
	if err := s.Save(ctx, "k:日", leech); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, "k:本", exact.Introduce(now.Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	cfg.Pace = Leeches
	sess, err := Begin(ctx, s, exact, cfg, now)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if got := promptsOf(queueOf(sess)); len(got) != 1 || got[0] != "日" {
		t.Errorf("leech queue = %v, want just 日", got)
	}
}
