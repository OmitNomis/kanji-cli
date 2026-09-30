package srs

import (
	"testing"
	"time"
)

var now = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

// exact is the zero-value scheduler: no jitter, so due times are predictable.
var exact Scheduler

func TestIntroduceIsDueImmediatelyAndHinted(t *testing.T) {
	st := exact.Introduce(now)
	if st.Level != 0 {
		t.Errorf("level = %d, want 0", st.Level)
	}
	if !st.DueAt.Equal(now) {
		t.Errorf("dueAt = %v, want %v", st.DueAt, now)
	}
	if !ShowHint(st.Level) {
		t.Error("a newly introduced item must show its reading")
	}
}

func TestCorrectAnswerClimbsOneRung(t *testing.T) {
	st := exact.Introduce(now)
	for want := 1; want <= MaxLevel; want++ {
		st = exact.Grade(st, true, now)
		if st.Level != want {
			t.Fatalf("after %d correct answers level = %d, want %d", want, st.Level, want)
		}
		if !st.DueAt.Equal(now.Add(Interval(want))) {
			t.Errorf("level %d due at %v, want %v", want, st.DueAt, now.Add(Interval(want)))
		}
	}

	// The ladder has a top, and staying there is not an error.
	st = exact.Grade(st, true, now)
	if st.Level != MaxLevel {
		t.Errorf("level = %d, want it to stay at %d", st.Level, MaxLevel)
	}
	if st.Correct != MaxLevel+1 {
		t.Errorf("correct = %d, want %d", st.Correct, MaxLevel+1)
	}
}

func TestWrongAnswerDemotion(t *testing.T) {
	cases := []struct{ from, want int }{
		{0, 0}, // nothing below the bottom
		{1, 0},
		{2, 1},
		{3, 1}, // two steps from here up
		{5, 3},
		{9, 7},
	}
	for _, c := range cases {
		st := exact.Grade(State{Level: c.from, AddedAt: now}, false, now)
		if st.Level != c.want {
			t.Errorf("wrong answer at level %d gave level %d, want %d", c.from, st.Level, c.want)
		}
		if !st.DueAt.Equal(now.Add(Interval(c.want))) {
			t.Errorf("level %d: due at %v, want %v", c.want, st.DueAt, now.Add(Interval(c.want)))
		}
	}
}

func TestWrongAnswerOnNewItemIsNotALapse(t *testing.T) {
	// Level 0 means the item has never been answered correctly, so a wrong
	// answer is a first encounter, not a forgetting.
	st := exact.Grade(exact.Introduce(now), false, now)
	if st.Lapses != 0 {
		t.Errorf("lapses = %d, want 0 for a wrong answer at level 0", st.Lapses)
	}
	if st.Incorrect != 1 {
		t.Errorf("incorrect = %d, want 1", st.Incorrect)
	}

	// Once it has been learned, forgetting it counts.
	st = exact.Grade(st, true, now)
	st = exact.Grade(st, false, now)
	if st.Lapses != 1 {
		t.Errorf("lapses = %d, want 1 once the item had reached level 1", st.Lapses)
	}
}

func TestStreakTracking(t *testing.T) {
	st := exact.Introduce(now)
	for range 4 {
		st = exact.Grade(st, true, now)
	}
	if st.Streak != 4 || st.BestStreak != 4 {
		t.Fatalf("streak = %d, best = %d, want 4 and 4", st.Streak, st.BestStreak)
	}

	st = exact.Grade(st, false, now)
	if st.Streak != 0 {
		t.Errorf("streak = %d, want 0 after a wrong answer", st.Streak)
	}
	if st.BestStreak != 4 {
		t.Errorf("bestStreak = %d, want it to survive at 4", st.BestStreak)
	}

	st = exact.Grade(st, true, now)
	if st.Streak != 1 || st.BestStreak != 4 {
		t.Errorf("streak = %d, best = %d, want 1 and 4", st.Streak, st.BestStreak)
	}
}

