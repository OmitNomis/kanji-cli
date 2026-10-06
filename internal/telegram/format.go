package telegram

import (
	"fmt"
	"strings"
	"time"

	"github.com/OmitNomis/cli-kanji/internal/review"
	"github.com/OmitNomis/cli-kanji/internal/srs"
	"github.com/OmitNomis/cli-kanji/internal/store"
	"github.com/OmitNomis/cli-kanji/internal/ui"
)

// Telegram is not a terminal, so none of internal/ui's drawing applies: there
// is no ANSI, no fixed width, and a message arrives as one unit rather than
// being revealed a line at a time. What is worth borrowing from that package is
// the prose -- Duration and Spaced are formatting, not drawing -- and a second
// copy of either would be a second place to fix.

// maxReadings bounds a list of readings. A kanji can have a dozen and naming
// all of them teaches nothing.
const maxReadings = 6

// maxCompounds bounds the compound list in a lookup, which has to fit on a
// phone screen rather than in a terminal.
const maxCompounds = 8

// Escape makes text safe inside HTML. Telegram's HTML mode needs exactly these
// three, which is the reason this bot does not use MarkdownV2.
func Escape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	return strings.ReplaceAll(s, ">", "&gt;")
}

// Join assembles a message from blocks, which is how a front-end that cannot
// print incrementally builds one: feedback and the next card go out together
// rather than as two notifications.
func Join(blocks ...string) string {
	var kept []string
	for _, b := range blocks {
		if strings.TrimSpace(b) != "" {
			kept = append(kept, b)
		}
	}
	return strings.Join(kept, "\n\n")
}

// Card is the prompt to answer.
//
// The reading appears only while the item is new, exactly as in the terminal:
// srs.ShowHint decides, and above that level the reading must not be anywhere
// in the message -- not in a footer, not in a preview, not as a hint about how
// long it is.
func Card(position, total, remaining int, c store.Card) string {
	kind := "word"
	if c.IsKanji() {
		kind = fmt.Sprintf("kanji · grade %d", c.Grade)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "<b>%d/%d</b> · %s · %s", position, total, kind, srs.LevelName(c.State.Level))
	if remaining > 0 {
		fmt.Fprintf(&b, " · %d left", remaining)
	}

	// Monospace keeps the characters from being reflowed and renders them big
	// enough to read on a phone.
	fmt.Fprintf(&b, "\n\n<code>%s</code>", Escape(ui.Spaced(c.Prompt)))

	if srs.ShowHint(c.State.Level) && c.Hint != "" {
		fmt.Fprintf(&b, "\n\nreading <b>%s</b>", Escape(c.Hint))
	}
	return b.String()
}

// Correct reports a right answer.
func Correct(res review.Result, c store.Card) string {
	var b strings.Builder
	fmt.Fprintf(&b, "✓ <b>%s</b>", Escape(res.Matched))
	if c.Gloss != "" {
		fmt.Fprintf(&b, "\n%s", Escape(c.Gloss))
	}
	if others := otherReadings(c.Answers, res.Matched); others != "" {
		fmt.Fprintf(&b, "\nalso %s", Escape(others))
	}
	fmt.Fprintf(&b, "\n<i>%s</i>", Escape(progression(res)))
	return b.String()
}

// Wrong reports a wrong answer and what the reading actually was.
func Wrong(res review.Result, c store.Card) string {
	typed := res.Normalized
	if typed == "" {
		typed = "(nothing)"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "✗ you typed %s", Escape(typed))
	fmt.Fprintf(&b, "\n%s is <b>%s</b>", Escape(c.Prompt), Escape(readings(c.Answers)))
	if c.Gloss != "" {
		fmt.Fprintf(&b, "\n%s", Escape(c.Gloss))
	}
	fmt.Fprintf(&b, "\n<i>%s</i>", Escape(progression(res)))
	return b.String()
}

