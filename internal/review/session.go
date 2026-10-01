// Package review turns the dictionary and the scheduler into a session: which
// cards to ask, in what order, and what a typed answer does to them.
//
// It holds no terminal code at all. The queue and the grading are the parts
// worth testing, and they are testable precisely because nothing here knows
// what a terminal is.
package review

import (
	"context"
	"fmt"
	"time"

	"github.com/OmitNomis/cli-kanji/internal/kana"
	"github.com/OmitNomis/cli-kanji/internal/srs"
	"github.com/OmitNomis/cli-kanji/internal/store"
)

// Config is everything adjustable about a session.
type Config struct {
	Selection store.Selection

	// NewKanji and NewWords are daily budgets, counted across every session in
	// the same local day rather than per session, so starting the program twice
	// does not double the intake.
	NewKanji int
	NewWords int

	// MaxCards caps the session. Zero means work through everything due.
	MaxCards int

	// NewEvery places one new item every nth card, so a backlog of reviews does
	// not have to be cleared before anything new appears.
	NewEvery int

	// RelearnGap is how many cards later a wrong answer comes back within the
	// same session. The scheduler has already set a real due time; this is the
	// immediate second look that makes the correction stick.
	RelearnGap int

	// MaxRelearns caps how many times one card may be requeued in a session, so
	// a reading you simply do not know yet cannot monopolise the queue.
	MaxRelearns int
}

// DefaultConfig is a session sized for a daily sitting: a few new characters,
// a dozen new compounds, and every review that has come due.
func DefaultConfig() Config {
	return Config{
		Selection:   store.DefaultSelection(),
		NewKanji:    5,
		NewWords:    15,
		MaxCards:    0,
		NewEvery:    4,
		RelearnGap:  6,
		MaxRelearns: 2,
	}
}

// dueFetchLimit bounds the backlog pulled into memory at once. A card is a few
// hundred bytes, so this is well under a megabyte even on the target box, and
// nobody clears two thousand reviews in a sitting anyway.
const dueFetchLimit = 2000

// Session is one pass through a queue of cards.
type Session struct {
	store *store.Store
	sched srs.Scheduler
	cfg   Config

	queue    []store.Card
	pos      int
	relearns map[string]int

	Introduced struct {
		Kanji int
		Words int
	}
	Answered  int
	Correct   int
	Incorrect int
	StartedAt time.Time
}

// Begin assembles the queue: everything due, with the day's remaining new items
// woven in.
func Begin(ctx context.Context, st *store.Store, sched srs.Scheduler, cfg Config, now time.Time) (*Session, error) {
	s := &Session{
		store:     st,
		sched:     sched,
		cfg:       cfg,
		relearns:  map[string]int{},
		StartedAt: now,
	}

	due, err := st.Due(ctx, now, dueFetchLimit)
	if err != nil {
		return nil, err
	}

	fresh, err := s.pickNew(ctx, now)
	if err != nil {
		return nil, err
	}

	s.queue = interleave(due, fresh, cfg.NewEvery)
	if cfg.MaxCards > 0 && len(s.queue) > cfg.MaxCards {
		s.queue = s.queue[:cfg.MaxCards]
	}
	return s, nil
}

// pickNew works out how much of today's budget is left and claims that many
// items: kanji first, because every compound is gated behind them.
func (s *Session) pickNew(ctx context.Context, now time.Time) ([]store.Card, error) {
	kanjiToday, wordsToday, err := s.store.IntroducedSince(ctx, startOfDay(now))
	if err != nil {
		return nil, err
	}

	kanjiBudget := max(s.cfg.NewKanji-kanjiToday, 0)
	wordBudget := max(s.cfg.NewWords-wordsToday, 0)

	var out []store.Card
	if kanjiBudget > 0 {
		items, err := s.store.NextKanji(ctx, s.cfg.Selection, kanjiBudget)
		if err != nil {
			return nil, err
		}
		out = append(out, asNewCards(items, s.sched, now)...)
	}
	if wordBudget > 0 {
		items, err := s.store.NextWords(ctx, s.cfg.Selection, wordBudget)
		if err != nil {
			return nil, err
		}
		out = append(out, asNewCards(items, s.sched, now)...)
	}
	return out, nil
}

