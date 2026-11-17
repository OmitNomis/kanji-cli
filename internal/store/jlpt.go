package store

import (
	"context"
	"fmt"
	"strings"
)

// A JLPT target narrows what is introduced to the kanji one level of the test
// needs, easiest level first, instead of the whole joyo list in school-grade
// order. It is stored in the progress database rather than passed as a flag,
// so the terminal and the Telegram bot follow the same target without either
// being restarted or reconfigured.
//
// The test has published no kanji list since 2010. What KANJIDIC2 still carries
// is the four levels of the test before then, 4 the easiest and 1 the hardest,
// and the five current levels map onto those. The old test had nothing between
// its levels 3 and 2, so N3 and N2 share old level 2 and are taught as one
// group.

const jlptTargetKey = "target.jlpt"

// jlptFloor is the lowest old level a target includes.
var jlptFloor = map[string]int{"N5": 4, "N4": 3, "N3": 2, "N2": 2, "N1": 1}

// ParseJLPT reads a target such as "N2", "n2" or "2". "off" and "" clear the
// target, which is returned as "".
func ParseJLPT(s string) (string, error) {
	t := strings.ToUpper(strings.TrimSpace(s))
	switch t {
	case "", "OFF", "NONE", "JOYO":
		return "", nil
	}
	if !strings.HasPrefix(t, "N") {
		t = "N" + t
	}
	if _, ok := jlptFloor[t]; !ok {
		return "", fmt.Errorf("bad JLPT target %q: want N5, N4, N3, N2, N1 or off", s)
	}
	return t, nil
}

// JLPTFloor is the lowest old JLPT level a target includes, or 0 for no
// target.
func JLPTFloor(target string) int { return jlptFloor[target] }

// JLPTTarget returns the stored target, or "" when there is none.
func (s *Store) JLPTTarget(ctx context.Context) (string, error) {
	v, err := s.Setting(ctx, jlptTargetKey, "")
	if err != nil {
		return "", err
	}
	if _, ok := jlptFloor[v]; !ok {
		return "", nil
	}
	return v, nil
}

// SetJLPTTarget stores a target as ParseJLPT returns it; "" clears it.
func (s *Store) SetJLPTTarget(ctx context.Context, target string) error {
	if target == "" {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM setting WHERE key = ?`, jlptTargetKey); err != nil {
			return fmt.Errorf("clear setting %s: %w", jlptTargetKey, err)
		}
		return nil
	}
	if _, ok := jlptFloor[target]; !ok {
		return fmt.Errorf("bad JLPT target %q", target)
	}
	return s.SetSetting(ctx, jlptTargetKey, target)
}
