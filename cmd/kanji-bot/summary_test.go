package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// at is today at the given local hour.
func at(hour int) time.Time {
	y, m, d := time.Now().Date()
	return time.Date(y, m, d, hour, 0, 0, 0, time.Local)
}

// The summary goes out once a day, from the set hour on, and says what the
// day came to.
func TestSummaryIsSentOnceADay(t *testing.T) {
	ctx := context.Background()
	b, rec := harness(t)
	b.summary = summary{at: 21}

	b.handle(ctx, update(mine, "/review"))
	b.handle(ctx, update(mine, "nichi"))
	sent := len(rec.messages)

	b.summarize(ctx, at(20))
	if len(rec.messages) != sent {
		t.Fatalf("summary sent before its hour: %v", rec.messages[sent:])
	}

	b.summarize(ctx, at(21))
	if len(rec.messages) != sent+1 {
		t.Fatalf("no summary at its hour")
	}
	msg := rec.messages[sent]
	if msg.chatID != mine {
		t.Errorf("summary went to %d, want %d", msg.chatID, mine)
	}
	for _, want := range []string{"today", "1 answer", "100% right", "1 kanji"} {
		if !strings.Contains(msg.text, want) {
			t.Errorf("summary is missing %q:\n%s", want, msg.text)
		}
	}

	// Later the same evening, even after a restart forgets the timer: no.
	b.summary = summary{at: 21}
	b.summarize(ctx, at(23))
	if len(rec.messages) != sent+1 {
		t.Errorf("summary sent twice in a day: %v", rec.messages[sent+1:])
	}

	// The next evening: yes.
	b.summarize(ctx, at(21).AddDate(0, 0, 1))
	if len(rec.messages) != sent+2 {
		t.Errorf("no summary the next day")
	}
}

func TestSummaryWaitsForTheSessionToEnd(t *testing.T) {
	ctx := context.Background()
	b, rec := harness(t)
	b.summary = summary{at: 21}

	b.handle(ctx, update(mine, "/review"))
	sent := len(rec.messages)
	b.summarize(ctx, at(22))
	if len(rec.messages) != sent {
		t.Errorf("summary interrupted a session: %v", rec.messages[sent:])
	}
}

func TestSummaryOfAnEmptyDay(t *testing.T) {
	ctx := context.Background()
	b, rec := harness(t)
	b.summary = summary{at: 21}

	b.summarize(ctx, at(22))
	if len(rec.messages) != 1 || !strings.Contains(rec.messages[0].text, "nothing reviewed today") {
		t.Errorf("empty day: %v", rec.messages)
	}
}
