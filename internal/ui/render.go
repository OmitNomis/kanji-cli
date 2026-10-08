// Package ui draws the review session. It is line-based on purpose: the target
// machine is a 1 GB Atom often reached over ssh, and a full-screen renderer
// would cost memory for redraws while fighting whatever input method the
// reviewer is typing kana with.
package ui

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/OmitNomis/cli-kanji/internal/review"
	"github.com/OmitNomis/cli-kanji/internal/srs"
	"github.com/OmitNomis/cli-kanji/internal/store"
)

// Layout constants. The indents are what give the session its shape: the
// prompt sits furthest in, feedback hangs off it, and the rules bracket it.
const (
	indent       = "  "
	promptIndent = "        "
	markIndent   = "    "
	noteIndent   = "       "

	// DefaultWidth is a conservative 80-column terminal with a margin. COLUMNS
	// is not exported to child processes by most shells, so guessing wide and
	// wrapping is worse than guessing narrow.
	DefaultWidth = 72
	minWidth     = 40

	// maxOtherReadings bounds the "also" line. A kanji can have a dozen
	// readings and listing all of them teaches nothing.
	maxOtherReadings = 6
)

// Renderer writes the interface.
type Renderer struct {
	out   io.Writer
	style Style
	width int
}

// New creates a renderer. Call it before any output: on Windows it is what
// switches the console to UTF-8, without which every kanji is mojibake.
func New(out io.Writer, width int, forceColor bool) *Renderer {
	enableUTF8()
	if width < minWidth {
		width = DefaultWidth
	}
	return &Renderer{out: out, style: NewStyle(forceColor), width: width}
}

// Style exposes the colour helpers for callers that need to tint their own text.
func (r *Renderer) Style() Style { return r.style }

func (r *Renderer) printf(format string, args ...any) {
	fmt.Fprintf(r.out, format, args...)
}

// Line writes one indented line.
func (r *Renderer) Line(format string, args ...any) {
	r.printf(indent+format+"\n", args...)
}

// Blank writes an empty line.
func (r *Renderer) Blank() { r.printf("\n") }

// Rule draws a horizontal rule with a label on the left and, optionally, a
// note on the right.
func (r *Renderer) Rule(label, note string) {
	left := "── " + label + " "
	available := r.width - len(indent) - Width(left)
	if note != "" {
		available -= Width(note) + 1
	}
	if available < 1 {
		available = 1
	}

	line := left + strings.Repeat("─", available)
	if note != "" {
		line += " " + note
	}
	r.printf("%s%s\n", indent, r.style.Dim(line))
}

// Card shows the prompt to answer.
//
// The kind is always named. Without it a bare 本 is ambiguous in a way that
// changes the right answer: as a kanji it accepts ほん or もと, as a word it is
// ほん and nothing else.
func (r *Renderer) Card(position, total, remaining int, c store.Card) {
	kind := "word"
	if c.IsKanji() {
		kind = fmt.Sprintf("kanji · grade %d", c.Grade)
	}

	label := fmt.Sprintf("%d/%d · %s · %s", position, total, kind, srs.LevelName(c.State.Level))
	note := ""
	if remaining > 0 {
		note = fmt.Sprintf("%s left", Count(remaining))
	}

	r.Blank()
	r.Rule(label, note)
	r.Blank()
	r.printf("%s%s\n", promptIndent, r.style.BoldCyan(Spaced(c.Prompt)))

	// The reading is given away while an item is new, because there is nothing
	// to recall yet: the first encounters teach it, the later ones test it.
	if srs.ShowHint(c.State.Level) && c.Hint != "" {
		r.printf("%s%s  %s\n", promptIndent,
			r.style.Dim("reading"),
			r.style.Yellow(Truncate(c.Hint, r.width-Width(promptIndent)-10)))
	}
	r.Blank()
}

// Correct reports a right answer.
func (r *Renderer) Correct(res review.Result, c store.Card) {
	r.printf("%s%s  %s   %s\n", markIndent,
		r.style.Green("✓"),
		r.style.Bold(res.Matched),
		r.style.Dim(Truncate(c.Gloss, r.width-Width(markIndent)-Width(res.Matched)-8)))

	if others := otherReadings(c.Answers, res.Matched); others != "" {
		r.printf("%s%s\n", noteIndent, r.style.Dim("also "+others))
	}
	r.printf("%s%s\n", noteIndent, r.style.Dim(progression(res)))
}

// Wrong reports a wrong answer and what the reading actually was.
func (r *Renderer) Wrong(res review.Result, c store.Card) {
	typed := res.Normalized
	if typed == "" {
		typed = "(nothing)"
	}

	r.printf("%s%s  %s\n", markIndent, r.style.Red("✗"), r.style.Dim("you typed "+typed))
	r.printf("%s%s is %s   %s\n", noteIndent,
		c.Prompt,
		r.style.Bold(strings.Join(limit(c.Answers, maxOtherReadings), ", ")),
		r.style.Dim(Truncate(c.Gloss, r.width-Width(noteIndent)-30)))
	r.printf("%s%s\n", noteIndent, r.style.Dim(progression(res)))
}