// Revealed reports an answer the reviewer asked to be shown, which is graded as
// wrong because being told is not knowing.
func Revealed(res review.Result, c store.Card) string {
	var b strings.Builder
	fmt.Fprintf(&b, "· %s is <b>%s</b>", Escape(c.Prompt), Escape(readings(c.Answers)))
	if c.Gloss != "" {
		fmt.Fprintf(&b, "\n%s", Escape(c.Gloss))
	}
	fmt.Fprintf(&b, "\n<i>%s</i>", Escape(progression(res)))
	return b.String()
}

// Drill asks for the reading that was just missed.
//
// A chat makes this natural -- it reads as the next thing being asked for --
// and it preserves the one rule the program is built around: you produce the
// kana, or you do not. There is no button to press instead, because tapping a
// button is multiple choice.
func Drill() string { return "<b>type it to carry on</b>  ·  /skip to move on" }

// Mistyped tells the reviewer a retype did not match, naming one reading to aim
// at. The reading is already on screen by this point, so nothing is given away.
func Mistyped(c store.Card) string {
	if len(c.Answers) == 0 {
		return "✗ not that"
	}
	return fmt.Sprintf("✗ type <b>%s</b>", Escape(c.Answers[0]))
}

// Retyped acknowledges a successful retype without re-reporting the answer.
func Retyped() string { return "✓ got it" }

// Skipped acknowledges a card passed over, so a skip is never silent.
func Skipped() string { return "· skipped" }

// Info is a dictionary entry and the compounds that use it.
func Info(it store.Item, compounds []store.Item) string {
	kind := "word"
	if it.IsKanji() {
		kind = fmt.Sprintf("kanji · grade %d", it.Grade)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "<b>%s</b> · %s", Escape(it.Prompt), kind)
	fmt.Fprintf(&b, "\n\nreadings <b>%s</b>", Escape(strings.Join(it.Answers, ", ")))

	// Only a kanji's hint adds anything: it keeps the on/kun split and the
	// okurigana markers that the flattened answer list throws away.
	if it.IsKanji() && it.Hint != "" {
		fmt.Fprintf(&b, "\n<i>%s</i>", Escape(it.Hint))
	}
	fmt.Fprintf(&b, "\nmeaning %s", Escape(it.Gloss))

	if len(compounds) > 0 {
		b.WriteString("\n\ncompounds\n<pre>")
		for _, w := range compounds[:min(len(compounds), maxCompounds)] {
			fmt.Fprintf(&b, "%s  %s  %s\n",
				Escape(ui.Pad(w.Prompt, 8)),
				Escape(ui.Pad(ui.Truncate(firstReading(w), 12), 12)),
				Escape(ui.Truncate(w.Gloss, 28)))
		}
		b.WriteString("</pre>")
	}
	return b.String()
}

// NoEntry is the answer to a lookup that found nothing.
func NoEntry(target string) string {
	return fmt.Sprintf("no entry for %s", Escape(target))
}

// Banner opens a session with where the queue stands.
func Banner(c store.Counts, cram bool) string {
	label := "review"
	if cram {
		label = "cram · due times ignored"
	}
	return fmt.Sprintf("<b>%s</b>\n%s due · %s in progress · %s burned",
		label, ui.Count(c.Due), ui.Count(c.InProgress), ui.Count(c.Burned))
}

// NothingDue closes the door politely, and points at the one way past it.
func NothingDue(nextDue time.Time, hasNext bool, now time.Time) string {
	if hasNext {
		return fmt.Sprintf("nothing due. next review in %s.\n\n/cram goes through the queue anyway.",
			ui.Duration(nextDue.Sub(now)))
	}
	return "nothing due and nothing scheduled."
}

// Summary closes a session.
func Summary(s *review.Session, c store.Counts, nextDue time.Time, hasNext bool, now time.Time) string {
	var b strings.Builder
	b.WriteString("<b>session</b>")

	if s.Answered == 0 {
		b.WriteString("\nnothing answered")
	} else {
		fmt.Fprintf(&b, "\n%d answered · %d correct · %.0f%% accuracy",
			s.Answered, s.Correct, s.Accuracy()*100)
		if s.Incorrect > 0 {
			fmt.Fprintf(&b, "\n%d missed", s.Incorrect)
		}
		if s.Introduced.Kanji > 0 || s.Introduced.Words > 0 {
			fmt.Fprintf(&b, "\nintroduced %d kanji and %d compounds",
				s.Introduced.Kanji, s.Introduced.Words)
		}
	}

	switch {
	case c.Due > 0:
		fmt.Fprintf(&b, "\n<b>%s still due</b>", ui.Count(c.Due))
	case hasNext:
		fmt.Fprintf(&b, "\nnext review in %s", ui.Duration(nextDue.Sub(now)))
	}
	return b.String()
}

