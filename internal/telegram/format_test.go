package telegram

import (
	"strings"
	"testing"

	"github.com/OmitNomis/cli-kanji/internal/review"
	"github.com/OmitNomis/cli-kanji/internal/srs"
	"github.com/OmitNomis/cli-kanji/internal/store"
)

func card(level int) store.Card {
	return store.Card{
		Item: store.Item{
			Key:     "w:1321400",
			Kind:    "word",
			Prompt:  "写真",
			Answers: []string{"しゃしん"},
			Hint:    "しゃしん",
			Gloss:   "photograph; photo <picture>",
		},
		State: srs.State{Level: level},
	}
}

func TestEscape(t *testing.T) {
	got := Escape(`a & b <tag> "q"`)
	want := `a &amp; b &lt;tag&gt; "q"`
	if got != want {
		t.Errorf("Escape = %q, want %q", got, want)
	}
}

func TestCardShowsTheReadingOnlyWhileNew(t *testing.T) {
	for level := 0; level <= srs.HintMaxLevel; level++ {
		if msg := Card(1, 10, 9, card(level)); !strings.Contains(msg, "しゃしん") {
			t.Errorf("level %d card has no reading, and a new item has nothing to recall yet:\n%s", level, msg)
		}
	}

	// Above the hint level the reading must not be anywhere in the message --
	// not in a footer, not as a hint about its length.
	for level := srs.HintMaxLevel + 1; level <= srs.MaxLevel; level++ {
		msg := Card(1, 10, 9, card(level))
		if strings.Contains(msg, "しゃしん") {
			t.Errorf("level %d card leaks the reading:\n%s", level, msg)
		}
	}
}

func TestCardCarriesThePromptAndTheQueuePosition(t *testing.T) {
	msg := Card(14, 60, 312, card(4))

	// The prompt is bold, and spaced so each character reads as itself.
	if !strings.Contains(msg, "<b>写 真</b>") {
		t.Errorf("prompt is not bold and spaced:\n%s", msg)
	}
	for _, want := range []string{"14/60", "word", "🍀 familiar", "312 left", "▰▰▱▱▱▱▱▱▱▱"} {
		if !strings.Contains(msg, want) {
			t.Errorf("card is missing %q:\n%s", want, msg)
		}
	}
}

func TestFeedbackEscapesTheGloss(t *testing.T) {
	c := card(2)
	res := review.Result{
		Correct:    true,
		Matched:    "しゃしん",
		Normalized: "しゃしん",
		Before:     srs.State{Level: 2},
		After:      srs.State{Level: 3},
	}

	msg := Correct(res, c)
	if strings.Contains(msg, "<picture>") {
		t.Errorf("an unescaped gloss would be swallowed as a tag:\n%s", msg)
	}
	if !strings.Contains(msg, "&lt;picture&gt;") {
		t.Errorf("the gloss is missing:\n%s", msg)
	}
	if !strings.Contains(msg, "learning → familiar") {
		t.Errorf("the progression is missing:\n%s", msg)
	}
}

func TestWrongNamesTheReadingAndWhatWasTyped(t *testing.T) {
	msg := Wrong(review.Result{
		Normalized: "しゃしま",
		Before:     srs.State{Level: 2},
		After:      srs.State{Level: 1},
	}, card(2))

	for _, want := range []string{"しゃしま", "写真 is <b>しゃしん</b>"} {
		if !strings.Contains(msg, want) {
			t.Errorf("wrong-answer message is missing %q:\n%s", want, msg)
		}
	}
}

func TestWrongSaysSoWhenNothingWasTyped(t *testing.T) {
	msg := Wrong(review.Result{Normalized: ""}, card(2))
	if !strings.Contains(msg, "(nothing)") {
		t.Errorf("a revealed-by-silence answer reads as a blank:\n%s", msg)
	}
}

func TestJoinDropsEmptyBlocks(t *testing.T) {
	if got := Join("one", "", "   ", "two"); got != "one\n\ntwo" {
		t.Errorf("Join = %q, want the empties dropped", got)
	}
	if got := Join("", ""); got != "" {
		t.Errorf("Join of nothing = %q, want empty", got)
	}
}

// The drill is how the program refuses to be multiple choice, so the message
// has to ask for the reading rather than offer it.
func TestDrillAsksForTheReading(t *testing.T) {
	if !strings.Contains(Drill(), "type it") {
		t.Errorf("Drill = %q", Drill())
	}
}

func TestHelpListsTheCommands(t *testing.T) {
	help := Help()
	for _, cmd := range []string{"/review", "/ahead", "/cram", "/reveal", "/skip", "/info", "/stats", "/stop"} {
		if !strings.Contains(help, cmd) {
			t.Errorf("help does not mention %s", cmd)
		}
	}
}

func TestBarNeverRoundsProgressAway(t *testing.T) {
	for _, tc := range []struct {
		have, total int
		want        string
	}{
		{0, 10, "▱▱▱▱▱"},
		{1, 1000, "▰▱▱▱▱"},
		{5, 10, "▰▰▱▱▱"},
		{10, 10, "▰▰▰▰▰"},
		{12, 10, "▰▰▰▰▰"},
		{3, 0, "▱▱▱▱▱"},
	} {
		if got := bar(tc.have, tc.total, 5); got != tc.want {
			t.Errorf("bar(%d, %d) = %s, want %s", tc.have, tc.total, got, tc.want)
		}
	}
}

// Every message goes out in HTML mode, where Telegram rejects the whole message
// over one unbalanced tag. The gloss and the typed answer are the untrusted
// parts, so hostile ones must still come out balanced.
func TestFeedbackTagsBalance(t *testing.T) {
	c := card(2)
	c.Gloss = "</blockquote><b>"
	res := review.Result{Normalized: "<s>", Before: srs.State{Level: 2}, After: srs.State{Level: 1}}
	for _, msg := range []string{Wrong(res, c), Revealed(res, c), Correct(res, c), Info(c.Item, []store.Item{c.Item})} {
		for _, tag := range []string{"b", "i", "s", "code", "blockquote"} {
			open := strings.Count(msg, "<"+tag+">") + strings.Count(msg, "<"+tag+" ")
			if closed := strings.Count(msg, "</"+tag+">"); open != closed {
				t.Errorf("<%s> opened %d times, closed %d:\n%s", tag, open, closed, msg)
			}
		}
	}
}

// The menu must not offer anything the bot does not understand: an unknown
// command falls through to help, which would make a menu entry a dead end.
func TestCommandsAreUnderstood(t *testing.T) {
	for _, c := range Commands() {
		got := review.ParseCommand("/"+c.Command, review.SlashPrefix)
		if got.Kind == review.CmdHelp && c.Command != "help" {
			t.Errorf("/%s is in the menu but parses as an unknown command", c.Command)
		}
		if c.Description == "" || len(c.Description) > 256 {
			t.Errorf("/%s description %q: Telegram wants 1-256 characters", c.Command, c.Description)
		}
	}
}
