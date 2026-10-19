package review

import (
	"context"
	"testing"
	"time"
)

func TestParseCommandColonPrefix(t *testing.T) {
	cases := []struct {
		line string
		want Kind
		arg  string
	}{
		{"", CmdEmpty, ""},
		{"nichi", CmdAnswer, "nichi"},
		{"  にち  ", CmdAnswer, "にち"},
		{"?", CmdReveal, ""},
		{":q", CmdQuit, ""},
		{":s", CmdSkip, ""},
		{":i", CmdInfo, ""},
		{":i 国境", CmdInfo, "国境"},
		{":stats", CmdStats, ""},
		{":u", CmdUndo, ""},
		{":nonsense", CmdHelp, ""},

		// The reason commands need a prefix at all: い is a reading of 五, so a
		// bare "i" has to be an answer and not "info".
		{"i", CmdAnswer, "i"},
		{"/review", CmdAnswer, "/review"},
	}

	for _, c := range cases {
		got := ParseCommand(c.line, ColonPrefix)
		if got.Kind != c.want || got.Text != c.arg {
			t.Errorf("ParseCommand(%q) = %v %q, want %v %q", c.line, got.Kind, got.Text, c.want, c.arg)
		}
	}
}

func TestParseCommandSlashPrefix(t *testing.T) {
	cases := []struct {
		line string
		want Kind
		arg  string
	}{
		{"/review", CmdReview, ""},
		{"/cram", CmdCram, ""},
		{"/ahead", CmdAhead, ""},
		{"/quick", CmdAhead, ""},
		{"/stop", CmdQuit, ""},
		{"/skip", CmdSkip, ""},
		{"/reveal", CmdReveal, ""},
		{"/stats", CmdStats, ""},
		{"/undo", CmdUndo, ""},
		{"/leeches", CmdLeeches, ""},
		{"/leeches review", CmdLeeches, "review"},
		{"/start", CmdHelp, ""},
		{"/info 国境", CmdInfo, "国境"},

		// A command addressed to one bot in a group names it after an @.
		{"/skip@kanji_bot", CmdSkip, ""},
		{"/info@kanji_bot 国境", CmdInfo, "国境"},

		// A colon is not special to the bot, so this is an answer that happens
		// to look odd rather than a command.
		{":s", CmdAnswer, ":s"},
		{"shashin", CmdAnswer, "shashin"},
	}

	for _, c := range cases {
		got := ParseCommand(c.line, SlashPrefix)
		if got.Kind != c.want || got.Text != c.arg {
			t.Errorf("ParseCommand(%q) = %v %q, want %v %q", c.line, got.Kind, got.Text, c.want, c.arg)
		}
	}
}

// answer is the line a turn would be given for one.
func answer(s string) Command { return Command{Kind: CmdAnswer, Text: s} }

