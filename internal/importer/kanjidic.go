// Package importer turns the EDRDG dictionary XML into the prebuilt SQLite
// dictionary the review loop reads. It streams both files element by element
// rather than loading them, partly on principle and partly because JMdict is
// 63 MB of XML.
package importer

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// Kanji is one KANJIDIC2 character, reduced to the fields this program uses.
type Kanji struct {
	Literal     string
	Grade       int // 1-6 elementary, 8 remaining joyo, 9/10 jinmeiyo, 0 none
	StrokeCount int
	Freq        int // newspaper frequency rank; 0 when unranked
	JLPT        int
	Meanings    []string // English only
	On          []string // katakana, raw KANJIDIC form
	Kun         []string // hiragana, with . and - markers intact
	Nanori      []string
}

// IsJoyo reports whether the kanji is one of the 2,136 characters on the joyo
// list: grades 1-6 are the kyoiku kanji taught in elementary school, grade 8 is
// the remainder taught from middle school on. Grades 9 and 10 are jinmeiyo and
// are imported but never scheduled.
func (k Kanji) IsJoyo() bool {
	return (k.Grade >= 1 && k.Grade <= 6) || k.Grade == 8
}

type xmlKanjidicReading struct {
	Type  string `xml:"r_type,attr"`
	Value string `xml:",chardata"`
}

type xmlKanjidicMeaning struct {
	Lang  string `xml:"m_lang,attr"`
	Value string `xml:",chardata"`
}

type xmlCharacter struct {
	Literal string `xml:"literal"`
	Misc    struct {
		Grade int `xml:"grade"`
		// stroke_count repeats when sources disagree; the first is the accepted one.
		StrokeCount []int `xml:"stroke_count"`
		Freq        int   `xml:"freq"`
		JLPT        int   `xml:"jlpt"`
	} `xml:"misc"`
	ReadingMeaning struct {
		Groups []struct {
			Readings []xmlKanjidicReading `xml:"reading"`
			Meanings []xmlKanjidicMeaning `xml:"meaning"`
		} `xml:"rmgroup"`
		Nanori []string `xml:"nanori"`
	} `xml:"reading_meaning"`
}

// ParseKanjidic streams KANJIDIC2 and calls fn once per character. A non-nil
// error from fn aborts the scan.
func ParseKanjidic(r io.Reader, fn func(Kanji) error) error {
	d := xml.NewDecoder(r)
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("kanjidic2: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "character" {
			continue
		}

		var c xmlCharacter
		if err := d.DecodeElement(&c, &se); err != nil {
			return fmt.Errorf("kanjidic2: decode character: %w", err)
		}

		k := Kanji{
			Literal: strings.TrimSpace(c.Literal),
			Grade:   c.Misc.Grade,
			Freq:    c.Misc.Freq,
			JLPT:    c.Misc.JLPT,
			Nanori:  trimAll(c.ReadingMeaning.Nanori),
		}
		if len(c.Misc.StrokeCount) > 0 {
			k.StrokeCount = c.Misc.StrokeCount[0]
		}
		for _, g := range c.ReadingMeaning.Groups {
			for _, rd := range g.Readings {
				v := strings.TrimSpace(rd.Value)
				if v == "" {
					continue
				}
				switch rd.Type {
				case "ja_on":
					k.On = append(k.On, v)
				case "ja_kun":
					k.Kun = append(k.Kun, v)
				}
				// pinyin, korean and vietnamese readings are of no use here.
			}
			for _, m := range g.Meanings {
				// An absent m_lang attribute means English.
				if m.Lang != "" {
					continue
				}
				if v := strings.TrimSpace(m.Value); v != "" {
					k.Meanings = append(k.Meanings, v)
				}
			}
		}

		if k.Literal == "" {
			continue
		}
		if err := fn(k); err != nil {
			return err
		}
	}
}

func trimAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