// Revealed reports an answer the reviewer asked to be shown. It is graded as
// wrong, because being told is not knowing.
func (r *Renderer) Revealed(res review.Result, c store.Card) {
	r.printf("%s%s  %s is %s   %s\n", markIndent,
		r.style.Yellow("·"), c.Prompt,
		r.style.Bold(strings.Join(limit(c.Answers, maxOtherReadings), ", ")),
		r.style.Dim(Truncate(c.Gloss, r.width-Width(markIndent)-30)))
	r.printf("%s%s\n", noteIndent, r.style.Dim(progression(res)))
}

// Drill asks the reviewer to type the reading they just missed.
func (r *Renderer) Drill() {
	r.printf("%s%s\n", noteIndent, r.style.Dim("type it to carry on"))
}

// Rejected tells the reviewer a retype did not match, naming one reading to
// aim at. Without it a failed retype is a bare prompt again, which reads as the
// program having ignored the input.
func (r *Renderer) Rejected(c store.Card) {
	target := ""
	if len(c.Answers) > 0 {
		target = c.Answers[0]
	}
	r.printf("%s%s  %s\n", noteIndent, r.style.Red("✗"), r.style.Dim("type "+target))
}

// Garbled says a line came through mangled and has to be typed again. Nothing
// was graded, so it says so.
func (r *Renderer) Garbled() {
	r.printf("%s%s\n", noteIndent, r.style.Yellow("that came through garbled, not graded: type it again"))
}

// Accepted acknowledges a successful drill retype without re-reporting the
// whole answer.
func (r *Renderer) Accepted() {
	r.printf("%s%s\n", noteIndent, r.style.Green("✓"))
}

// Ask writes the input prompt. It deliberately leaves the cursor on the line.
func (r *Renderer) Ask(label string) {
	r.printf("%s%s ", indent, r.style.Bold(label))
}

// Banner opens the program with where the queue stands.
func (r *Renderer) Banner(c store.Counts, builtAt string) {
	r.Blank()
	r.Rule("cli-kanji", builtAt)
	r.Line("%s", r.style.Dim(fmt.Sprintf(
		"dictionary: %s kanji, %s compounds",
		Count(c.DictKanjiItems), Count(c.DictWordItems))))
	r.Line("%s", fmt.Sprintf("%s due · %s in progress · %s burned",
		r.style.Bold(Count(c.Due)), Count(c.InProgress), Count(c.Burned)))
}

// Help lists the commands available at a prompt.
func (r *Renderer) Help() {
	r.Line("%s", r.style.Dim("type the reading, kana or romaji"))
	r.Line("%s", r.style.Dim("? reveal · :s skip · :i info · :stats · :q quit"))
}

// Info prints a full dictionary entry and the compounds that use it.
func (r *Renderer) Info(it store.Item, compounds []store.Item) {
	kind := "word"
	if it.IsKanji() {
		kind = fmt.Sprintf("kanji · grade %d", it.Grade)
	}

	r.Blank()
	r.Rule(it.Prompt+" · "+kind, "")
	r.Blank()
	r.Line("readings  %s", r.style.Bold(strings.Join(it.Answers, ", ")))

	// Only a kanji's hint adds anything here: it keeps the on/kun split and the
	// okurigana markers that the flattened answer list throws away. A word's
	// hint is just its primary reading, already on the line above.
	if it.IsKanji() && it.Hint != "" {
		r.Line("          %s", r.style.Dim(it.Hint))
	}
	r.Line("meaning   %s", it.Gloss)

	if len(compounds) > 0 {
		r.Blank()
		r.Line("%s", r.style.Dim("compounds"))
		for _, w := range compounds {
			r.Line("  %s  %s  %s",
				Pad(w.Prompt, 10),
				Pad(Truncate(w.Answers[0], 14), 14),
				r.style.Dim(Truncate(w.Gloss, r.width-34)))
		}
	}
	r.Blank()
}

// Summary closes a session.
func (r *Renderer) Summary(s *review.Session, c store.Counts, nextDue time.Time, hasNext bool, now time.Time) {
	r.Blank()
	r.Rule("session", "")

	if s.Answered == 0 {
		r.Line("%s", r.style.Dim("nothing answered"))
	} else {
		r.Line("%d answered · %s correct · %.0f%% accuracy",
			s.Answered, r.style.Green(fmt.Sprint(s.Correct)), s.Accuracy()*100)
		if s.Incorrect > 0 {
			r.Line("%s", r.style.Dim(fmt.Sprintf("%d missed", s.Incorrect)))
		}
		if s.Introduced.Kanji > 0 || s.Introduced.Words > 0 {
			r.Line("%s", r.style.Dim(fmt.Sprintf("introduced %d kanji and %s",
				s.Introduced.Kanji, plural(s.Introduced.Words, "compound", "compounds"))))
		}
	}

	switch {
	case c.Due > 0:
		r.Line("%s", fmt.Sprintf("%s still due", r.style.Bold(Count(c.Due))))
	case hasNext:
		r.Line("%s", r.style.Dim("next review in "+Duration(nextDue.Sub(now))))
	}
	r.Blank()
}

