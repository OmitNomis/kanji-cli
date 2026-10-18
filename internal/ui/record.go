package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/OmitNomis/cli-kanji/internal/kana"
	"github.com/OmitNomis/cli-kanji/internal/srs"
	"github.com/OmitNomis/cli-kanji/internal/store"
)

// RecordLines describes what you have done with an item, for a lookup: where it
// sits on the ladder and when it is next asked, how often you have got it right,
// and the last wrong thing you typed. Both front-ends print these lines, each in
// its own dress, so the wording lives here once.
//
// An item never answered gets a single line saying so.
func RecordLines(rec store.Record, now time.Time) []string {
	if !rec.Seen {
		return []string{"not started yet"}
	}
	st := rec.State

	status := []string{fmt.Sprintf("%s (level %d)", srs.LevelName(st.Level), st.Level)}
	if st.Level < srs.MaxLevel {
		status = append(status, "due "+dueIn(st.DueAt, now))
	}
	if st.IsLeech() {
		status = append(status, "leech")
	}

	tally := []string{fmt.Sprintf("%s right, %s wrong", Count(st.Correct), Count(st.Incorrect))}
	if st.Lapses > 0 {
		tally = append(tally, fmt.Sprintf("forgotten %s", times(st.Lapses)))
	}
	if st.BestStreak > 1 {
		tally = append(tally, fmt.Sprintf("best streak %d", st.BestStreak))
	}

	lines := []string{strings.Join(status, " · "), strings.Join(tally, " · ")}
	if rec.LastWrong != "" {
		lines = append(lines, fmt.Sprintf("last missed with %s, %s ago",
			kana.ToHiragana(rec.LastWrong), Duration(now.Sub(rec.LastWrongAt))))
	}
	return lines
}

func dueIn(at, now time.Time) string {
	if !at.After(now) {
		return "now"
	}
	return "in " + Duration(at.Sub(now))
}

func times(n int) string {
	if n == 1 {
		return "once"
	}
	return fmt.Sprintf("%d times", n)
}

// Stage is the accuracy of one named stretch of the ladder.
type Stage struct {
	Name string
	store.Tally
}

// Stages groups accuracy by level name, which is what a level means to the
// reviewer, in ladder order and leaving out stages with no answers.
func Stages(byLevel [srs.MaxLevel + 1]store.Tally) []Stage {
	var out []Stage
	for level, t := range byLevel {
		name := srs.LevelName(level)
		if len(out) == 0 || out[len(out)-1].Name != name {
			out = append(out, Stage{Name: name})
		}
		last := &out[len(out)-1]
		last.Answered += t.Answered
		last.Correct += t.Correct
	}
	kept := out[:0]
	for _, st := range out {
		if st.Answered > 0 {
			kept = append(kept, st)
		}
	}
	return kept
}
