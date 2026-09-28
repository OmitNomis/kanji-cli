package kana

import "strings"

// separators are characters that carry no phonetic weight and are dropped
// before comparison. The dot and hyphen come from KANJIDIC2 kun readings
// (う.つ, -がえ.り); the rest are spacing and punctuation noise.
const separators = " \t\r\n.-・〜~（）()「」『』、。/／　"

// Normalize reduces an answer to a canonical hiragana form so that every
// reasonable spelling of the same reading compares equal.
//
// It converts romaji and katakana to hiragana, drops separators, folds the
// obsolete ぢ/づ/ゐ/ゑ onto じ/ず/い/え, and canonicalises long vowels: the
// prolonged mark ー is expanded to the vowel it repeats, おう becomes おお and
// えい becomes ええ. That makes "koohii", "kouhii" and コーヒー identical while
// keeping length intact, so こうこう and ここう stay distinct answers.
func Normalize(s string) string {
	r := []rune(ToHiragana(s))
	var b strings.Builder
	b.Grow(len(r) * 3)

	var prevVowel rune // vowel of the last kana written, 0 if none/unvoweled
	for _, c := range r {
		if strings.ContainsRune(separators, c) {
			continue
		}

		switch c {
		case 'ぢ':
			c = 'じ'
		case 'づ':
			c = 'ず'
		case 'ゐ':
			c = 'い'
		case 'ゑ':
			c = 'え'
		case prolongedMark:
			// ー repeats the previous vowel. With nothing to repeat (after ん,
			// っ or at the start of a string) it is simply dropped.
			expanded, ok := plainVowelKana[prevVowel]
			if !ok {
				continue
			}
			c = expanded
		case 'う':
			// おう, こう, きょう ... are all spelled おお-style internally.
			if prevVowel == 'o' {
				c = 'お'
			}
		case 'い':
			// えい, けい, ねい ... likewise become ええ-style.
			if prevVowel == 'e' {
				c = 'え'
			}
		}

		b.WriteRune(c)
		prevVowel = kanaToVowel[c] // 0 for ん and っ, which is what we want
	}

	return b.String()
}

// Matches reports whether an answer is the expected reading, compared in
// normalized form.
func Matches(expected, answer string) bool {
	n := Normalize(answer)
	return n != "" && n == Normalize(expected)
}

// MatchesAny reports whether an answer matches any of the accepted readings,
// and returns the one it matched.
func MatchesAny(expected []string, answer string) (string, bool) {
	n := Normalize(answer)
	if n == "" {
		return "", false
	}
	for _, e := range expected {
		if Normalize(e) == n {
			return e, true
		}
	}
	return "", false
}
