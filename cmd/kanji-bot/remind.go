package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/OmitNomis/cli-kanji/internal/review"
	"github.com/OmitNomis/cli-kanji/internal/telegram"
)

// remindEvery is how often the queue is looked at. A reminder that arrives a
// few minutes after a card came due loses nothing, and the check assembles a
// whole session, which is not free on the Atom.
const remindEvery = 5 * time.Minute

// reminder decides when to tell a chat that a session is waiting.
//
// It pings once per pile rather than once per card: after a ping it stays
// quiet until the waiting session drops back below the threshold, which only
// happens by reviewing. Otherwise every card coming due would be a
// notification, and the ten-minute relearn step alone would make the bot a
// pest. The armed state is persisted, so a restart -- every deploy is one --
// does not repeat a ping already sent.
type reminder struct {
	// at is the session size worth a ping. Zero turns reminders off.
	at int

	quiet quietHours
	next  time.Time
}

// remind checks each allowed chat, at most once per remindEvery.
func (b *bot) remind(ctx context.Context, now time.Time) {
	r := &b.reminder
	if r.at <= 0 || now.Before(r.next) {
		return
	}
	r.next = now.Add(remindEvery)

	for id := range b.allowed {
		if err := b.remindChat(ctx, id, now); err != nil {
			log.Printf("chat %d: reminder: %v", id, err)
		}
	}
}

func (b *bot) remindChat(ctx context.Context, id int64, now time.Time) error {
	// Someone in the middle of a session knows already.
	if b.chats[id] != nil {
		return nil
	}

	// Assembling the session is the only way to know its size exactly: due
	// reviews are a count, but what is left of the day's new items depends on
	// the gate. Begin writes nothing until a card is answered.
	sess, err := review.Begin(ctx, b.store, b.sched, b.cfg, now)
	if err != nil {
		return err
	}
	size := sess.Total()

	key := fmt.Sprintf("remind.%d.armed", id)
	armed, err := b.store.Setting(ctx, key, "1")
	if err != nil {
		return err
	}

	if size < b.reminder.at {
		if armed != "1" {
			return b.store.SetSetting(ctx, key, "1")
		}
		return nil
	}

	// Past the threshold. Overnight the ping waits rather than being dropped:
	// the pile is still there in the morning and the check runs again then.
	if armed != "1" || b.reminder.quiet.contains(now) {
		return nil
	}

	counts, err := b.store.Counts(ctx, now)
	if err != nil {
		return err
	}
	due := min(counts.Due, size)
	if err := b.send(ctx, id, telegram.Reminder(due, size-due)); err != nil {
		return err
	}
	return b.store.SetSetting(ctx, key, "0")
}

// quietHours is a span of local hours with no pings. from == to means none.
type quietHours struct{ from, to int }

// parseQuiet reads "23-8": from 23:00 up to 08:00, wrapping past midnight. An
// empty string means no quiet hours.
func parseQuiet(s string) (quietHours, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return quietHours{}, nil
	}
	lo, hi, ok := strings.Cut(s, "-")
	from, err1 := strconv.Atoi(strings.TrimSpace(lo))
	to, err2 := strconv.Atoi(strings.TrimSpace(hi))
	if !ok || err1 != nil || err2 != nil || from < 0 || from > 23 || to < 0 || to > 23 {
		return quietHours{}, fmt.Errorf("bad quiet hours %q: want a range of hours such as 23-8", s)
	}
	return quietHours{from, to}, nil
}

func (q quietHours) contains(t time.Time) bool {
	h := t.Hour()
	switch {
	case q.from == q.to:
		return false
	case q.from < q.to:
		return h >= q.from && h < q.to
	default:
		return h >= q.from || h < q.to
	}
}
