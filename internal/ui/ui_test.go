package ui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/OmitNomis/cli-kanji/internal/review"
	"github.com/OmitNomis/cli-kanji/internal/srs"
	"github.com/OmitNomis/cli-kanji/internal/store"
)

func TestWidthCountsJapaneseAsTwoColumns(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"写真", 4},
		{"しゃしん", 8},
		{"１月", 4},  // fullwidth digit plus kanji
		{"a写", 3},  // mixed
		{"写 真", 5}, // the space is one column
		{"──", 2},  // box drawing is narrow
		{"時々", 4},  // the iteration mark is still wide
	}
	for _, c := range cases {
		if got := Width(c.in); got != c.want {
			t.Errorf("Width(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestPadUsesColumnsNotBytes(t *testing.T) {
	// 日本人 is three runes, nine bytes and six columns. Padding on anything
	// but columns misaligns every table in the interface.
	got := Pad("日本人", 10)
	if Width(got) != 10 {
		t.Errorf("Pad(日本人, 10) has width %d, want 10", Width(got))
	}
	if !strings.HasPrefix(got, "日本人") {
		t.Errorf("Pad lost the content: %q", got)
	}
	// Already wide enough: leave it alone rather than truncating.
	if got := Pad("日本人", 4); got != "日本人" {
		t.Errorf("Pad(日本人, 4) = %q, want it unchanged", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("photograph", 20); got != "photograph" {
		t.Errorf("Truncate = %q, want it unchanged", got)
	}
	got := Truncate("photograph; photo; picture", 12)
	if Width(got) > 12 {
		t.Errorf("Truncate = %q, width %d, want at most 12", got, Width(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("Truncate = %q, want it to end in an ellipsis", got)
	}

	// Cutting must land on a rune boundary, never inside a multi-byte kanji.
	cut := Truncate("日本語の勉強", 7)
	if Width(cut) > 7 {
		t.Errorf("Truncate = %q, width %d, want at most 7", cut, Width(cut))
	}
	for _, r := range cut {
		if r == '\uFFFD' {
			t.Errorf("Truncate split a rune: %q", cut)
		}
	}
	if got := Truncate("日本", 0); got != "" {
		t.Errorf("Truncate(_, 0) = %q, want empty", got)
	}
}

func TestSpacedSeparatesOnlyAdjacentKanji(t *testing.T) {
	cases := []struct{ in, want string }{
		{"写真", "写 真"},
		{"日本語", "日 本 語"},
		{"一つ", "一つ"},   // okurigana belongs to the kanji
		{"食べ物", "食べ物"}, // the kanji are not adjacent
		{"１月", "１月"},   // a fullwidth digit is not a kanji
		{"験", "験"},
		{"時々", "時々"}, // the iteration mark is not a kanji
		{"", ""},
	}
	for _, c := range cases {
		if got := Spaced(c.in); got != c.want {
			t.Errorf("Spaced(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{-time.Hour, "now"},
		{30 * time.Second, "under a minute"},
		{10 * time.Minute, "10m"},
		{4 * time.Hour, "4h"},
		{3 * 24 * time.Hour, "3d"},
		{180 * 24 * time.Hour, "6mo"},
		// Nothing the ladder produces lands here, but a due date from somewhere
		// else should not read as "3166mo".
		{100 * 365 * 24 * time.Hour, "100y"},
	}
	for _, c := range cases {
		if got := Duration(c.in); got != c.want {
			t.Errorf("Duration(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCountGroupsThousands(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1,000"},
		{25406, "25,406"},
		{2136, "2,136"},
	}
	for _, c := range cases {
		if got := Count(c.in); got != c.want {
			t.Errorf("Count(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestScaleNeverRoundsProgressAwayToNothing(t *testing.T) {
	// One kanji out of 2,136 is 0.01 of a 24-cell bar. Showing an empty bar
	// would say no progress had been made, which is false.
	if got := scale(1, 2136, 24); got != 1 {
		t.Errorf("scale(1, 2136, 24) = %d, want 1", got)
	}
	if got := scale(0, 2136, 24); got != 0 {
		t.Errorf("scale(0, _, _) = %d, want 0", got)
	}
	if got := scale(2136, 2136, 24); got != 24 {
		t.Errorf("scale at full = %d, want 24", got)
	}
	// Overshoot and division by zero must not produce a bar longer than the box.
	if got := scale(5000, 2136, 24); got != 24 {
		t.Errorf("scale past full = %d, want it clamped to 24", got)
	}
	if got := scale(5, 0, 24); got != 0 {
		t.Errorf("scale with no total = %d, want 0", got)
	}
}

func TestPluralAvoidsOneCompounds(t *testing.T) {
	if got := plural(1, "compound", "compounds"); got != "1 compound" {
		t.Errorf("plural(1) = %q, want \"1 compound\"", got)
	}
	if got := plural(0, "compound", "compounds"); got != "0 compounds" {
		t.Errorf("plural(0) = %q, want \"0 compounds\"", got)
	}
	if got := plural(1200, "compound", "compounds"); got != "1,200 compounds" {
		t.Errorf("plural(1200) = %q, want \"1,200 compounds\"", got)
	}
}

func TestRuleFitsTheWidth(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf, 60, false)

	r.Rule("14/60 · word · learning", "312 left")
	line := strings.TrimRight(buf.String(), "\n")
	if got := Width(line); got != 60 {
		t.Errorf("rule width = %d, want 60: %q", got, line)
	}
	if !strings.Contains(line, "312 left") {
		t.Errorf("rule lost its note: %q", line)
	}

	// A label too long for the width must not produce a negative repeat count.
	buf.Reset()
	r.Rule(strings.Repeat("x", 200), "note")
	if buf.Len() == 0 {
		t.Error("an over-long label produced no output")
	}
}

func TestOtherReadingsExcludesTheMatchAndIsBounded(t *testing.T) {
	answers := []string{"にち", "じつ", "ひ", "び", "か"}
	got := otherReadings(answers, "にち")
	if strings.Contains(got, "にち") {
		t.Errorf("otherReadings = %q, should not repeat the matched reading", got)
	}
	if got != "じつ, ひ, び, か" {
		t.Errorf("otherReadings = %q, want \"じつ, ひ, び, か\"", got)
	}
	if got := otherReadings([]string{"ご"}, "ご"); got != "" {
		t.Errorf("otherReadings with nothing left = %q, want empty", got)
	}

	many := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
	if n := strings.Count(otherReadings(many, "a"), ",") + 1; n > maxOtherReadings+1 {
		t.Errorf("otherReadings listed %d items, want at most %d plus an ellipsis", n, maxOtherReadings)
	}
}

func TestLimitDoesNotCorruptTheInput(t *testing.T) {
	in := []string{"a", "b", "c", "d"}
	got := limit(in, 2)
	if len(got) != 3 || got[2] != "…" {
		t.Fatalf("limit = %v, want [a b …]", got)
	}
	// The ellipsis must not have been written over the caller's slice.
	if in[2] != "c" {
		t.Errorf("limit overwrote its input: %v", in)
	}
}

func TestCardNamesTheKindAndHintsOnlyWhenNew(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf, 72, false)

	// A new word: the kind and the reading both have to be on screen.
	r.Card(1, 10, 9, store.Card{
		Item: store.Item{
			Kind: "word", Prompt: "写真", Answers: []string{"しゃしん"},
			Hint: "しゃしん", Gloss: "photograph",
		},
		State: srs.State{Level: 0},
		New:   true,
	})
	out := buf.String()
	for _, want := range []string{"word", "写 真", "しゃしん", "1/10", "9 left"} {
		if !strings.Contains(out, want) {
			t.Errorf("new word card is missing %q:\n%s", want, out)
		}
	}

	// The same card once learned: the reading must be gone, or it is not a test.
	buf.Reset()
	r.Card(2, 10, 0, store.Card{
		Item: store.Item{
			Kind: "word", Prompt: "写真", Answers: []string{"しゃしん"},
			Hint: "しゃしん", Gloss: "photograph",
		},
		State: srs.State{Level: srs.HintMaxLevel + 1},
	})
	out = buf.String()
	if strings.Contains(out, "しゃしん") {
		t.Errorf("a learned card gave away its reading:\n%s", out)
	}
	if !strings.Contains(out, "写 真") {
		t.Errorf("card lost its prompt:\n%s", out)
	}

	// A kanji card must say so, and say which grade.
	buf.Reset()
	r.Card(1, 1, 0, store.Card{
		Item:  store.Item{Kind: "kanji", Prompt: "験", Grade: 4, Answers: []string{"けん"}},
		State: srs.State{Level: 5},
	})
	out = buf.String()
	if !strings.Contains(out, "kanji") || !strings.Contains(out, "grade 4") {
		t.Errorf("kanji card does not name its kind and grade:\n%s", out)
	}
}

func TestWrongNamesWhatWasTypedAndWhatWasRight(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf, 72, false)

	card := store.Card{
		Item:  store.Item{Kind: "kanji", Prompt: "日", Answers: []string{"にち", "ひ"}, Gloss: "day"},
		State: srs.State{Level: 3},
	}
	res := review.Result{
		Normalized: "ほん",
		Before:     srs.State{Level: 3},
		After:      srs.State{Level: 1, DueAt: time.Now().Add(4 * time.Hour)},
	}
	r.Wrong(res, card)

	out := buf.String()
	for _, want := range []string{"ほん", "日 is", "にち", "familiar → learning"} {
		if !strings.Contains(out, want) {
			t.Errorf("wrong-answer output is missing %q:\n%s", want, out)
		}
	}

	// An empty answer has to be reported as such rather than as a blank line.
	buf.Reset()
	r.Wrong(review.Result{Before: res.Before, After: res.After}, card)
	if !strings.Contains(buf.String(), "(nothing)") {
		t.Errorf("an empty answer was not reported:\n%s", buf.String())
	}
}

func TestWrapKeepsWordsAndReadingsWhole(t *testing.T) {
	got := Wrap("photograph; photo; picture; snapshot", 20)
	want := []string{"photograph; photo;", "picture; snapshot"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("Wrap = %q, want %q", got, want)
	}

	// A kanji's readings have no spaces between them, only ・, and must still
	// break between readings rather than through one.
	for _, l := range Wrap("い.きる・い.かす・い.ける・う.まれる", 16) {
		if Width(l) > 16 {
			t.Errorf("line %q is %d columns, want at most 16", l, Width(l))
		}
		if strings.HasPrefix(l, ".") || strings.HasSuffix(l, ".") {
			t.Errorf("line %q splits a reading", l)
		}
	}

	// A run with nowhere to break is cut, but nothing is lost.
	long := Wrap("supercalifragilistic", 8)
	if strings.Join(long, "") != "supercalifragilistic" {
		t.Errorf("Wrap lost text: %q", long)
	}
	if Wrap("", 10) != nil {
		t.Errorf("Wrap of nothing should be no lines")
	}
}

func TestLongGlossIsWrappedNotCut(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf, 40, false)

	gloss := "to be; to exist; to live; to be located; to be equipped with; to happen"
	card := store.Card{
		Item:  store.Item{Kind: "word", Prompt: "有る", Answers: []string{"ある"}, Gloss: gloss},
		State: srs.State{Level: 3},
	}
	res := review.Result{
		Matched: "ある",
		Before:  srs.State{Level: 3},
		After:   srs.State{Level: 4, DueAt: time.Now().Add(72 * time.Hour)},
	}
	r.Correct(res, card)
	r.Wrong(res, card)
	r.Revealed(res, card)

	out := buf.String()
	if strings.Contains(out, "…") {
		t.Errorf("a gloss was truncated:\n%s", out)
	}
	if n := strings.Count(out, "happen"); n != 3 {
		t.Errorf("the end of the gloss appears %d times, want 3:\n%s", n, out)
	}
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if Width(l) > 40 {
			t.Errorf("line is %d columns, want at most 40: %q", Width(l), l)
		}
	}
}

func TestStyleRespectsNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if s := NewStyle(false); s.Enabled() {
		t.Error("NO_COLOR should disable colour")
	}
	// An explicit request still wins, for piping into something that renders it.
	if s := NewStyle(true); !s.Enabled() {
		t.Error("forced colour should stay on")
	}

	plain := Style{}
	if got := plain.Green("ok"); got != "ok" {
		t.Errorf("colourless style emitted escapes: %q", got)
	}
	coloured := Style{on: true}
	if got := coloured.Green("ok"); !strings.Contains(got, "\x1b[32m") {
		t.Errorf("coloured style emitted no escape: %q", got)
	}
	// Empty text must not be wrapped; a stray reset sequence is just noise.
	if got := coloured.Green(""); got != "" {
		t.Errorf("empty text was wrapped: %q", got)
	}
}
