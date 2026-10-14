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

// maxCompounds bounds the compound list in a lookup. It starts collapsed, but
// opened on a phone a long one is still a long scroll.
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
	var b strings.Builder
	fmt.Fprintf(&b, "%s  ·  %s", kindBadge(c.Item), levelBadge(c.State.Level))

	// Bold rather than monospace: Telegram sets code a size smaller, and the
	// prompt is the one thing in the message that has to read at a glance.
	fmt.Fprintf(&b, "\n\n<b>%s</b>", Escape(ui.Spaced(c.Prompt)))

	if srs.ShowHint(c.State.Level) && c.Hint != "" {
		fmt.Fprintf(&b, "\n💡 <i>%s</i>", Escape(c.Hint))
	}

	fmt.Fprintf(&b, "\n\n<code>%s</code>  %d/%d", bar(position-1, total, 10), position, total)
	if remaining > 0 {
		fmt.Fprintf(&b, " · %s left", ui.Count(remaining))
	}
	return b.String()
}

// Correct reports a right answer.
func Correct(res review.Result, c store.Card) string {
	var b strings.Builder
	fmt.Fprintf(&b, "✅ <b>%s</b>", Escape(res.Matched))
	if others := otherReadings(c.Answers, res.Matched); others != "" {
		fmt.Fprintf(&b, "  <i>also %s</i>", Escape(others))
	}
	b.WriteString(gloss(c.Gloss))
	fmt.Fprintf(&b, "\n%s", progression(res))
	return b.String()
}

// Wrong reports a wrong answer and what the reading actually was.
func Wrong(res review.Result, c store.Card) string {
	typed := "<i>(nothing)</i>"
	if res.Normalized != "" {
		typed = "<s>" + Escape(res.Normalized) + "</s>"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "❌ %s", typed)
	fmt.Fprintf(&b, "\n%s is <b>%s</b>", Escape(c.Prompt), Escape(readings(c.Answers)))
	b.WriteString(gloss(c.Gloss))
	fmt.Fprintf(&b, "\n%s", progression(res))
	return b.String()
}

// Revealed reports an answer the reviewer asked to be shown, which is graded as
// wrong because being told is not knowing.
func Revealed(res review.Result, c store.Card) string {
	var b strings.Builder
	fmt.Fprintf(&b, "👀 %s is <b>%s</b>", Escape(c.Prompt), Escape(readings(c.Answers)))
	b.WriteString(gloss(c.Gloss))
	fmt.Fprintf(&b, "\n%s", progression(res))
	return b.String()
}

// Drill asks for the reading that was just missed.
//
// A chat makes this natural -- it reads as the next thing being asked for --
// and it preserves the one rule the program is built around: you produce the
// kana, or you do not. There is no button to press instead, because tapping a
// button is multiple choice.
func Drill() string { return "✍️ <b>type it to carry on</b>  ·  /skip to move on" }

// Mistyped tells the reviewer a retype did not match, naming one reading to aim
// at. The reading is already on screen by this point, so nothing is given away.
func Mistyped(c store.Card) string {
	if len(c.Answers) == 0 {
		return "❌ not that"
	}
	return fmt.Sprintf("❌ not quite, type <b>%s</b>", Escape(c.Answers[0]))
}

// Retyped acknowledges a successful retype without re-reporting the answer.
func Retyped() string { return "👍 got it" }

// Skipped acknowledges a card passed over, so a skip is never silent.
func Skipped() string { return "⏭ skipped" }

// Info is a dictionary entry and the compounds that use it.
func Info(it store.Item, compounds []store.Item) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<b>%s</b>  ·  %s", Escape(it.Prompt), kindBadge(it))
	fmt.Fprintf(&b, "\n\n🔊 <b>%s</b>", Escape(strings.Join(it.Answers, ", ")))

	// Only a kanji's hint adds anything: it keeps the on/kun split and the
	// okurigana markers that the flattened answer list throws away.
	if it.IsKanji() && it.Hint != "" {
		fmt.Fprintf(&b, "\n<i>%s</i>", Escape(it.Hint))
	}
	b.WriteString(gloss(it.Gloss))

	if len(compounds) > 0 {
		// Expandable, so a lookup in the middle of a session does not push the
		// card being answered off the screen.
		b.WriteString("\n\n📚 <b>compounds</b>\n<blockquote expandable>")
		for i, w := range compounds[:min(len(compounds), maxCompounds)] {
			if i > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "<b>%s</b> %s · <i>%s</i>",
				Escape(w.Prompt), Escape(firstReading(w)), Escape(ui.Truncate(w.Gloss, 40)))
		}
		b.WriteString("</blockquote>")
	}
	return b.String()
}

