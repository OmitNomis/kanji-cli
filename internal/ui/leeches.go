package ui

import (
	"fmt"
	"strings"

	"github.com/OmitNomis/cli-kanji/internal/srs"
	"github.com/OmitNomis/cli-kanji/internal/store"
)

// Leeches lists the items forgotten too often, each with what was typed for
// it instead: the same wrong reading every time is a confusion to sort out,
// a different one every time is a reading that never stuck.
func (r *Renderer) Leeches(list []store.Leech) {
	r.Blank()
	r.Rule("leeches", Count(len(list)))
	r.Blank()
	if len(list) == 0 {
		r.Line("none: nothing has been forgotten %d times.", srs.LeechLapses)
		r.Blank()
		return
	}

	// Each line hangs its detail in a column of its own, as a lookup's
	// compound list does.
	lead := indent + strings.Repeat(" ", 10+2+14+2)
	for _, l := range list {
		detail := fmt.Sprintf("forgotten %d× · %s", l.Card.State.Lapses, srs.LevelName(l.Card.State.Level))
		if len(l.Wrong) > 0 {
			detail += " · ✗ " + missList(l.Wrong)
		}
		lines := Wrap(detail, r.width()-Width(lead))
		r.Line("%s  %s  %s",
			Pad(l.Card.Prompt, 10),
			Pad(Truncate(firstOf(l.Card.Answers), 14), 14),
			r.style.Dim(firstOf(lines)))
		for _, more := range lines[min(1, len(lines)):] {
			r.Line("%s%s", strings.Repeat(" ", Width(lead)-Width(indent)), r.style.Dim(more))
		}
	}
	r.Blank()
	r.Line("%s", r.style.Dim("kanji -leeches reviews just these"))
	r.Blank()
}

// Related shows words already known that share a kanji with a leech just
// missed: something for a reading that will not stick on its own to hang on.
func (r *Renderer) Related(items []store.Item) {
	if len(items) == 0 {
		return
	}
	var words []string
	for _, it := range items {
		words = append(words, it.Prompt+" "+firstOf(it.Answers))
	}
	r.labelled(noteIndent, "you know  ", strings.Join(words, " · "), r.style.Dim, r.style.Cyan)
}

// missList is wrong answers with how often each was given.
func missList(ms []store.Miss) string {
	var parts []string
	for _, m := range ms {
		if m.Count > 1 {
			parts = append(parts, fmt.Sprintf("%s ×%d", m.Kana, m.Count))
		} else {
			parts = append(parts, m.Kana)
		}
	}
	return strings.Join(parts, ", ")
}
