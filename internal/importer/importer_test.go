package importer

import (
	"reflect"
	"strings"
	"testing"
)

const kanjidicSample = `<?xml version="1.0" encoding="UTF-8"?>
<kanjidic2>
<header><file_version>4</file_version><database_version>2026-279</database_version><date_of_creation>2026-10-06</date_of_creation></header>
<character>
<literal>一</literal>
<misc><grade>1</grade><stroke_count>1</stroke_count><stroke_count>2</stroke_count><freq>2</freq><jlpt>4</jlpt></misc>
<reading_meaning>
<rmgroup>
<reading r_type="pinyin">yi1</reading>
<reading r_type="ja_on">イチ</reading>
<reading r_type="ja_on">イツ</reading>
<reading r_type="ja_kun">ひと-</reading>
<reading r_type="ja_kun">ひと.つ</reading>
<meaning>one</meaning>
<meaning m_lang="fr">un</meaning>
</rmgroup>
<nanori>かず</nanori>
</reading_meaning>
</character>
<character>
<literal>璃</literal>
<misc><grade>9</grade><stroke_count>15</stroke_count></misc>
<reading_meaning><rmgroup><reading r_type="ja_on">リ</reading><meaning>glassy</meaning></rmgroup></reading_meaning>
</character>
<character>
<literal>鬱</literal>
<misc><grade>8</grade><stroke_count>29</stroke_count></misc>
<reading_meaning><rmgroup><reading r_type="ja_on">ウツ</reading><meaning>depression</meaning></rmgroup></reading_meaning>
</character>
</kanjidic2>`