// NoEntry is the answer to a lookup that found nothing.
func NoEntry(target string) string {
	return fmt.Sprintf("🤷 no entry for %s", Escape(target))
}

// Banner opens a session with where the queue stands.
func Banner(c store.Counts, cram bool) string {
	label := "🎴 <b>review</b>"
	if cram {
		label = "⚡ <b>cram</b> · due times ignored"
	}
	return fmt.Sprintf("%s\n🔔 %s due · 📚 %s in progress · 🔥 %s burned",
		label, ui.Count(c.Due), ui.Count(c.InProgress), ui.Count(c.Burned))
}

// NothingDue closes the door politely, and points at the one way past it.
func NothingDue(nextDue time.Time, hasNext bool, now time.Time) string {
	if hasNext {
		return fmt.Sprintf("😴 nothing due. next review in <b>%s</b>.\n\n/cram goes through the queue anyway.",
			ui.Duration(nextDue.Sub(now)))
	}
	return "😴 nothing due and nothing scheduled."
}

// Summary closes a session.
func Summary(s *review.Session, c store.Counts, nextDue time.Time, hasNext bool, now time.Time) string {
	var b strings.Builder
	b.WriteString("🏁 <b>session done</b>")

	if s.Answered == 0 {
		b.WriteString("\nnothing answered")
	} else {
		fmt.Fprintf(&b, "\n✅ %d correct · ❌ %d missed", s.Correct, s.Incorrect)
		fmt.Fprintf(&b, "\n🎯 <code>%s</code> %.0f%% of %d",
			bar(s.Correct, s.Answered, 10), s.Accuracy()*100, s.Answered)
		if s.Introduced.Kanji > 0 || s.Introduced.Words > 0 {
			fmt.Fprintf(&b, "\n🌱 introduced %d kanji and %d compounds",
				s.Introduced.Kanji, s.Introduced.Words)
		}
	}

	switch {
	case c.Due > 0:
		fmt.Fprintf(&b, "\n🔔 <b>%s still due</b>", ui.Count(c.Due))
	case hasNext:
		fmt.Fprintf(&b, "\n⏰ next review in %s", ui.Duration(nextDue.Sub(now)))
	}
	return b.String()
}

