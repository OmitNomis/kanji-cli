package importer

import (
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
)

// Word is one JMdict entry reduced to a single reviewable compound: the
// preferred kanji spelling, every reading valid for that spelling, and the
// English senses that are not restricted away from it.
type Word struct {
	Seq         int64
	Surface     string
	AltSurfaces []string
	Reading     string   // primary reading, same as Readings[0]
	Readings    []string // every accepted reading, preferred first
	Glosses     []string // one entry per sense, synonyms already joined
	POS         []string
	Tags        []string
	Common      int // 2 = ichi1/news1/spec1/gai1, 1 = tier two, 0 = neither
	FreqRank    int // 1-48 from the nfXX buckets, else 25/60/999
}

// Priority tags. The JMdict tier-one tags cover the ~18k entries dictionaries
// normally print as common; the tier-two tags roughly double that.
var (
	priTier1 = map[string]bool{"ichi1": true, "news1": true, "spec1": true, "gai1": true}
	priTier2 = map[string]bool{"ichi2": true, "news2": true, "spec2": true, "gai2": true}
)

// kebRejected marks spellings that exist for lookup but should not be taught:
// rare, outdated, irregular or ateji forms.
var kebRejected = map[string]bool{
	"rK": true, "oK": true, "iK": true, "io": true, "ateji": true,
}

// archaicTags marks senses not worth drilling however often they were printed.
var archaicTags = map[string]bool{"arch": true, "obs": true}

// unrankedFreq is the freq_rank given to an entry with no priority tag at all.
const unrankedFreq = 999

type xmlKEle struct {
	Keb string   `xml:"keb"`
	Inf []string `xml:"ke_inf"`
	Pri []string `xml:"ke_pri"`
}

type xmlREle struct {
	Reb     string    `xml:"reb"`
	NoKanji *struct{} `xml:"re_nokanji"`
	Restr   []string  `xml:"re_restr"`
	Inf     []string  `xml:"re_inf"`
	Pri     []string  `xml:"re_pri"`
}

type xmlSense struct {
	StagK []string `xml:"stagk"`
	StagR []string `xml:"stagr"`
	POS   []string `xml:"pos"`
	Field []string `xml:"field"`
	Misc  []string `xml:"misc"`
	Gloss []string `xml:"gloss"`
}

type xmlEntry struct {
	Seq   int64      `xml:"ent_seq"`
	KEle  []xmlKEle  `xml:"k_ele"`
	REle  []xmlREle  `xml:"r_ele"`
	Sense []xmlSense `xml:"sense"`
}

// ParseJMdict streams JMdict and calls fn once per entry that has a kanji
// spelling. Entries that are kana-only, or whose every spelling and sense is
// restricted away, are skipped: they are words, but not kanji compounds.
//
// The decoder runs non-strict on purpose. JMdict uses DTD entities for its
// controlled vocabularies, and in non-strict mode Go leaves them as literal
// text, which hands us exactly the short codes we want to store while still
// expanding real entities such as the ampersand.
func ParseJMdict(r io.Reader, fn func(Word) error) error {
	d := xml.NewDecoder(r)
	d.Strict = false

	for {
		tok, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("jmdict: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "entry" {
			continue
		}

		var e xmlEntry
		if err := d.DecodeElement(&e, &se); err != nil {
			return fmt.Errorf("jmdict: decode entry %d: %w", e.Seq, err)
		}

		w, ok := convertEntry(e)
		if !ok {
			continue
		}
		if err := fn(w); err != nil {
			return err
		}
	}
}

// candidate is one kanji spelling of an entry, with enough context to decide
// whether it is the one worth teaching.
type candidate struct {
	text string
	pri  []string
	poor bool // rare, outdated, irregular or ateji
}

// convertEntry picks the spelling to teach, the readings that belong to it, and
// the senses that are not restricted to some other spelling.
func convertEntry(e xmlEntry) (Word, bool) {
	cands := spellings(e)
	if len(cands) == 0 {
		return Word{}, false
	}

	// Teach a clean spelling if the entry offers one.
	chosen := 0
	for i, c := range cands {
		if !c.poor {
			chosen = i
			break
		}
	}
	surface := cands[chosen].text

	var alts []string
	for i, c := range cands {
		if i != chosen {
			alts = append(alts, c.text)
		}
	}

	readings, readingPri := acceptedReadings(e, surface)
	if len(readings) == 0 {
		return Word{}, false
	}

	glosses, pos, tags, archaic := senses(e, surface, readings)
	if len(glosses) == 0 {
		return Word{}, false
	}

	common, rank := priority(append(append([]string{}, cands[chosen].pri...), readingPri...))
	if archaic {
		common, rank = 0, unrankedFreq
	}

	return Word{
		Seq:         e.Seq,
		Surface:     surface,
		AltSurfaces: alts,
		Reading:     readings[0],
		Readings:    readings,
		Glosses:     glosses,
		POS:         pos,
		Tags:        tags,
		Common:      common,
		FreqRank:    rank,
	}, true
}

