package kana

import (
	"reflect"
	"testing"
)

func TestToHiragana(t *testing.T) {
	cases := []struct{ in, want string }{
		// plain gojuon and the three romanisations that collide
		{"aiueo", "あいうえお"},
		{"shashin", "しゃしん"},
		{"syasin", "しゃしん"},
		{"syashin", "しゃしん"},
		{"chichi", "ちち"},
		{"titi", "ちち"},
		{"tsutsumu", "つつむ"},
		{"tutumu", "つつむ"},
		{"fuji", "ふじ"},
		{"huzi", "ふじ"},

		// sokuon
		{"kitte", "きって"},
		{"gakkou", "がっこう"},
		{"zasshi", "ざっし"},
		{"matcha", "まっちゃ"},
		{"icchi", "いっち"},
		{"ippai", "いっぱい"},

		// syllabic n, and n that is not syllabic
		{"nihon", "にほん"},
		{"nani", "なに"},
		{"onna", "おんな"},
		{"sannen", "さんねん"},
		{"kin'en", "きんえん"},
		{"konnichi", "こんにち"},
		{"nyuugaku", "にゅうがく"},
		{"shinbun", "しんぶん"},
		{"shimbun", "しんぶん"},
		{"sampo", "さんぽ"},

		// already-kana input passes through; katakana folds down
		{"しゃしん", "しゃしん"},
		{"シャシン", "しゃしん"},
		{"コーヒー", "こーひー"},

		// case folding and surrounding space
		{"  Shashin  ", "しゃしん"},
		{"KYOU", "きょう"},

		// unmatched characters survive so a wrong answer looks wrong
		{"xyzq", "xyzq"},
		{"kaq", "かq"},
		{"shashin!", "しゃしん!"},
	}

	for _, c := range cases {
		if got := ToHiragana(c.in); got != c.want {
			t.Errorf("ToHiragana(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeLongVowels(t *testing.T) {
	// Every spelling of the same long vowel must collapse to one form.
	groups := [][]string{
		{"gakkou", "gakkoo", "がっこう", "がっこお"},
		{"kyou", "kyoo", "きょう", "きょお"},
		{"koohii", "kouhii", "コーヒー", "こおひい"},
		{"teinei", "teenee", "ていねい"},
		{"kuuki", "くうき", "クーキ"},
		{"tsuzuku", "tsuduku", "つづく", "つずく"},
	}
	for _, g := range groups {
		want := Normalize(g[0])
		if want == "" {
			t.Fatalf("Normalize(%q) came back empty", g[0])
		}
		for _, v := range g[1:] {
			if got := Normalize(v); got != want {
				t.Errorf("Normalize(%q) = %q, want %q (from %q)", v, got, want, g[0])
			}
		}
	}
}

func TestNormalizeKeepsLengthDistinctions(t *testing.T) {
	// Collapsing long vowels must not make genuinely different readings equal.
	pairs := [][2]string{
		{"koukou", "kokou"},
		{"kuuki", "kuki"},
		{"shoujo", "shojo"},
		{"kitte", "kite"},
		{"onna", "ona"},
	}
	for _, p := range pairs {
		if Normalize(p[0]) == Normalize(p[1]) {
			t.Errorf("Normalize collapsed %q and %q to the same value %q", p[0], p[1], Normalize(p[0]))
		}
	}
}

func TestNormalizeDropsSeparators(t *testing.T) {
	if got, want := Normalize("う.つ"), Normalize("うつ"); got != want {
		t.Errorf("Normalize(%q) = %q, want %q", "う.つ", got, want)
	}
	if got := Normalize("  "); got != "" {
		t.Errorf("Normalize(whitespace) = %q, want empty", got)
	}
}

func TestMatchesAny(t *testing.T) {
	readings := []string{"しゃ", "うつ", "うつす"}

	if r, ok := MatchesAny(readings, "utsusu"); !ok || r != "うつす" {
		t.Errorf("MatchesAny(utsusu) = %q, %v; want うつす, true", r, ok)
	}
	if r, ok := MatchesAny(readings, "シャ"); !ok || r != "しゃ" {
		t.Errorf("MatchesAny(シャ) = %q, %v; want しゃ, true", r, ok)
	}
	if _, ok := MatchesAny(readings, "sha-shin"); ok {
		t.Error("MatchesAny accepted a reading that is not in the list")
	}
	// An empty answer must never be accepted, even against an empty expectation.
	if _, ok := MatchesAny([]string{""}, ""); ok {
		t.Error("MatchesAny accepted an empty answer")
	}
}

func TestVariants(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"シャ", []string{"しゃ"}},
		{"うつ.す", []string{"うつす", "うつ"}},
		{"-がえ.り", []string{"がえり", "がえ"}},
		{"あ.う", []string{"あう", "あ"}},
		{"", nil},
		{"-", nil},
	}
	for _, c := range cases {
		if got := Variants(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Variants(%q) = %#v, want %#v", c.in, got, c.want)
		}
	}
}

func TestVariantsOfDeduplicates(t *testing.T) {
	got := VariantsOf([]string{"うつ.す", "うつ", "シャ", "しゃ"})
	want := []string{"うつす", "うつ", "しゃ"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("VariantsOf = %#v, want %#v", got, want)
	}
}

func TestIsKana(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"しゃしん", true},
		{"コーヒー", true},
		{"写真", false},
		{"shashin", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsKana(c.in); got != c.want {
			t.Errorf("IsKana(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
