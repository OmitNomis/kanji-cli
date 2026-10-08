package importer

import "sort"

// MergeHomographs folds entries that share a spelling into one.
//
// JMdict splits a spelling into one entry per word: 人 is じん, ひと and にん,
// three entries. Taught as three cards they all show the same 人 with nothing to
// tell them apart, so each card's answer is a guess between readings the
// reviewer may know perfectly well. One spelling is one card, accepting every
// reading any of its entries has.
//
// The merged entry keeps the sequence number of the most common entry, so a
// card already in progress under that key keeps its progress. Readings are
// ordered the same way, most common entry first, and each entry contributes its
// leading sense to the glosses, so the card still says what every reading means.
// Order is that of each spelling's first appearance.
func MergeHomographs(words []Word) []Word {
	groups := make(map[string][]Word, len(words))
	var order []string
	for _, w := range words {
		if _, seen := groups[w.Surface]; !seen {
			order = append(order, w.Surface)
		}
		groups[w.Surface] = append(groups[w.Surface], w)
	}

	out := make([]Word, 0, len(order))
	for _, surface := range order {
		out = append(out, mergeGroup(groups[surface]))
	}
	return out
}

func mergeGroup(group []Word) Word {
	if len(group) == 1 {
		return group[0]
	}

	sort.SliceStable(group, func(i, j int) bool {
		if group[i].Common != group[j].Common {
			return group[i].Common > group[j].Common
		}
		return group[i].FreqRank < group[j].FreqRank
	})

	merged := group[0]
	merged.entries = len(group)
	merged.Readings = nil
	merged.Glosses = nil
	seen := map[string]bool{}
	for _, w := range group {
		for _, r := range w.Readings {
			if !seen[r] {
				seen[r] = true
				merged.Readings = append(merged.Readings, r)
			}
		}
		if len(w.Glosses) > 0 {
			merged.Glosses = append(merged.Glosses, w.Glosses[0])
		}
	}
	return merged
}