// Stats is the whole picture: coverage of the dictionary, the shape of the
// queue across the ladder, and what the last week looked like.
func Stats(c store.Counts, week store.Activity, nextDue time.Time, hasNext bool, now time.Time) string {
	var b strings.Builder
	b.WriteString("<b>progress</b>\n<pre>")
	fmt.Fprintf(&b, "kanji      %7s / %s\n", ui.Count(c.KanjiInProgress), ui.Count(c.DictKanjiItems))
	fmt.Fprintf(&b, "compounds  %7s / %s\n", ui.Count(c.WordsInProgress), ui.Count(c.DictWordItems))
	b.WriteString("</pre>")

	ladder := strings.Builder{}
	for level := 0; level <= srs.MaxLevel; level++ {
		if c.ByLevel[level] == 0 {
			continue
		}
		fmt.Fprintf(&ladder, "%s %6s\n",
			ui.Pad(fmt.Sprintf("lvl %d %s", level, srs.LevelName(level)), 16),
			ui.Count(c.ByLevel[level]))
	}
	if ladder.Len() > 0 {
		fmt.Fprintf(&b, "\n<pre>%s</pre>", ladder.String())
	}

	fmt.Fprintf(&b, "\n\ndue now <b>%s</b>", ui.Count(c.Due))
	if c.Burned > 0 {
		fmt.Fprintf(&b, "\nburned %s", ui.Count(c.Burned))
	}
	if c.Leeches > 0 {
		fmt.Fprintf(&b, "\nleeches %s (forgotten %d+ times)", ui.Count(c.Leeches), srs.LeechLapses)
	}
	if week.Answered > 0 {
		fmt.Fprintf(&b, "\nlast 7 days %s answers over %d days, %.0f%% correct",
			ui.Count(week.Answered), week.Days, week.Accuracy()*100)
	}
	if c.Due == 0 && hasNext {
		fmt.Fprintf(&b, "\nnext in %s", ui.Duration(nextDue.Sub(now)))
	}
	return b.String()
}

// Help lists what the bot understands.
//
// The commands are left as plain text rather than wrapped in <code>, because
// Telegram turns a bare /command into something tappable and wrapping it would
// take that away.
func Help() string {
	return strings.Join([]string{
		"<b>cli-kanji</b>",
		"",
		"Type the reading — kana or romaji, whatever your keyboard gives you. Anything not starting with a slash is an answer.",
		"",
		"/review — start or resume a session",
		"/cram — ignore due times and work through the queue now",
		"/reveal — show the reading, graded as wrong",
		"/skip — skip without grading",
		"/info 国境 — an entry and the compounds that use it",
		"/stats — progress",
		"/stop — end the session",
	}, "\n")
}

// NoSession is the answer to an answer with nothing to answer.
func NoSession() string { return "no session. /review to start, or /cram to ignore due times." }

// progression describes what an answer did to an item's schedule.
func progression(res review.Result) string {
	before := srs.LevelName(res.Before.Level)
	after := srs.LevelName(res.After.Level)
	move := after
	if before != after {
		move = before + " → " + after
	}
	return fmt.Sprintf("%s · back in %s", move, ui.Duration(time.Until(res.After.DueAt)))
}

// readings lists a card's accepted readings, bounded.
func readings(answers []string) string {
	return strings.Join(limit(answers, maxReadings), ", ")
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
	return strings.Join(limit(out, maxReadings), ", ")
}

func limit(in []string, n int) []string {
	if len(in) <= n {
		return in
	}
	return append(in[:n:n], "…")
}

func firstReading(it store.Item) string {
	if len(it.Answers) == 0 {
		return ""
	}
	return it.Answers[0]
}
