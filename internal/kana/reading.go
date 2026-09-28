package kana

import "strings"

// Variants expands a dictionary reading into every form that should count as
// correct for a bare kanji.
//
// KANJIDIC2 writes kun readings with a dot before the okurigana (うつ.す) and a
// hyphen where the kanji is used as a prefix or suffix (-がえ.り). For a
// single-kanji prompt both the whole word and the part the kanji itself covers
// are fair answers, so うつ.す yields both うつす and うつ.
//
// The result is hiragana, deduplicated, longest form first, and never empty for
// a non-empty reading.
func Variants(raw string) []string {
	raw = strings.Trim(strings.TrimSpace(raw), "-‐−－")
	if raw == "" {
		return nil
	}

	full := ToHiragana(strings.NewReplacer(".", "", "．", "", "-", "").Replace(raw))
	out := []string{}
	if full != "" {
		out = append(out, full)
	}

	// Everything before the first dot is the reading of the kanji proper.
	if i := strings.IndexAny(raw, ".．"); i > 0 {
		if stem := ToHiragana(strings.ReplaceAll(raw[:i], "-", "")); stem != "" && stem != full {
			out = append(out, stem)
		}
	}

	return out
}

// VariantsOf expands every reading in raw and returns the flattened,
// deduplicated result.
func VariantsOf(raw []string) []string {
	seen := make(map[string]bool, len(raw)*2)
	out := make([]string, 0, len(raw)*2)
	for _, r := range raw {
		for _, v := range Variants(r) {
			if !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	return out
}
