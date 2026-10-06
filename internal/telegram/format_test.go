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

	// The prompt is monospaced so a phone does not reflow it, and spaced so
	// each character reads as itself.
	if !strings.Contains(msg, "<code>写 真</code>") {
		t.Errorf("prompt is not monospaced and spaced:\n%s", msg)
	}
	for _, want := range []string{"14/60", "word", "familiar", "312 left"} {
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
	for _, cmd := range []string{"/review", "/cram", "/reveal", "/skip", "/info", "/stats", "/stop"} {
		if !strings.Contains(help, cmd) {
			t.Errorf("help does not mention %s", cmd)
		}
	}
}