func TestCountersAndAccuracy(t *testing.T) {
	st := exact.Introduce(now)
	for _, correct := range []bool{true, true, false, true} {
		st = exact.Grade(st, correct, now)
	}

	if st.Reps != 4 {
		t.Errorf("reps = %d, want 4", st.Reps)
	}
	if st.Correct != 3 || st.Incorrect != 1 {
		t.Errorf("correct/incorrect = %d/%d, want 3/1", st.Correct, st.Incorrect)
	}
	if got := st.Accuracy(); got != 0.75 {
		t.Errorf("accuracy = %v, want 0.75", got)
	}
	if got := (State{}).Accuracy(); got != 0 {
		t.Errorf("accuracy of an unanswered item = %v, want 0", got)
	}
	if !st.LastAt.Equal(now) {
		t.Errorf("lastAt = %v, want %v", st.LastAt, now)
	}
}

func TestIntervalsIncreaseAndAreClamped(t *testing.T) {
	for level := 1; level <= MaxLevel; level++ {
		if Interval(level) <= Interval(level-1) {
			t.Errorf("interval at level %d (%v) is not longer than at %d (%v)",
				level, Interval(level), level-1, Interval(level-1))
		}
	}
	if Interval(-5) != Interval(0) {
		t.Error("a negative level should clamp to the bottom interval")
	}
	if Interval(MaxLevel+5) != Interval(MaxLevel) {
		t.Error("a level past the top should clamp to the top interval")
	}
}

func TestShowHintOnlyAtLowLevels(t *testing.T) {
	for level := 0; level <= MaxLevel; level++ {
		want := level <= HintMaxLevel
		if got := ShowHint(level); got != want {
			t.Errorf("ShowHint(%d) = %v, want %v", level, got, want)
		}
	}
}

func TestLeechThreshold(t *testing.T) {
	if (State{Lapses: LeechLapses - 1}).IsLeech() {
		t.Error("an item just under the threshold is not a leech")
	}
	if !(State{Lapses: LeechLapses}).IsLeech() {
		t.Error("an item at the threshold is a leech")
	}
}

func TestDefaultSchedulerJittersLongIntervalsOnly(t *testing.T) {
	s := Default()

	// Short intervals must stay exact, or a 10-minute relearn step could drift
	// outside the session it belongs to.
	for range 50 {
		st := s.Grade(State{Level: 0, AddedAt: now}, false, now)
		if !st.DueAt.Equal(now.Add(Interval(0))) {
			t.Fatalf("level 0 due at %v, want exactly %v", st.DueAt, now.Add(Interval(0)))
		}
	}

	// Day-and-longer intervals must land within the advertised spread, and must
	// actually vary rather than all landing on the same value.
	base := Interval(4)
	spread := time.Duration(float64(base) * jitterFraction)
	seen := map[time.Time]bool{}
	for range 200 {
		st := s.Grade(State{Level: 3, AddedAt: now}, true, now)
		delta := st.DueAt.Sub(now)
		if delta < base-spread || delta > base+spread {
			t.Fatalf("due in %v, want within %v of %v", delta, spread, base)
		}
		seen[st.DueAt] = true
	}
	if len(seen) < 2 {
		t.Error("jitter produced a single due time across 200 gradings")
	}
}

func TestLevelNames(t *testing.T) {
	// Every level must have a name, and the top one must read as finished.
	for level := 0; level <= MaxLevel; level++ {
		if LevelName(level) == "" {
			t.Errorf("level %d has no name", level)
		}
	}
	if LevelName(0) != "new" {
		t.Errorf("LevelName(0) = %q, want \"new\"", LevelName(0))
	}
	if LevelName(MaxLevel) != "burned" {
		t.Errorf("LevelName(%d) = %q, want \"burned\"", MaxLevel, LevelName(MaxLevel))
	}
}

func TestGradeDoesNotMutateItsInput(t *testing.T) {
	before := State{Level: 4, Reps: 7, Correct: 5, Incorrect: 2, Streak: 3, AddedAt: now}
	snapshot := before
	exact.Grade(before, true, now.Add(time.Hour))
	if before != snapshot {
		t.Errorf("Grade modified its argument: %+v, want %+v", before, snapshot)
	}
}
