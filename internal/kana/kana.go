// Package kana converts romaji to hiragana and normalises kana for answer
// comparison, so a reviewer can type either "shashin" or しゃしん and have both
// count as the same answer.
package kana

import (
	"strings"
	"unicode"
)

const (
	hiraganaStart = 0x3041
	hiraganaEnd   = 0x3096
	katakanaStart = 0x30A1
	katakanaEnd   = 0x30F6
	// katakanaToHiragana is the fixed offset between the two blocks.
	katakanaToHiragana = katakanaStart - hiraganaStart

	prolongedMark = 'ー' // U+30FC, the long-vowel mark
	sokuon        = 'っ'
	syllabicN     = 'ん'
)

// ToHiragana converts ASCII romaji to hiragana, folds katakana to hiragana and
// passes anything else through untouched. Input is lowercased first, so "Shashin"
// and "shashin" behave identically.
//
// Characters that match no syllable are emitted as-is rather than dropped: a
// typo should produce a visibly wrong answer, not a silently shortened one.
func ToHiragana(s string) string {
	r := []rune(strings.ToLower(strings.TrimSpace(s)))
	var b strings.Builder
	b.Grow(len(s))

	for i := 0; i < len(r); {
		c := r[i]

		// Anything outside ASCII is already Japanese (or punctuation); the only
		// transformation it needs is katakana -> hiragana.
		if c > unicode.MaxASCII {
			b.WriteRune(foldKatakana(c))
			i++
			continue
		}

		// Hepburn spells ん as "m" before b, p and m: shimbun, sampo, ammari.
		if c == 'm' && i+1 < len(r) && (r[i+1] == 'b' || r[i+1] == 'p' || r[i+1] == 'm') {
			b.WriteRune(syllabicN)
			i++
			continue
		}

		// "tch" is how Hepburn doubles ち: matcha -> まっちゃ.
		if c == 't' && i+2 < len(r) && r[i+1] == 'c' && r[i+2] == 'h' {
			b.WriteRune(sokuon)
			i++
			continue
		}

		// Any other doubled consonant is a sokuon: kitte -> きって.
		if i+1 < len(r) && r[i+1] == c && isSokuonConsonant(c) {
			b.WriteRune(sokuon)
			i++
			continue
		}

		// An "n" is syllabic ん unless it opens na/ni/nu/ne/no or nya/nyu/nyo.
		// An apostrophe forces the syllabic reading: "kin'en" -> きんえん.
		if c == 'n' {
			switch {
			case i+1 >= len(r):
				b.WriteRune(syllabicN)
				i++
				continue
			case r[i+1] == '\'':
				b.WriteRune(syllabicN)
				i += 2
				continue
			case !isVowel(r[i+1]) && r[i+1] != 'y':
				b.WriteRune(syllabicN)
				i++
				continue
			}
		}

		// Greedy longest match, so "sho" wins over "s"+"ho".
		matched := false
		for n := maxRomajiLen; n >= 1; n-- {
			if i+n > len(r) {
				continue
			}
			if k, ok := romajiTable[string(r[i:i+n])]; ok {
				b.WriteString(k)
				i += n
				matched = true
				break
			}
		}
		if !matched {
			b.WriteRune(c)
			i++
		}
	}

	return b.String()
}

// foldKatakana maps a katakana rune onto its hiragana counterpart. Runes with
// no counterpart (ー, ・, ヷ-ヺ) are returned unchanged.
func foldKatakana(c rune) rune {
	if c >= katakanaStart && c <= katakanaEnd {
		return c - katakanaToHiragana
	}
	return c
}

// IsKana reports whether every rune in s is hiragana, katakana, ー or ・.
// Used to decide whether a dictionary reading needs no conversion.
func IsKana(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case c >= hiraganaStart && c <= hiraganaEnd:
		case c >= katakanaStart && c <= katakanaEnd:
		case c == prolongedMark || c == '・':
		default:
			return false
		}
	}
	return true
}

func isVowel(c rune) bool {
	switch c {
	case 'a', 'i', 'u', 'e', 'o':
		return true
	}
	return false
}

// isSokuonConsonant reports whether a doubled c means っ. "n" is excluded
// because "nn" is syllabic ん, and vowels are excluded because "oo" is a long
// vowel.
func isSokuonConsonant(c rune) bool {
	if c < 'a' || c > 'z' {
		return false
	}
	return c != 'n' && !isVowel(c)
}
