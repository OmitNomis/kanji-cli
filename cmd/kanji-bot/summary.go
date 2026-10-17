package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/OmitNomis/cli-kanji/internal/telegram"
)

// summary sends each chat an account of its day, once, at a set local hour.
//
// The day it last went out is persisted per chat, so a restart after the hour
// -- every deploy is one -- does not send it twice, and a bot that was down at
// the hour sends it when it comes back, as long as that is still the same day.
// Quiet hours do not apply: the hour was chosen, which is all the consent a
// message needs.
type summary struct {
	// at is the local hour to send from. Zero turns summaries off: midnight is
	// the one hour with no day behind it to sum up.
	at int

	next time.Time
}

// summarize checks each allowed chat, at most once per remindEvery.
func (b *bot) summarize(ctx context.Context, now time.Time) {
	s := &b.summary
	if s.at <= 0 || now.Hour() < s.at || now.Before(s.next) {
		return
	}
	s.next = now.Add(remindEvery)

	for id := range b.allowed {
		if err := b.summarizeChat(ctx, id, now); err != nil {
			log.Printf("chat %d: summary: %v", id, err)
		}
	}
}

func (b *bot) summarizeChat(ctx context.Context, id int64, now time.Time) error {
	// Mid-session the day is not over yet; the summary waits for the
	// session to end, or to be forgotten as idle.
	if b.chats[id] != nil {
		return nil
	}

	key := fmt.Sprintf("summary.%d.day", id)
	today := now.Format(time.DateOnly)
	last, err := b.store.Setting(ctx, key, "")
	if err != nil || last == today {
		return err
	}

	msg, err := b.dayText(ctx, now)
	if err != nil {
		return err
	}
	if err := b.send(ctx, id, msg); err != nil {
		return err
	}
	return b.store.SetSetting(ctx, key, today)
}

func (b *bot) dayText(ctx context.Context, now time.Time) (string, error) {
	y, m, d := now.Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, now.Location())

	var day telegram.Day
	var err error
	if day.Activity, err = b.store.Activity(ctx, midnight); err != nil {
		return "", err
	}
	if day.NewKanji, day.NewWords, err = b.store.IntroducedSince(ctx, midnight); err != nil {
		return "", err
	}
	if day.Streak, _, err = b.store.Streak(ctx, now); err != nil {
		return "", err
	}
	counts, err := b.store.Counts(ctx, now)
	if err != nil {
		return "", err
	}
	day.Due = counts.Due
	return telegram.DailySummary(day), nil
}