// spellings returns the entry's kanji-bearing spellings, minus the search-only
// forms that exist purely so a typo still finds the word.
func spellings(e xmlEntry) []candidate {
	var out []candidate
	for _, k := range e.KEle {
		keb := strings.TrimSpace(k.Keb)
		if keb == "" || !hasKanji(keb) || hasEntity(k.Inf, "sK") {
			continue
		}
		out = append(out, candidate{text: keb, pri: k.Pri, poor: anyEntity(k.Inf, kebRejected)})
	}
	return out
}

// acceptedReadings returns every reading valid for surface, preferred first,
// along with the priority tags those readings carried.
//
// Readings marked re_nokanji are dropped because they are explicitly not
// readings of the kanji form, and search-only readings are dropped for the same
// reason the search-only spellings are.
func acceptedReadings(e xmlEntry, surface string) (readings, pri []string) {
	collect := func(respectRestr bool) {
		for _, r := range e.REle {
			reb := strings.TrimSpace(r.Reb)
			if reb == "" || r.NoKanji != nil || hasEntity(r.Inf, "sk") {
				continue
			}
			if respectRestr && len(r.Restr) > 0 && !containsTrimmed(r.Restr, surface) {
				continue
			}
			readings = append(readings, reb)
			pri = append(pri, r.Pri...)
		}
	}

	collect(true)
	if len(readings) == 0 {
		// Every reading was restricted to a spelling we did not choose. Fall
		// back to the unrestricted set rather than dropping the word.
		collect(false)
	}
	return dedupe(readings), pri
}

// senses returns the glosses that survive stagk/stagr restriction, the
// part-of-speech and misc codes they carry, and whether every surviving sense
// is archaic or obsolete.
func senses(e xmlEntry, surface string, readings []string) (glosses, pos, tags []string, archaic bool) {
	archaic = true
	for _, s := range e.Sense {
		if len(s.StagK) > 0 && !containsTrimmed(s.StagK, surface) {
			continue
		}
		if len(s.StagR) > 0 && !anyCommon(s.StagR, readings) {
			continue
		}

		var parts []string
		for _, g := range s.Gloss {
			if g = strings.TrimSpace(g); g != "" {
				parts = append(parts, g)
			}
		}
		if len(parts) == 0 {
			continue
		}

		glosses = append(glosses, strings.Join(parts, "; "))
		pos = append(pos, entityCodes(s.POS)...)
		tags = append(tags, entityCodes(s.Misc)...)
		tags = append(tags, entityCodes(s.Field)...)
		if !anyEntity(s.Misc, archaicTags) {
			archaic = false
		}
	}
	if len(glosses) == 0 {
		archaic = false
	}
	return glosses, dedupe(pos), dedupe(tags), archaic
}

// priority turns a bag of ke_pri/re_pri tags into a tier and a sortable rank.
// The nfXX tags are frequency buckets of 500 words each from a newspaper corpus,
// so they order far better than the coarse tier flags and win when present.
func priority(pri []string) (common, rank int) {
	common, rank = 0, unrankedFreq
	for _, p := range pri {
		p = strings.TrimSpace(p)
		switch {
		case strings.HasPrefix(p, "nf"):
			if n, err := strconv.Atoi(strings.TrimPrefix(p, "nf")); err == nil && n > 0 && n < rank {
				rank = n
			}
		case priTier1[p]:
			if common < 2 {
				common = 2
			}
		case priTier2[p]:
			if common < 1 {
				common = 1
			}
		}
	}
	if rank == unrankedFreq {
		switch common {
		case 2:
			rank = 25
		case 1:
			rank = 60
		}
	}
	return common, rank
}

// KanjiRunes returns the distinct kanji in s, in order of appearance.
func KanjiRunes(s string) []rune {
	var out []rune
	seen := map[rune]bool{}
	for _, r := range s {
		if isKanji(r) && !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

func hasKanji(s string) bool {
	for _, r := range s {
		if isKanji(r) {
			return true
		}
	}
	return false
}

// isKanji reports whether r is a character a learner would call a kanji.
//
// Unicode's Han script includes the CJK Symbols and Punctuation block, which
// holds the iteration mark 々, the abbreviation 〆 and the ideographic zero 〇.
// Counting those as kanji would make 時々 look as though it contained a
// character outside the joyo list, and quietly drop it from a graded queue.
func isKanji(r rune) bool {
	if r >= 0x3000 && r <= 0x303F {
		return false
	}
	return unicode.Is(unicode.Han, r)
}

// entityCode strips the XML entity punctuation JMdict's controlled vocabularies
// arrive wrapped in, turning the literal text of a noun tag into just "n".
func entityCode(s string) string {
	return strings.Trim(strings.TrimSpace(s), "&;")
}

func entityCodes(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if c := entityCode(s); c != "" {
			out = append(out, c)
		}
	}
	return out
}

func hasEntity(list []string, code string) bool {
	for _, s := range list {
		if entityCode(s) == code {
			return true
		}
	}
	return false
}

func anyEntity(list []string, codes map[string]bool) bool {
	for _, s := range list {
		if codes[entityCode(s)] {
			return true
		}
	}
	return false
}

func containsTrimmed(list []string, want string) bool {
	for _, s := range list {
		if strings.TrimSpace(s) == want {
			return true
		}
	}
	return false
}

func anyCommon(a, b []string) bool {
	for _, x := range a {
		if containsTrimmed(b, strings.TrimSpace(x)) {
			return true
		}
	}
	return false
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