func TestParseKanjidic(t *testing.T) {
	var got []Kanji
	if err := ParseKanjidic(strings.NewReader(kanjidicSample), func(k Kanji) error {
		got = append(got, k)
		return nil
	}); err != nil {
		t.Fatalf("ParseKanjidic: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("parsed %d characters, want 3", len(got))
	}

	one := got[0]
	if one.Literal != "一" || one.Grade != 1 || one.Freq != 2 || one.JLPT != 4 {
		t.Errorf("一 = %+v, want literal 一, grade 1, freq 2, jlpt 4", one)
	}
	if one.StrokeCount != 1 {
		t.Errorf("一 stroke count = %d, want 1 (the first of the repeated values)", one.StrokeCount)
	}
	if want := []string{"イチ", "イツ"}; !reflect.DeepEqual(one.On, want) {
		t.Errorf("一 on readings = %#v, want %#v", one.On, want)
	}
	if want := []string{"ひと-", "ひと.つ"}; !reflect.DeepEqual(one.Kun, want) {
		t.Errorf("一 kun readings = %#v, want %#v", one.Kun, want)
	}
	if want := []string{"one"}; !reflect.DeepEqual(one.Meanings, want) {
		t.Errorf("一 meanings = %#v, want %#v (non-English must be dropped)", one.Meanings, want)
	}
	if want := []string{"かず"}; !reflect.DeepEqual(one.Nanori, want) {
		t.Errorf("一 nanori = %#v, want %#v", one.Nanori, want)
	}

	// Grade 1-6 and 8 are joyo; 9 and 10 are jinmeiyo and must not be.
	if !got[0].IsJoyo() {
		t.Error("grade 1 should be joyo")
	}
	if got[1].IsJoyo() {
		t.Error("grade 9 (jinmeiyo) should not be joyo")
	}
	if !got[2].IsJoyo() {
		t.Error("grade 8 (middle school) should be joyo")
	}
}

// jmdictDoc wraps entries in the DOCTYPE shape JMdict ships, so the parser is
// exercised against the undefined-entity handling it relies on.
func jmdictDoc(entries string) string {
	return `<!DOCTYPE JMdict [<!ENTITY n "noun"><!ENTITY adj-i "adjective"><!ENTITY arch "archaic"><!ENTITY uk "usually kana">]>
<JMdict>` + entries + `</JMdict>`
}

func parseOne(t *testing.T, entry string) (Word, bool) {
	t.Helper()
	var got []Word
	if err := ParseJMdict(strings.NewReader(jmdictDoc(entry)), func(w Word) error {
		got = append(got, w)
		return nil
	}); err != nil {
		t.Fatalf("ParseJMdict: %v", err)
	}
	if len(got) == 0 {
		return Word{}, false
	}
	if len(got) > 1 {
		t.Fatalf("expected at most one word, got %d", len(got))
	}
	return got[0], true
}

func TestParseJMdictBasicEntry(t *testing.T) {
	w, ok := parseOne(t, `<entry>
<ent_seq>1321400</ent_seq>
<k_ele><keb>写真</keb><ke_pri>ichi1</ke_pri><ke_pri>news1</ke_pri><ke_pri>nf05</ke_pri></k_ele>
<r_ele><reb>しゃしん</reb><re_pri>ichi1</re_pri><re_pri>nf05</re_pri></r_ele>
<sense><pos>&n;</pos><gloss>photograph</gloss><gloss>photo</gloss></sense>
<sense><pos>&n;</pos><gloss>snapshot</gloss></sense>
</entry>`)
	if !ok {
		t.Fatal("entry was skipped")
	}

	if w.Seq != 1321400 || w.Surface != "写真" || w.Reading != "しゃしん" {
		t.Errorf("got %+v, want seq 1321400, surface 写真, reading しゃしん", w)
	}
	if want := []string{"photograph; photo", "snapshot"}; !reflect.DeepEqual(w.Glosses, want) {
		t.Errorf("glosses = %#v, want %#v (synonyms joined per sense)", w.Glosses, want)
	}
	if want := []string{"n"}; !reflect.DeepEqual(w.POS, want) {
		t.Errorf("pos = %#v, want %#v (entity punctuation stripped and deduped)", w.POS, want)
	}
	if w.Common != 2 {
		t.Errorf("common = %d, want 2 for an ichi1/news1 entry", w.Common)
	}
	if w.FreqRank != 5 {
		t.Errorf("freqRank = %d, want 5 from nf05", w.FreqRank)
	}
}

func TestParseJMdictSkipsKanaOnlyEntries(t *testing.T) {
	if _, ok := parseOne(t, `<entry>
<ent_seq>1071670</ent_seq>
<r_ele><reb>スナップショット</reb></r_ele>
<sense><pos>&n;</pos><gloss>snapshot</gloss></sense>
</entry>`); ok {
		t.Error("a kana-only entry is not a kanji compound and must be skipped")
	}
}

func TestParseJMdictPrefersCleanSpelling(t *testing.T) {
	// The rare form comes first in the file; the plain form must still be taught,
	// with the rare one kept as an alternate.
	w, ok := parseOne(t, `<entry>
<ent_seq>1000001</ent_seq>
<k_ele><keb>籠もる</keb><ke_inf>&rK;</ke_inf></k_ele>
<k_ele><keb>籠る</keb></k_ele>
<r_ele><reb>こもる</reb></r_ele>
<sense><pos>&n;</pos><gloss>to seclude oneself</gloss></sense>
</entry>`)
	if !ok {
		t.Fatal("entry was skipped")
	}
	if w.Surface != "籠る" {
		t.Errorf("surface = %q, want 籠る (the form not marked rK)", w.Surface)
	}
	if want := []string{"籠もる"}; !reflect.DeepEqual(w.AltSurfaces, want) {
		t.Errorf("altSurfaces = %#v, want %#v", w.AltSurfaces, want)
	}
}

func TestParseJMdictSkipsSearchOnlySpellings(t *testing.T) {
	w, ok := parseOne(t, `<entry>
<ent_seq>1000002</ent_seq>
<k_ele><keb>日本</keb></k_ele>
<k_ele><keb>日本國</keb><ke_inf>&sK;</ke_inf></k_ele>
<r_ele><reb>にほん</reb></r_ele>
<r_ele><reb>にっぽん</reb></r_ele>
<sense><pos>&n;</pos><gloss>Japan</gloss></sense>
</entry>`)
	if !ok {
		t.Fatal("entry was skipped")
	}
	if len(w.AltSurfaces) != 0 {
		t.Errorf("altSurfaces = %#v, want none (sK is search-only)", w.AltSurfaces)
	}
	if want := []string{"にほん", "にっぽん"}; !reflect.DeepEqual(w.Readings, want) {
		t.Errorf("readings = %#v, want %#v (both are valid for 日本)", w.Readings, want)
	}
}

func TestParseJMdictRespectsReadingRestrictions(t *testing.T) {
	// ゆう belongs to 夕 only. Teaching 夕方 must not accept it.
	w, ok := parseOne(t, `<entry>
<ent_seq>1000003</ent_seq>
<k_ele><keb>夕方</keb></k_ele>
<k_ele><keb>夕</keb></k_ele>
<r_ele><reb>ゆうがた</reb><re_restr>夕方</re_restr></r_ele>
<r_ele><reb>ゆう</reb><re_restr>夕</re_restr></r_ele>
<sense><pos>&n;</pos><gloss>evening</gloss></sense>
</entry>`)
	if !ok {
		t.Fatal("entry was skipped")
	}
	if want := []string{"ゆうがた"}; !reflect.DeepEqual(w.Readings, want) {
		t.Errorf("readings = %#v, want %#v (ゆう is restricted to 夕)", w.Readings, want)
	}
}

func TestParseJMdictFallsBackWhenEveryReadingIsRestricted(t *testing.T) {
	// Both readings are restricted to the spelling we did not choose. Dropping
	// the word entirely would be worse than accepting the readings we have.
	w, ok := parseOne(t, `<entry>
<ent_seq>1000004</ent_seq>
<k_ele><keb>彼処</keb></k_ele>
<k_ele><keb>彼所</keb></k_ele>
<r_ele><reb>あそこ</reb><re_restr>彼所</re_restr></r_ele>
<sense><pos>&n;</pos><gloss>there</gloss></sense>
</entry>`)
	if !ok {
		t.Fatal("entry was skipped despite having a usable reading")
	}
	if want := []string{"あそこ"}; !reflect.DeepEqual(w.Readings, want) {
		t.Errorf("readings = %#v, want %#v", w.Readings, want)
	}
}

func TestParseJMdictDropsNoKanjiReadings(t *testing.T) {
	w, ok := parseOne(t, `<entry>
<ent_seq>1000005</ent_seq>
<k_ele><keb>乳母</keb></k_ele>
<r_ele><reb>うば</reb></r_ele>
<r_ele><reb>おんば</reb><re_nokanji/></r_ele>
<sense><pos>&n;</pos><gloss>wet nurse</gloss></sense>
</entry>`)
	if !ok {
		t.Fatal("entry was skipped")
	}
	if want := []string{"うば"}; !reflect.DeepEqual(w.Readings, want) {
		t.Errorf("readings = %#v, want %#v (re_nokanji is not a reading of the kanji form)", w.Readings, want)
	}
}

func TestParseJMdictRespectsSenseRestrictions(t *testing.T) {
	w, ok := parseOne(t, `<entry>
<ent_seq>1000006</ent_seq>
<k_ele><keb>開く</keb></k_ele>
<k_ele><keb>空く</keb></k_ele>
<r_ele><reb>ひらく</reb></r_ele>
<sense><stagk>開く</stagk><pos>&n;</pos><gloss>to open</gloss></sense>
<sense><stagk>空く</stagk><pos>&n;</pos><gloss>to be vacant</gloss></sense>
</entry>`)
	if !ok {
		t.Fatal("entry was skipped")
	}
	if want := []string{"to open"}; !reflect.DeepEqual(w.Glosses, want) {
		t.Errorf("glosses = %#v, want %#v (the 空く sense belongs to another spelling)", w.Glosses, want)
	}
}

func TestParseJMdictDemotesArchaicEntries(t *testing.T) {
	w, ok := parseOne(t, `<entry>
<ent_seq>1000007</ent_seq>
<k_ele><keb>御坐る</keb><ke_pri>news1</ke_pri></k_ele>
<r_ele><reb>おわする</reb><re_pri>news1</re_pri></r_ele>
<sense><pos>&n;</pos><misc>&arch;</misc><gloss>to be</gloss></sense>
</entry>`)
	if !ok {
		t.Fatal("entry was skipped")
	}
	if w.Common != 0 || w.FreqRank != unrankedFreq {
		t.Errorf("common = %d, freqRank = %d; an entry whose every sense is archaic must not be scheduled", w.Common, w.FreqRank)
	}
	if want := []string{"arch"}; !reflect.DeepEqual(w.Tags, want) {
		t.Errorf("tags = %#v, want %#v", w.Tags, want)
	}
}

func TestPriorityTiers(t *testing.T) {
	cases := []struct {
		pri        []string
		wantCommon int
		wantRank   int
	}{
		{[]string{"ichi1", "nf12"}, 2, 12},
		{[]string{"news1"}, 2, 25},
		{[]string{"news2"}, 1, 60},
		{[]string{"spec1", "news2"}, 2, 25},
		{nil, 0, unrankedFreq},
		{[]string{"nf48", "nf03"}, 0, 3},
		{[]string{"nfXX"}, 0, unrankedFreq},
	}
	for _, c := range cases {
		gotCommon, gotRank := priority(c.pri)
		if gotCommon != c.wantCommon || gotRank != c.wantRank {
			t.Errorf("priority(%#v) = (%d, %d), want (%d, %d)", c.pri, gotCommon, gotRank, c.wantCommon, c.wantRank)
		}
	}
}

func TestKanjiRunes(t *testing.T) {
	cases := []struct {
		in   string
		want []rune
	}{
		{"写真", []rune{'写', '真'}},
		{"食べ物", []rune{'食', '物'}},
		{"々", nil},
		{"ひらがな", nil},
		{"人人", []rune{'人'}},
	}
	for _, c := range cases {
		if got := KanjiRunes(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("KanjiRunes(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
