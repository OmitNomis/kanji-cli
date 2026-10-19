package telegram

import (
	"fmt"
	"strings"

	"github.com/OmitNomis/cli-kanji/internal/srs"
	"github.com/OmitNomis/cli-kanji/internal/store"
)

// maxLeeches bounds the list in one message. Telegram caps a message at 4096
// characters, and a list longer than this is a different problem anyway.
const maxLeeches = 25

// Leeches lists the items forgotten too often, each with what was typed for
// it instead.
func Leeches(list []store.Leech) string {
	if len(list) == 0 {
		return fmt.Sprintf("🐛 no leeches: nothing has been forgotten %d times.", srs.LeechLapses)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "🐛 <b>%d %s</b> <i>(forgotten %d+ times)</i>\n",
		len(list), plural(len(list), "leech", "leeches"), srs.LeechLapses)
	for _, l := range list[:min(len(list), maxLeeches)] {
		fmt.Fprintf(&b, "\n<b>%s</b> %s · %d×",
			Escape(l.Card.Prompt), Escape(firstReading(l.Card.Item)), l.Card.State.Lapses)
		if len(l.Wrong) > 0 {
			var miss []string
			for _, m := range l.Wrong {
				if m.Count > 1 {
					miss = append(miss, fmt.Sprintf("%s ×%d", Escape(m.Kana), m.Count))
				} else {
					miss = append(miss, Escape(m.Kana))
				}
			}
			fmt.Fprintf(&b, "\n   ❌ <i>%s</i>", strings.Join(miss, ", "))
		}
	}
	if len(list) > maxLeeches {
		fmt.Fprintf(&b, "\n\n…and %d more", len(list)-maxLeeches)
	}
	b.WriteString("\n\n/leeches review to drill just these")
	return b.String()
}

// Related shows words already known that share a kanji with a leech just
// missed, for a reading that will not stick on its own to hang on.
func Related(items []store.Item) string {
	if len(items) == 0 {
		return ""
	}
	var words []string
	for _, it := range items {
		words = append(words, fmt.Sprintf("<b>%s</b> %s", Escape(it.Prompt), Escape(firstReading(it))))
	}
	return "🧩 you know " + strings.Join(words, " · ")
}
