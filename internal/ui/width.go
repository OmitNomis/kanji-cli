package ui

import (
	"strings"
	"unicode"

	"github.com/OmitNomis/cli-kanji/internal/kana"
)

// Width returns how many terminal columns s occupies.
//
// Every character this program displays prominently is a kanji or kana, and
// those are double-width. Measuring them as one column each would make every
// rule and every right-aligned tag in the interface wrong by the number of
// Japanese characters on the line.
func Width(s string) int {
	w := 0
	for _, r := range s {
		w += RuneWidth(r)
	}
	return w
}

// RuneWidth returns the column count of a single rune: zero for combining
// marks, two for East Asian wide and fullwidth characters, one otherwise.
func RuneWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Me, r):
		// Combining marks hang off the previous character.
		return 0
	case isWide(r):
		return 2
	default:
		return 1
	}
}

// wideRanges are the East Asian Wide and Fullwidth blocks, which is all this
// program needs: CJK ideographs, kana, the fullwidth Latin forms JMdict uses in
// entries such as １月, and the CJK punctuation around them.
var wideRanges = [...][2]rune{
	{0x1100, 0x115F},   // Hangul Jamo
	{0x2E80, 0x303E},   // CJK radicals, Kangxi, CJK punctuation
	{0x3041, 0x33FF},   // kana, Bopomofo, Hangul compatibility, CJK compatibility
	{0x3400, 0x4DBF},   // CJK Extension A
	{0x4E00, 0x9FFF},   // CJK Unified Ideographs
	{0xA000, 0xA4CF},   // Yi
	{0xAC00, 0xD7A3},   // Hangul syllables
	{0xF900, 0xFAFF},   // CJK compatibility ideographs
	{0xFE30, 0xFE6F},   // CJK compatibility forms
	{0xFF00, 0xFF60},   // fullwidth Latin and punctuation
	{0xFFE0, 0xFFE6},   // fullwidth currency signs
	{0x1F300, 0x1F64F}, // emoji, in case a gloss contains one
	{0x1F900, 0x1F9FF},
	{0x20000, 0x3FFFD}, // CJK Extensions B and beyond
}

func isWide(r rune) bool {
	for _, rg := range wideRanges {
		if r >= rg[0] && r <= rg[1] {
			return true
		}
	}
	return false
}

// Pad right-pads s with spaces to at least n columns.
func Pad(s string, n int) string {
	if gap := n - Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

// Truncate shortens s to at most n columns, ending in an ellipsis when it had
// to cut. Cutting happens on rune boundaries, never inside one.
func Truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if Width(s) <= n {
		return s
	}

	// Leave room for the ellipsis, which is itself one column.
	limit := n - 1
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := RuneWidth(r)
		if w+rw > limit {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String() + "…"
}

// Spaced separates adjacent kanji with a space, which is how a prompt is
// displayed: 写真 becomes 写 真 so each character reads as itself rather than as
// half of a block.
//
// Only kanji-to-kanji boundaries get a space. Okurigana and kana belong to the
// character they follow, so 一つ and 食べ物 are left alone rather than being
// scattered into 一 つ and 食 べ 物.
func Spaced(s string) string {
	var b strings.Builder
	var prev rune
	for i, r := range s {
		if i > 0 && kana.IsKanji(prev) && kana.IsKanji(r) {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
		prev = r
	}
	return b.String()
}

// Wrap breaks s into lines of at most n columns. Lines break after a space or
// a ・, which is what separates a kanji's readings in its hint, so neither a word
// nor a reading is ever split while it fits on a line of its own. Only a run
// with no break in it wider than n is cut, wherever it has to be.
func Wrap(s string, n int) []string {
	if n < 1 {
		n = 1
	}
	var (
		lines []string
		line  []rune
		w     int
		brk   int // len(line) just after the last break opportunity, 0 for none
	)
	flush := func(cut int) {
		lines = append(lines, strings.TrimRight(string(line[:cut]), " "))
		rest := append([]rune(nil), line[cut:]...)
		line, w, brk = rest, Width(string(rest)), 0
	}
	for _, r := range s {
		rw := RuneWidth(r)
		for w+rw > n && len(line) > 0 {
			if r == ' ' || brk == 0 {
				flush(len(line))
			} else {
				flush(brk)
			}
		}
		if r == ' ' && len(line) == 0 {
			continue
		}
		line = append(line, r)
		w += rw
		if r == ' ' || r == '・' {
			brk = len(line)
		}
	}
	if len(line) > 0 {
		lines = append(lines, strings.TrimRight(string(line), " "))
	}
	return lines
}
