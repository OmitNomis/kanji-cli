// Package srs is the spaced-repetition scheduler: ten levels, one step forward
// for a correct answer and two back for a wrong one.
//
// This is SM-2 with the ease factor taken out. SM-2 earns its per-item ease
// from a four-way self-grade, and this program has no self-grade to earn it
// from: an answer either is the reading or it is not. What is left is a fixed
// interval ladder, which is also what makes the scheduler cheap enough to be
// a single integer per item in SQLite.
package srs

import (
	"math/rand"
	"time"
)

// MaxLevel is the top of the ladder. An item that reaches it is still
// scheduled, just half a year out.
const MaxLevel = 9

// HintMaxLevel is the level at and below which the reading is shown with the
// prompt. A new item has nothing to recall yet, so the first few encounters
// teach rather than test.
const HintMaxLevel = 1

// DemotionAtOrAbove is the level from which a wrong answer costs two steps
// instead of one. Forgetting something you had held for a month is worth more
// than forgetting something you met yesterday.
const DemotionAtOrAbove = 3

// LeechLapses is how many times an item may be forgotten before it is worth
// flagging. The name is Anki's.
const LeechLapses = 8

// intervals is the delay until an item at each level comes back. The first rung
// is minutes rather than hours on purpose: a newly introduced reading should
// come round again inside the same session.
var intervals = [MaxLevel + 1]time.Duration{
	0: 10 * time.Minute,
	1: 4 * time.Hour,
	2: 8 * time.Hour,
	3: 24 * time.Hour,
	4: 3 * 24 * time.Hour,
	5: 7 * 24 * time.Hour,
	6: 14 * 24 * time.Hour,
	7: 30 * 24 * time.Hour,
	8: 90 * 24 * time.Hour,
	9: 180 * 24 * time.Hour,
}

// jitterFrom is the shortest interval worth spreading out. Below a day the
// clumping does not matter; above it, a few thousand items introduced in one
// sitting would otherwise all fall due on the same minute.
const jitterFrom = 24 * time.Hour

// jitterFraction is how far either side of the exact interval an item may land.
const jitterFraction = 0.05

// State is everything the scheduler knows about one item. It maps one to one
// onto a row of the review table.
type State struct {
	Level      int
	DueAt      time.Time
	Reps       int
	Lapses     int
	Streak     int
	BestStreak int
	Correct    int
	Incorrect  int
	AddedAt    time.Time
	LastAt     time.Time // zero until the item has been answered once
}

// Scheduler applies the ladder. The zero value is usable and completely
// deterministic, which is what the tests want; Default adds jitter, which is
// what a real queue wants.
type Scheduler struct {
	// Jitter perturbs an interval before it becomes a due time. nil means exact.
	Jitter func(time.Duration) time.Duration
}

// Default returns a scheduler that spreads due times by up to 5% either way on
// intervals of a day or more.
func Default() Scheduler {
	// Scheduling is driven by user keystrokes, so a locally seeded generator is
	// both sufficient and kept out of the global one.
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return Scheduler{
		Jitter: func(d time.Duration) time.Duration {
			if d < jitterFrom {
				return d
			}
			spread := float64(d) * jitterFraction
			return d + time.Duration((r.Float64()*2-1)*spread)
		},
	}
}

// Introduce returns the state of an item being added to the queue: level zero
// and due now, so it is asked immediately with its reading shown.
func (s Scheduler) Introduce(now time.Time) State {
	return State{Level: 0, DueAt: now, AddedAt: now}
}

// Grade advances or demotes an item and returns its new state. The old state is
// not modified.
func (s Scheduler) Grade(st State, correct bool, now time.Time) State {
	next := st
	next.Reps++
	next.LastAt = now
	if next.AddedAt.IsZero() {
		next.AddedAt = now
	}

	if correct {
		next.Correct++
		next.Streak++
		if next.Streak > next.BestStreak {
			next.BestStreak = next.Streak
		}
		if next.Level < MaxLevel {
			next.Level++
		}
	} else {
		next.Incorrect++
		next.Streak = 0
		// A wrong answer on a brand-new item is not a lapse; it has not been
		// learned yet, so there was nothing to forget.
		if st.Level > 0 {
			next.Lapses++
		}
		next.Level = demote(st.Level)
	}

	next.DueAt = now.Add(s.interval(next.Level))
	return next
}

// demote is the penalty for a wrong answer: one step near the bottom of the
// ladder, two once the intervals are long enough that the loss is real.
func demote(level int) int {
	step := 1
	if level >= DemotionAtOrAbove {
		step = 2
	}
	if level-step < 0 {
		return 0
	}
	return level - step
}

func (s Scheduler) interval(level int) time.Duration {
	d := Interval(level)
	if s.Jitter != nil {
		d = s.Jitter(d)
	}
	if d < time.Minute {
		d = time.Minute
	}
	return d
}

// Interval is the exact, unjittered delay for a level. Exported so the UI can
// say what it is about to do before doing it.
func Interval(level int) time.Duration {
	switch {
	case level < 0:
		return intervals[0]
	case level > MaxLevel:
		return intervals[MaxLevel]
	default:
		return intervals[level]
	}
}

// ShowHint reports whether the reading should be shown alongside the prompt.
func ShowHint(level int) bool { return level <= HintMaxLevel }

// IsLeech reports whether an item has been forgotten often enough to be worth
// relearning deliberately rather than waiting for the ladder to fix it.
func (st State) IsLeech() bool { return st.Lapses >= LeechLapses }

// Accuracy is the share of answers that were correct, or 0 for an item never
// answered.
func (st State) Accuracy() float64 {
	total := st.Correct + st.Incorrect
	if total == 0 {
		return 0
	}
	return float64(st.Correct) / float64(total)
}

// LevelName is a short label for a level, for status lines and statistics.
func LevelName(level int) string {
	switch {
	case level <= 0:
		return "new"
	case level <= 2:
		return "learning"
	case level <= 4:
		return "familiar"
	case level <= 6:
		return "known"
	case level < MaxLevel:
		return "solid"
	default:
		return "burned"
	}
}
