package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The bot pings once per pile: not again while the pile stands, and again only
// after reviewing has brought it down and it has built back up. The harness's
// one-kanji dictionary makes a session of one card.
func TestReminderPingsOncePerPile(t *testing.T) {
	ctx := context.Background()
	b, rec := harness(t)
	b.reminder = reminder{at: 1}
	t0 := time.Now()

	b.remind(ctx, t0)
	if len(rec.messages) != 1 || !strings.Contains(rec.messages[0].text, "1 card waiting") {
		t.Fatalf("no ping for a waiting session: %v", rec.messages)
	}
	if rec.messages[0].chatID != mine {
		t.Errorf("pinged %d, want %d", rec.messages[0].chatID, mine)
	}

	b.remind(ctx, t0.Add(remindEvery))
	if len(rec.messages) != 1 {
		t.Fatalf("pinged again for the same pile: %v", rec.messages[1:])
	}

	// Reviewing clears the pile, which re-arms the reminder.
	b.handle(ctx, update(mine, "/review"))
	b.handle(ctx, update(mine, "nichi"))
	sentBefore := len(rec.messages)
	b.remind(ctx, t0.Add(2*remindEvery))
	if len(rec.messages) != sentBefore {
		t.Fatalf("pinged with nothing waiting: %v", rec.messages[sentBefore:])
	}

	// Four hours on, the card has come round again.
	b.remind(ctx, t0.Add(5*time.Hour))
	if len(rec.messages) != sentBefore+1 || !strings.Contains(rec.messages[sentBefore].text, "1 due") {
		t.Errorf("no ping once the card came due again: %v", rec.messages[sentBefore:])
	}
}

func TestReminderStaysQuietDuringASession(t *testing.T) {
	ctx := context.Background()
	b, rec := harness(t)
	b.reminder = reminder{at: 1}

	b.handle(ctx, update(mine, "/review"))
	sent := len(rec.messages)
	b.remind(ctx, time.Now())
	if len(rec.messages) != sent {
		t.Errorf("pinged someone already reviewing: %v", rec.messages[sent:])
	}
}

func TestReminderWaitsOutQuietHours(t *testing.T) {
	ctx := context.Background()
	b, rec := harness(t)
	b.reminder = reminder{at: 1, quiet: quietHours{23, 8}}

	day := time.Now()
	night := time.Date(day.Year(), day.Month(), day.Day(), 2, 0, 0, 0, time.Local)

	b.remind(ctx, night)
	if len(rec.messages) != 0 {
		t.Fatalf("pinged at 2am: %v", rec.messages)
	}
	b.remind(ctx, night.Add(7*time.Hour))
	if len(rec.messages) != 1 {
		t.Errorf("the held-over ping was not sent in the morning: %v", rec.messages)
	}
}

func TestReminderOffAtZero(t *testing.T) {
	b, rec := harness(t)
	b.remind(context.Background(), time.Now())
	if len(rec.messages) != 0 {
		t.Errorf("pinged with reminders off: %v", rec.messages)
	}
}

func TestParseQuiet(t *testing.T) {
	q, err := parseQuiet("23-8")
	if err != nil {
		t.Fatalf("parseQuiet: %v", err)
	}
	at := func(h int) time.Time { return time.Date(2026, 1, 1, h, 30, 0, 0, time.UTC) }
	for h, want := range map[int]bool{22: false, 23: true, 0: true, 7: true, 8: false, 12: false} {
		if got := q.contains(at(h)); got != want {
			t.Errorf("23-8 contains %d:30 = %v, want %v", h, got, want)
		}
	}

	if q, err := parseQuiet(""); err != nil || q.contains(at(3)) {
		t.Errorf("empty quiet hours = %v, %v; want none", q, err)
	}
	for _, bad := range []string{"23", "a-b", "25-3", "-1-4"} {
		if _, err := parseQuiet(bad); err == nil {
			t.Errorf("parseQuiet(%q) accepted a bad range", bad)
		}
	}
}
