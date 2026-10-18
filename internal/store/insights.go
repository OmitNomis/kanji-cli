package store

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/OmitNomis/cli-kanji/internal/srs"
)

// Insights is what the answer log says beyond where the queue stands: what is
// coming, whether each rung of the ladder is holding, and what is right but
// hard won.
type Insights struct {
	// Coming is how many items fall due within a day, a week and a month
	// that are not due yet. Each window includes the shorter ones.
	Coming struct{ Day, Week, Month int }

	// ByLevel is accuracy by the level an item was at when asked, over the
	// window. A rung whose accuracy sags is a rung whose interval is too long.
	ByLevel [srs.MaxLevel + 1]Tally

	// Slow is the items answered right but slowest, slowest first.
	Slow []SlowItem
}

// Tally counts answers and how many were right.
type Tally struct{ Answered, Correct int }

// Accuracy is the share that were right, 0 for none.
func (t Tally) Accuracy() float64 {
	if t.Answered == 0 {
		return 0
	}
	return float64(t.Correct) / float64(t.Answered)
}

// SlowItem is an item and the median time its correct answers took.
type SlowItem struct {
	Item   Item
	Median time.Duration
	Count  int // correct answers the median is over
}

// Insight windows and thresholds.
const (
	// InsightWindow is how far back accuracy and speed look: long enough to
	// see every rung below a month, short enough to describe you now.
	InsightWindow = 30 * 24 * time.Hour

	// slowMinAnswers is how many correct answers an item needs before its
	// speed means anything. One slow answer is a distraction; two is a habit.
	slowMinAnswers = 2

	// slowFloor is the median below which an answer is not slow, however it
	// ranks: the list is for hesitation, not for the slowest of the quick.
	slowFloor = 6 * time.Second

	slowLimit = 5
)

// Insights gathers what the answer log says about the window before now.
func (s *Store) Insights(ctx context.Context, now time.Time) (Insights, error) {
	var in Insights

	for _, w := range []struct {
		dst   *int
		ahead time.Duration
	}{
		{&in.Coming.Day, 24 * time.Hour},
		{&in.Coming.Week, 7 * 24 * time.Hour},
		{&in.Coming.Month, 30 * 24 * time.Hour},
	} {
		err := s.db.QueryRowContext(ctx, `
			SELECT count(*) FROM review
			WHERE due_at > ? AND due_at <= ? AND `+inDict,
			now.Unix(), now.Add(w.ahead).Unix()).Scan(w.dst)
		if err != nil {
			return in, fmt.Errorf("forecast: %w", err)
		}
	}

	since := now.Add(-InsightWindow).Unix()
	rows, err := s.db.QueryContext(ctx, `
		SELECT level_before, count(*), sum(correct)
		FROM answer_log WHERE at >= ?
		GROUP BY level_before`, since)
	if err != nil {
		return in, fmt.Errorf("accuracy by level: %w", err)
	}
	for rows.Next() {
		var level int
		var t Tally
		if err := rows.Scan(&level, &t.Answered, &t.Correct); err != nil {
			rows.Close()
			return in, err
		}
		if level >= 0 && level <= srs.MaxLevel {
			in.ByLevel[level] = t
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return in, err
	}

	in.Slow, err = s.slowest(ctx, since)
	return in, err
}

// slowest ranks items by the median time of their correct answers. The median
// is worked out here rather than in SQL, which has no median: the rows are a
// month of correct answers, a few thousand at most, and the Atom sorts that in
// well under a millisecond. A reveal is logged with no time and is not an
// answer anyway, so only timed rows count.
func (s *Store) slowest(ctx context.Context, since int64) ([]SlowItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT item_key, elapsed_ms FROM answer_log
		WHERE at >= ? AND correct = 1 AND elapsed_ms > 0
		ORDER BY item_key`, since)
	if err != nil {
		return nil, fmt.Errorf("answer times: %w", err)
	}
	times := map[string][]int64{}
	for rows.Next() {
		var key string
		var ms int64
		if err := rows.Scan(&key, &ms); err != nil {
			rows.Close()
			return nil, err
		}
		times[key] = append(times[key], ms)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []SlowItem
	for key, ms := range times {
		if len(ms) < slowMinAnswers {
			continue
		}
		slices.Sort(ms)
		median := ms[len(ms)/2]
		if len(ms)%2 == 0 {
			median = (ms[len(ms)/2-1] + ms[len(ms)/2]) / 2
		}
		if d := time.Duration(median) * time.Millisecond; d >= slowFloor {
			out = append(out, SlowItem{Item: Item{Key: key}, Median: d, Count: len(ms)})
		}
	}
	slices.SortFunc(out, func(a, b SlowItem) int {
		if c := cmp.Compare(b.Median, a.Median); c != 0 {
			return c
		}
		return cmp.Compare(a.Item.Key, b.Item.Key)
	})

	// Fill in the items, dropping any the dictionary no longer has.
	kept := out[:0]
	for _, si := range out {
		if len(kept) == slowLimit {
			break
		}
		var it Item
		err := scanItem(s.db.QueryRowContext(ctx, `
			SELECT `+itemColumns+` FROM `+DictAlias+`.item i WHERE i.key = ?`, si.Item.Key), &it)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		si.Item = it
		kept = append(kept, si)
	}
	return kept, nil
}