func asNewCards(items []store.Item, sched srs.Scheduler, now time.Time) []store.Card {
	out := make([]store.Card, 0, len(items))
	for _, it := range items {
		out = append(out, store.Card{Item: it, State: sched.Introduce(now), New: true})
	}
	return out
}

// interleave drops one new card into every nth slot, falling back to whichever
// list still has cards once the other runs out.
func interleave(due, fresh []store.Card, every int) []store.Card {
	if every < 1 {
		every = 1
	}

	out := make([]store.Card, 0, len(due)+len(fresh))
	var d, f int
	for d < len(due) || f < len(fresh) {
		// Position is 1-based for the modulo so a session opens with a review
		// when there is one to open with.
		wantNew := len(out)%every == every-1
		switch {
		case f < len(fresh) && (wantNew || d >= len(due)):
			out = append(out, fresh[f])
			f++
		case d < len(due):
			out = append(out, due[d])
			d++
		default:
			out = append(out, fresh[f])
			f++
		}
	}
	return out
}

// Remaining is how many cards are still queued, including requeued ones.
func (s *Session) Remaining() int { return len(s.queue) - s.pos }

// Total is the size the queue has reached, which grows as cards are requeued.
func (s *Session) Total() int { return len(s.queue) }

// Position is the 1-based index of the card Current returns.
func (s *Session) Position() int { return s.pos + 1 }

// Current returns the card to ask, or false when the session is done.
func (s *Session) Current() (store.Card, bool) {
	if s.pos >= len(s.queue) {
		return store.Card{}, false
	}
	return s.queue[s.pos], true
}

// Result is what an answer did.
type Result struct {
	Correct bool

	// Matched is the accepted reading the answer turned out to be, in the form
	// the dictionary writes it. Empty for a wrong answer.
	Matched string

	// Normalized is what the answer became after kana conversion, so a wrong
	// answer can be shown back as the kana it actually was.
	Normalized string

	Before   srs.State
	After    srs.State
	Requeued bool
}

// Answer grades the current card, persists it, and advances the session.
//
// A wrong answer is graded immediately rather than after a retype, so the
// record says what happened on the first attempt; the retype is practice, not a
// second chance at the score.
func (s *Session) Answer(ctx context.Context, given string, elapsed time.Duration, now time.Time) (Result, error) {
	card, ok := s.Current()
	if !ok {
		return Result{}, fmt.Errorf("no card to answer")
	}

	matched, correct := kana.MatchesAny(card.Answers, given)
	res := Result{
		Correct:    correct,
		Matched:    matched,
		Normalized: kana.Normalize(given),
		Before:     card.State,
		After:      s.sched.Grade(card.State, correct, now),
	}

	if err := s.store.Save(ctx, card.Key, res.After); err != nil {
		return res, err
	}
	if err := s.store.LogAnswer(ctx, card.Key, given, correct, res.Before.Level, res.After.Level, now, elapsed); err != nil {
		return res, err
	}

	s.Answered++
	if correct {
		s.Correct++
	} else {
		s.Incorrect++
	}
	if card.New {
		if card.IsKanji() {
			s.Introduced.Kanji++
		} else {
			s.Introduced.Words++
		}
	}

	// Carry the new state forward, and clear New so a requeued card is not
	// counted as a second introduction.
	card.State = res.After
	card.New = false
	s.queue[s.pos] = card

	if !correct && s.relearns[card.Key] < s.cfg.MaxRelearns {
		s.relearns[card.Key]++
		s.requeue(card)
		res.Requeued = true
	}

	s.pos++
	return res, nil
}

// Skip moves past the current card without grading it.
func (s *Session) Skip() {
	if s.pos < len(s.queue) {
		s.pos++
	}
}

// requeue puts a card back RelearnGap slots ahead, or at the end if the queue
// is shorter than that.
func (s *Session) requeue(card store.Card) {
	at := s.pos + 1 + s.cfg.RelearnGap
	if at >= len(s.queue) {
		s.queue = append(s.queue, card)
		return
	}
	s.queue = append(s.queue, store.Card{})
	copy(s.queue[at+1:], s.queue[at:])
	s.queue[at] = card
}

// Accuracy is the share of this session's answers that were correct.
func (s *Session) Accuracy() float64 {
	if s.Answered == 0 {
		return 0
	}
	return float64(s.Correct) / float64(s.Answered)
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