// Stats prints the whole picture: coverage of the dictionary, the shape of the
// queue across the level ladder, and what the last week actually looked like.
func (r *Renderer) Stats(c store.Counts, week store.Activity, nextDue time.Time, hasNext bool, now time.Time) {
	r.Blank()
	r.Rule("progress", "")
	r.Blank()
	r.Line("kanji      %s", bar(c.KanjiInProgress, c.DictKanjiItems, 24, r.style))
	r.Line("compounds  %s", bar(c.WordsInProgress, c.DictWordItems, 24, r.style))
	r.Blank()

	peak := 0
	for _, n := range c.ByLevel {
		if n > peak {
			peak = n
		}
	}
	for level := 0; level <= srs.MaxLevel; level++ {
		n := c.ByLevel[level]
		if n == 0 {
			continue
		}
		r.Line("%s %s %s",
			Pad(fmt.Sprintf("lvl %d %s", level, srs.LevelName(level)), 18),
			Pad(Count(n), 7),
			r.style.Dim(strings.Repeat("▌", scale(n, peak, 20))))
	}

	r.Blank()
	if week.Answered > 0 {
		r.Line("last 7 days  %s over %s, %.0f%% correct",
			plural(week.Answered, "answer", "answers"),
			plural(week.Days, "day", "days"),
			week.Accuracy()*100)
		r.Blank()
	}

	r.Line("due now    %s", r.style.Bold(Count(c.Due)))
	if c.Burned > 0 {
		r.Line("burned     %s", Count(c.Burned))
	}
	if c.Leeches > 0 {
		r.Line("leeches    %s  %s", Count(c.Leeches),
			r.style.Dim(fmt.Sprintf("(forgotten %d+ times)", srs.LeechLapses)))
	}
	if c.Due == 0 && hasNext {
		r.Line("next       %s", r.style.Dim("in "+Duration(nextDue.Sub(now))))
	}
	r.Blank()
}

// progression describes what an answer did to an item's schedule.
func progression(res review.Result) string {
	before := srs.LevelName(res.Before.Level)
	after := srs.LevelName(res.After.Level)
	move := after
	if before != after {
		move = before + " → " + after
	}
	return fmt.Sprintf("%s · back in %s", move, Duration(time.Until(res.After.DueAt)))
}

// otherReadings lists the accepted readings the answer was not, so a correct
// answer still teaches the ones that were not given.
func otherReadings(answers []string, matched string) string {
	var out []string
	for _, a := range answers {
		if a != matched {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(limit(out, maxOtherReadings), ", ")
}

func limit(in []string, n int) []string {
	if len(in) <= n {
		return in
	}
	return append(in[:n:n], "…")
}

// Duration renders a delay the way a person would say it.
func Duration(d time.Duration) string {
	if d < 0 {
		return "now"
	}
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < 90*time.Minute:
		return fmt.Sprintf("%dm", int(d.Round(time.Minute).Minutes()))
	case d < 36*time.Hour:
		return fmt.Sprintf("%dh", int(d.Round(time.Hour).Hours()))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Round(24*time.Hour).Hours()/24))
	case d < 2*365*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d.Hours()/(24*30)))
	default:
		// The ladder tops out at six months, so this only shows for a due date
		// that came from somewhere else. "400mo" would read as a bug.
		return fmt.Sprintf("%dy", int(d.Hours()/(24*365)))
	}
}

// bar draws a proportion as a filled bar with its numbers alongside.
func bar(have, total, cells int, s Style) string {
	if total <= 0 {
		return s.Dim("none")
	}
	filled := scale(have, total, cells)
	return fmt.Sprintf("%s%s  %s",
		s.Green(strings.Repeat("█", filled)),
		s.Dim(strings.Repeat("░", cells-filled)),
		fmt.Sprintf("%s / %s", Count(have), Count(total)))
}

// scale maps have/total onto cells, never rounding a non-zero value down to
// nothing: a bar that shows empty when progress has been made is a lie.
func scale(have, total, cells int) int {
	if total <= 0 || have <= 0 {
		return 0
	}
	n := have * cells / total
	if n == 0 {
		n = 1
	}
	if n > cells {
		n = cells
	}
	return n
}

// plural picks a noun form for n. Sessions report single-item days often
// enough that "1 compounds" would be noticeable.
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%s %s", Count(n), many)
}

// Count groups thousands, because "25406" and "2136" are hard to compare at a
// glance and "25,406" is not.
func Count(n int) string {
	s := fmt.Sprint(n)
	if n < 0 || len(s) <= 3 {
		return s
	}

	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}
