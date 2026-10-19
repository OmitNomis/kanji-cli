package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/OmitNomis/cli-kanji/internal/srs"
)

// /leeches lists, /leeches review drills, and the list shows what was typed
// instead of the reading.
func TestLeechesListAndReview(t *testing.T) {
	ctx := context.Background()
	b, rec := harness(t)

	b.handle(ctx, update(mine, "/leeches"))
	if last := rec.messages[len(rec.messages)-1].text; !strings.Contains(last, "no leeches") {
		t.Fatalf("/leeches with none: %q", last)
	}

	st := srs.Default().Introduce(time.Now())
	st.Level, st.Lapses, st.DueAt = 3, srs.LeechLapses, time.Now().AddDate(0, 1, 0)
	if err := b.store.Save(ctx, "k:日", st); err != nil {
		t.Fatal(err)
	}
	if _, err := b.store.LogAnswer(ctx, "k:日", "hon", false, 4, 3, time.Now(), 0); err != nil {
		t.Fatal(err)
	}

	b.handle(ctx, update(mine, "/leeches"))
	last := rec.messages[len(rec.messages)-1].text
	for _, want := range []string{"1 leech", "日", "ほん", "/leeches review"} {
		if !strings.Contains(last, want) {
			t.Errorf("/leeches is missing %q:\n%s", want, last)
		}
	}

	// 日 is not due for a month, but a leech session asks it anyway.
	b.handle(ctx, update(mine, "/leeches review"))
	last = rec.messages[len(rec.messages)-1].text
	if !strings.Contains(last, "leeches") || !strings.Contains(last, "日") {
		t.Errorf("/leeches review did not open on 日:\n%s", last)
	}
}