func TestTurnAsksAndGradesACorrectAnswer(t *testing.T) {
	ctx := context.Background()
	sess := beginFixture(t)
	turn := NewTurn(sess, now)

	card, ok := turn.Card()
	if !ok {
		t.Fatal("a fresh turn has no card")
	}
	if card.Prompt != "日" {
		t.Fatalf("first card = %q, want 日", card.Prompt)
	}
	if turn.Phase() != Asking {
		t.Fatalf("phase = %v, want Asking", turn.Phase())
	}

	out, err := turn.Submit(ctx, answer("nichi"), now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if out.What != OutCorrect {
		t.Fatalf("outcome = %v, want OutCorrect", out.What)
	}
	if out.Card.Prompt != "日" {
		t.Errorf("outcome card = %q, want the card that was answered", out.Card.Prompt)
	}
	if turn.Phase() != Asking {
		t.Errorf("phase = %v after a correct answer, want Asking", turn.Phase())
	}
	if next, _ := turn.Card(); next.Prompt == "日" {
		t.Error("the turn did not advance past the answered card")
	}
}

// A wrong answer has to be retyped before the session moves on. It is the one
// rule the whole program is built around, so it is tested on the state machine
// rather than on either front-end.
func TestTurnRequiresARetypeAfterAWrongAnswer(t *testing.T) {
	ctx := context.Background()
	sess := beginFixture(t)
	turn := NewTurn(sess, now)

	wrong, _ := turn.Card()
	out, err := turn.Submit(ctx, answer("zzz"), now)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if out.What != OutWrong {
		t.Fatalf("outcome = %v, want OutWrong", out.What)
	}
	if turn.Phase() != Drilling {
		t.Fatalf("phase = %v after a wrong answer, want Drilling", turn.Phase())
	}

	// The card in hand is still the one that was missed, even though the
	// session has already moved on and graded it.
	if card, _ := turn.Card(); card.Prompt != wrong.Prompt {
		t.Errorf("drilling on %q, want %q", card.Prompt, wrong.Prompt)
	}

	// A wrong retype is not graded, and does not let the session past.
	out, err = turn.Submit(ctx, answer("zzz"), now)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if out.What != OutMistyped {
		t.Errorf("outcome = %v, want OutMistyped", out.What)
	}
	if turn.Phase() != Drilling {
		t.Errorf("phase = %v, want to still be Drilling", turn.Phase())
	}
	if sess.Answered != 1 {
		t.Errorf("answered = %d, want 1: a retype must not be graded", sess.Answered)
	}

	out, err = turn.Submit(ctx, answer("nichi"), now)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if out.What != OutRetyped {
		t.Errorf("outcome = %v, want OutRetyped", out.What)
	}
	if turn.Phase() != Asking {
		t.Errorf("phase = %v, want Asking once the reading was typed", turn.Phase())
	}
	if sess.Answered != 1 || sess.Incorrect != 1 {
		t.Errorf("tallies = %d answered, %d wrong; want 1 and 1", sess.Answered, sess.Incorrect)
	}
}

// A reading you cannot type yet must not be able to trap the session.
func TestTurnLetsADrillBeSkipped(t *testing.T) {
	ctx := context.Background()
	sess := beginFixture(t)
	turn := NewTurn(sess, now)

	if _, err := turn.Submit(ctx, answer("zzz"), now); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	out, err := turn.Submit(ctx, Command{Kind: CmdSkip}, now)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if out.What != OutAbandoned {
		t.Errorf("outcome = %v, want OutAbandoned", out.What)
	}
	if turn.Phase() != Asking {
		t.Errorf("phase = %v, want Asking", turn.Phase())
	}
}

// Being shown the answer is graded as not knowing it, and still has to be
// typed: the reveal is not a way around the retype.
func TestTurnGradesARevealAsWrongAndDrillsIt(t *testing.T) {
	ctx := context.Background()
	sess := beginFixture(t)
	turn := NewTurn(sess, now)

	out, err := turn.Submit(ctx, Command{Kind: CmdReveal}, now)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if out.What != OutRevealed {
		t.Fatalf("outcome = %v, want OutRevealed", out.What)
	}
	if out.Result.Correct {
		t.Error("a reveal must not be graded as correct")
	}
	if turn.Phase() != Drilling {
		t.Errorf("phase = %v, want Drilling", turn.Phase())
	}
}

func TestTurnTreatsAnEmptyLineAsASlip(t *testing.T) {
	ctx := context.Background()
	sess := beginFixture(t)
	turn := NewTurn(sess, now)

	before, _ := turn.Card()
	out, err := turn.Submit(ctx, Command{Kind: CmdEmpty}, now)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if out.What != OutNothing {
		t.Errorf("outcome = %v, want OutNothing", out.What)
	}
	if after, _ := turn.Card(); after.Key != before.Key {
		t.Error("an empty line moved the session on")
	}
	if sess.Answered != 0 {
		t.Errorf("answered = %d, want 0: Enter twice must not cost a card", sess.Answered)
	}
}

func TestTurnInfoDefaultsToTheCardInHand(t *testing.T) {
	ctx := context.Background()
	sess := beginFixture(t)
	turn := NewTurn(sess, now)

	out, err := turn.Submit(ctx, Command{Kind: CmdInfo}, now)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if out.What != OutInfo || out.Target != "日" {
		t.Errorf("outcome = %v %q, want OutInfo 日", out.What, out.Target)
	}

	out, err = turn.Submit(ctx, Command{Kind: CmdInfo, Text: "国境"}, now)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if out.Target != "国境" {
		t.Errorf("target = %q, want the one that was named", out.Target)
	}
}

func TestTurnFinishesWithTheQueue(t *testing.T) {
	ctx := context.Background()
	sess := beginFixture(t)
	turn := NewTurn(sess, now)

	// Skipping grades nothing, so the queue drains without being requeued.
	for i := 0; i < sess.Total(); i++ {
		if _, err := turn.Submit(ctx, Command{Kind: CmdSkip}, now); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	if turn.Phase() != Finished {
		t.Fatalf("phase = %v, want Finished", turn.Phase())
	}
	if _, ok := turn.Card(); ok {
		t.Error("a finished turn still has a card")
	}

	// A line submitted to a finished turn is not an error, and does nothing.
	out, err := turn.Submit(ctx, answer("nichi"), now)
	if err != nil {
		t.Fatalf("Submit on a finished turn: %v", err)
	}
	if out.What != OutNothing {
		t.Errorf("outcome = %v, want OutNothing", out.What)
	}
}

func TestTurnQuitFinishesImmediately(t *testing.T) {
	ctx := context.Background()
	turn := NewTurn(beginFixture(t), now)

	out, err := turn.Submit(ctx, Command{Kind: CmdQuit}, now)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if out.What != OutQuit || turn.Phase() != Finished {
		t.Errorf("quit gave %v and phase %v, want OutQuit and Finished", out.What, turn.Phase())
	}
}

func TestParseGrades(t *testing.T) {
	cases := []struct {
		in       string
		lo, hi   int
		wantFail bool
	}{
		{"1-8", 1, 8, false},
		{"1-6", 1, 6, false},
		{" 3 ", 3, 3, false},
		{"0-8", 0, 0, true},
		{"1-9", 0, 0, true},
		{"6-1", 0, 0, true},
		{"elementary", 0, 0, true},
	}

	for _, c := range cases {
		lo, hi, err := ParseGrades(c.in)
		if c.wantFail {
			if err == nil {
				t.Errorf("ParseGrades(%q) accepted a bad range", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseGrades(%q): %v", c.in, err)
			continue
		}
		if lo != c.lo || hi != c.hi {
			t.Errorf("ParseGrades(%q) = %d-%d, want %d-%d", c.in, lo, hi, c.lo, c.hi)
		}
	}
}

// beginFixture opens a session over the three-kanji fixture, which the turn
// tests drive one line at a time.
func beginFixture(t *testing.T) *Session {
	t.Helper()
	sess, err := Begin(context.Background(), fixture(t), exact, testConfig(), now)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if sess.Total() == 0 {
		t.Fatal("the fixture produced an empty queue")
	}
	return sess
}

// Undo takes a fumbled answer back completely: the store, the tallies and the
// queue all look as they did before it was typed, and the card is asked again.
func TestTurnUndoesAWrongAnswer(t *testing.T) {
	ctx := context.Background()
	sess := beginFixture(t)
	turn := NewTurn(sess, now)

	card, _ := turn.Card()
	before, err := sess.store.Record(ctx, card.Key)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	total := sess.Total()

	if _, err := turn.Submit(ctx, answer("zzz"), now); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	out, err := turn.Submit(ctx, Command{Kind: CmdUndo}, now)
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if out.What != OutUndone {
		t.Fatalf("outcome = %v, want OutUndone", out.What)
	}
	if got, _ := turn.Card(); got.Key != card.Key || turn.Phase() != Asking {
		t.Errorf("after undo: asking %q in phase %v, want %q in Asking", got.Prompt, turn.Phase(), card.Prompt)
	}
	if got, _ := turn.Card(); got.New != card.New || got.State.Level != card.State.Level {
		t.Errorf("after undo the card is %+v, want it as it was: %+v", got.State, card.State)
	}
	if sess.Answered != 0 || sess.Incorrect != 0 || sess.Total() != total {
		t.Errorf("tallies %d answered, %d wrong, %d queued; want 0, 0, %d",
			sess.Answered, sess.Incorrect, sess.Total(), total)
	}

	after, err := sess.store.Record(ctx, card.Key)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if after.Seen != before.Seen || after.State.Level != before.State.Level ||
		after.State.Lapses != before.State.Lapses || after.LastWrong != "" {
		t.Errorf("stored record %+v, want it as it was: %+v", after, before)
	}

	// And it can be answered properly this time.
	out, err = turn.Submit(ctx, answer("nichi"), now)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if out.What != OutCorrect {
		t.Errorf("re-asked card graded %v, want OutCorrect", out.What)
	}
}

// The chance to undo survives the retype, but not the next card being
// answered.
func TestTurnUndoLastsUntilTheNextAnswer(t *testing.T) {
	ctx := context.Background()
	sess := beginFixture(t)
	turn := NewTurn(sess, now)

	missed, _ := turn.Card()
	for _, line := range []string{"zzz", "nichi"} { // miss, then retype
		if _, err := turn.Submit(ctx, answer(line), now); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	if !sess.CanUndo() {
		t.Fatal("the retype used up the undo")
	}
	out, err := turn.Submit(ctx, Command{Kind: CmdUndo}, now)
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if got, _ := turn.Card(); out.What != OutUndone || got.Key != missed.Key {
		t.Fatalf("undo from the next card: %v, asking %q; want OutUndone on %q", out.What, got.Prompt, missed.Prompt)
	}

	// Missed again, retyped, and the next card answered: too late now.
	for _, line := range []string{"zzz", "nichi", "zzz"} {
		if _, err := turn.Submit(ctx, answer(line), now); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	if _, err := turn.Submit(ctx, Command{Kind: CmdSkip}, now); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := turn.Submit(ctx, Command{Kind: CmdSkip}, now); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if out, _ := turn.Submit(ctx, Command{Kind: CmdUndo}, now); out.What != OutNoUndo {
		t.Errorf("undo after a skip = %v, want OutNoUndo", out.What)
	}
}

// Only a wrong answer can be taken back: not a right one, and not a reveal,
// which was asked for rather than fumbled.
func TestTurnUndoIgnoresCorrectAnswersAndReveals(t *testing.T) {
	ctx := context.Background()
	sess := beginFixture(t)
	turn := NewTurn(sess, now)

	if _, err := turn.Submit(ctx, answer("nichi"), now); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if out, _ := turn.Submit(ctx, Command{Kind: CmdUndo}, now); out.What != OutNoUndo {
		t.Errorf("undo after a correct answer = %v, want OutNoUndo", out.What)
	}

	if _, err := turn.Submit(ctx, Command{Kind: CmdReveal}, now); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if out, _ := turn.Submit(ctx, Command{Kind: CmdUndo}, now); out.What != OutNoUndo {
		t.Errorf("undo after a reveal = %v, want OutNoUndo", out.What)
	}
	if sess.Answered != 2 {
		t.Errorf("answered = %d, want 2", sess.Answered)
	}
}