// Stats is the whole picture: coverage of the dictionary, the shape of the
// queue across the ladder, and what the last week looked like.
func Stats(c store.Counts, week store.Activity, nextDue time.Time, hasNext bool, now time.Time) string {
	var b strings.Builder
	b.WriteString("📊 <b>progress</b>\n")
	fmt.Fprintf(&b, "\n✍️ kanji <code>%s</code> %s / %s",
		bar(c.KanjiInProgress, c.DictKanjiItems, 10), ui.Count(c.KanjiInProgress), ui.Count(c.DictKanjiItems))
	fmt.Fprintf(&b, "\n📖 words <code>%s</code> %s / %s",
		bar(c.WordsInProgress, c.DictWordItems, 10), ui.Count(c.WordsInProgress), ui.Count(c.DictWordItems))

	// The ladder groups levels by name, which is what a level means to the
	// reviewer: ten numbers would be noise.
	var ladder []string
	name, n := "", 0
	flush := func() {
		if n > 0 {
			ladder = append(ladder, fmt.Sprintf("%s %s <b>%s</b>", levelEmoji(name), name, ui.Count(n)))
		}
	}
	for level := 0; level <= srs.MaxLevel; level++ {
		if ln := srs.LevelName(level); ln != name {
			flush()
			name, n = ln, 0
		}
		n += c.ByLevel[level]
	}
	flush()
	if len(ladder) > 0 {
		fmt.Fprintf(&b, "\n\n%s", strings.Join(ladder, "\n"))
	}

	fmt.Fprintf(&b, "\n\n🔔 due now <b>%s</b>", ui.Count(c.Due))
	if c.Due == 0 && hasNext {
		fmt.Fprintf(&b, " · next in %s", ui.Duration(nextDue.Sub(now)))
	}
	if c.Leeches > 0 {
		fmt.Fprintf(&b, "\n🐛 %s leeches <i>(forgotten %d+ times)</i>", ui.Count(c.Leeches), srs.LeechLapses)
	}
	if week.Answered > 0 {
		fmt.Fprintf(&b, "\n📅 last 7 days: %s answers over %d days, %.0f%% correct",
			ui.Count(week.Answered), week.Days, week.Accuracy()*100)
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
		"🎴 <b>cli-kanji</b>",
		"",
		"Type the reading — kana or romaji, whatever your keyboard gives you. Anything not starting with a slash is an answer.",
		"",
		"▶️ /review — start or resume a session",
		"⚡ /cram — ignore due times and work through the queue now",
		"👀 /reveal — show the reading, graded as wrong",
		"⏭ /skip — skip without grading",
		"🔍 /info 国境 — an entry and the compounds that use it",
		"📊 /stats — progress",
		"⏹ /stop — end the session",
	}, "\n")
}

// Reminder says a session is waiting. It is the one message the bot sends
// unasked, so it says how big the session is and nothing else.
func Reminder(due, fresh int) string {
	var parts []string
	if due > 0 {
		parts = append(parts, ui.Count(due)+" due")
	}
	if fresh > 0 {
		parts = append(parts, ui.Count(fresh)+" new")
	}
	noun := "cards"
	if due+fresh == 1 {
		noun = "card"
	}
	return fmt.Sprintf("🔔 <b>%s %s waiting</b> · %s\n\n/review to start",
		ui.Count(due+fresh), noun, strings.Join(parts, " · "))
}

// NoSession is the answer to an answer with nothing to answer.
func NoSession() string {
	return "💤 no session. /review to start, or /cram to ignore due times."
}

// kindBadge names what a prompt is. Without it a bare 本 is ambiguous in a way
// that changes the right answer: as a kanji it accepts ほん or もと, as a word
// only ほん.
func kindBadge(it store.Item) string {
	if it.IsKanji() {
		return fmt.Sprintf("✍️ kanji · grade %d", it.Grade)
	}
	return "📖 word"
}

// levelBadge is a level's name with its emoji.
func levelBadge(level int) string {
	name := srs.LevelName(level)
	return levelEmoji(name) + " " + name
}

// levelEmoji gives each rung of the ladder a picture that grows as the item
// does, so how well a card is known reads before any text does.
func levelEmoji(name string) string {
	switch name {
	case "new":
		return "🌱"
	case "learning":
		return "🌿"
	case "familiar":
		return "🍀"
	case "known":
		return "🌳"
	case "solid":
		return "💎"
	case "burned":
		return "🔥"
	}
	return "·"
}

// gloss sets a meaning off as a quote, which Telegram draws with a bar down the
// side: what the word means stays apart from what you typed.
func gloss(s string) string {
	if s == "" {
		return ""
	}
	return "\n<blockquote>" + Escape(s) + "</blockquote>"
}

// bar draws have/total as a row of cells, never rounding progress that exists
// down to nothing.
func bar(have, total, cells int) string {
	filled := 0
	if total > 0 && have > 0 {
		filled = max(1, min(cells, have*cells/total))
	}
	return strings.Repeat("▰", filled) + strings.Repeat("▱", cells-filled)
}

// progression describes what an answer did to an item's schedule, as HTML.
func progression(res review.Result) string {
	before := srs.LevelName(res.Before.Level)
	after := srs.LevelName(res.After.Level)
	move := after
	if before != after {
		move = before + " → " + after
	}
	return fmt.Sprintf("%s %s · ⏰ back in %s", levelEmoji(after), Escape(move), ui.Duration(time.Until(res.After.DueAt)))
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
