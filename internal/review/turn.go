package review

import (
	"context"
	"strings"
	"time"

	"github.com/OmitNomis/cli-kanji/internal/kana"
	"github.com/OmitNomis/cli-kanji/internal/store"
)

// Kind is what a line typed at a prompt turned out to be.
type Kind int

const (
	CmdAnswer Kind = iota
	CmdEmpty
	CmdQuit
	CmdSkip
	CmdInfo
	CmdStats
	CmdHelp
	CmdReveal
	CmdUndo

	// CmdReview, CmdAhead and CmdCram open a session, so they mean nothing to
	// a turn that is already in one. A front-end that can start sessions on
	// demand -- the bot -- acts on them before the turn ever sees them.
	CmdReview
	CmdAhead
	CmdCram

	// CmdLeeches lists the leeches, or with "review" opens a session of
	// them. Either way it is the front-end's to act on, like the three above.
	CmdLeeches
)

// Command is a classified line: a kind, and the text that goes with it.
type Command struct {
	Kind Kind

	// Text is the answer for CmdAnswer and the lookup target for CmdInfo, where
	// it may be empty to mean whatever is on the card.
	Text string
}

// Command prefixes. Both front-ends need one, because single letters collide
// with real answers: い is a reading of 五, so a bare "i" has to mean い and
// not "info".
//
// The terminal uses a colon. Telegram clients already treat a slash as the
// command marker, with a menu and completion built around it, so the bot uses
// that rather than fighting it.
const (
	ColonPrefix = ":"
	SlashPrefix = "/"
)

// ParseCommand classifies a line against one prefix. Anything not starting with
// the prefix is an answer; a bare "?" is a reveal, since no reading is a
// question mark.
//
// An unknown command asks for help rather than being graded as an answer:
// mistyping a command should not cost you a card.
func ParseCommand(line, prefix string) Command {
	line = strings.TrimSpace(line)
	switch {
	case line == "":
		return Command{Kind: CmdEmpty}
	case line == "?":
		return Command{Kind: CmdReveal}
	case !strings.HasPrefix(line, prefix):
		return Command{Kind: CmdAnswer, Text: line}
	}

	name, arg, _ := strings.Cut(strings.TrimPrefix(line, prefix), " ")

	// Telegram addresses a command to a particular bot when several are in one
	// group: /skip@kanji_bot. The target is not part of the name.
	name, _, _ = strings.Cut(name, "@")

	switch strings.ToLower(name) {
	case "q", "quit", "exit", "stop", "end":
		return Command{Kind: CmdQuit}
	case "s", "skip", "n", "next":
		return Command{Kind: CmdSkip}
	case "i", "info", "look", "lookup":
		return Command{Kind: CmdInfo, Text: strings.TrimSpace(arg)}
	case "stats", "progress":
		return Command{Kind: CmdStats}
	case "h", "help", "?", "start":
		return Command{Kind: CmdHelp}
	case "r", "reveal", "hint":
		return Command{Kind: CmdReveal}
	case "u", "undo":
		return Command{Kind: CmdUndo}
	case "review", "go", "resume":
		return Command{Kind: CmdReview}
	case "ahead", "quick", "fast":
		return Command{Kind: CmdAhead}
	case "cram", "catchup":
		return Command{Kind: CmdCram}
	case "leeches", "leech":
		return Command{Kind: CmdLeeches, Text: strings.TrimSpace(arg)}
	default:
		return Command{Kind: CmdHelp}
	}
}

// Phase is where a turn has got to with the card in hand.
type Phase int

const (
	// Asking means a card has been put and an answer is awaited.
	Asking Phase = iota

	// Drilling means the last answer was wrong, or was revealed, and the
	// reading has to be typed before the session moves on.
	Drilling

	// Finished means the queue is empty.
	Finished
)

// What an outcome was: everything a front-end has to be able to show.
type What int

const (
	// OutNothing means the line changed nothing and the card still stands. An
	// empty line is a slip, not an answer.
	OutNothing What = iota

	OutCorrect
	OutWrong
	OutRevealed
	OutSkipped

	// OutRetyped and OutMistyped are a drill retype that did, and did not,
	// match. OutAbandoned is a drill left by skipping, which keeps the grade
	// the card already has.
	OutRetyped
	OutMistyped
	OutAbandoned

	OutInfo
	OutStats
	OutHelp
	OutQuit

	// OutUndone is a wrong answer taken back, its card asked again.
	// OutNoUndo is an undo with nothing to take back.
	OutUndone
	OutNoUndo
)

// Outcome is what a submitted line did.
type Outcome struct {
	What What

	// Card is the card the line applied to, which after a graded answer is no
	// longer the card the session is on.
	Card store.Card

	// Result is the grading, for OutCorrect, OutWrong and OutRevealed.
	Result Result

	// Target is what to look up, for OutInfo. It is the prompt on the card when
	// the command named nothing.
	Target string
}

// Turn drives a session one card at a time: ask it, grade the answer, and when
// the answer was not the reading, require the reading to be typed before moving
// on.
//
// That retype is the rule the whole program is built around -- reading the
// answer and pressing Enter teaches nothing -- and the command vocabulary is
// the other thing every front-end needs. Both live here rather than in a main,
// so a bug fixed in one front-end is fixed in both.
type Turn struct {
	sess  *Session
	phase Phase
	card  store.Card

	// asked is when the card in hand was last put, so an answer can be timed.
	// Anything that re-asks resets it, because time spent reading a dictionary
	// entry is not time spent recalling.
	asked time.Time
}

// NewTurn starts driving a session. The session owns the queue and the grading;
// a turn owns only the question in front of the reviewer.
func NewTurn(sess *Session, now time.Time) *Turn {
	t := &Turn{sess: sess, asked: now}
	t.advance(now)
	return t
}

// Session is the session being driven, for the counts a front-end puts around
// a card.
func (t *Turn) Session() *Session { return t.sess }

// Phase reports whether the turn wants an answer, a retype, or nothing.
func (t *Turn) Phase() Phase { return t.phase }

// Card is the card in hand, and false once the queue is empty.
func (t *Turn) Card() (store.Card, bool) { return t.card, t.phase != Finished }

// Submit applies one line and reports what it did.
//
// Everything that reaches the scheduler goes through Session.Answer, so an
// answer is persisted before Submit returns: killing either front-end costs
// nothing but the summary.
func (t *Turn) Submit(ctx context.Context, cmd Command, now time.Time) (Outcome, error) {
	if t.phase == Finished {
		return Outcome{What: OutNothing}, nil
	}

	out := Outcome{Card: t.card}

	// These mean the same thing in either phase, and none of them touches the
	// card.
	switch cmd.Kind {
	case CmdEmpty, CmdReview, CmdAhead, CmdCram, CmdLeeches:
		out.What = OutNothing
		t.asked = now
		return out, nil

	case CmdQuit:
		t.phase = Finished
		out.What = OutQuit
		return out, nil

	case CmdHelp:
		out.What = OutHelp
		t.asked = now
		return out, nil

	case CmdStats:
		out.What = OutStats
		t.asked = now
		return out, nil

	case CmdInfo:
		out.What = OutInfo
		out.Target = cmd.Text
		if out.Target == "" {
			out.Target = t.card.Prompt
		}
		t.asked = now
		return out, nil

	case CmdUndo:
		// Works from the retype, or from the card after it, as long as that
		// card has not been answered: the session forgets the chance as
		// soon as anything else is graded or skipped.
		if !t.sess.CanUndo() {
			out.What = OutNoUndo
			return out, nil
		}
		card, err := t.sess.Undo(ctx)
		if err != nil {
			return out, err
		}
		t.card, t.phase, t.asked = card, Asking, now
		out.What, out.Card = OutUndone, card
		return out, nil
	}

	if t.phase == Drilling {
		return t.retype(cmd, now), nil
	}

	switch cmd.Kind {
	case CmdSkip:
		t.sess.Skip()
		out.What = OutSkipped
		t.advance(now)

	case CmdReveal:
		// Being shown the answer is graded as not knowing it, which is what it
		// is. An empty answer can never match, so a reveal reaches the
		// scheduler through the same grading path as any other failure, and the
		// log records it as an empty answer.
		res, err := t.sess.Answer(ctx, "", 0, now)
		if err != nil {
			return out, err
		}
		out.What, out.Result = OutRevealed, res
		t.phase = Drilling

	case CmdAnswer:
		res, err := t.sess.Answer(ctx, cmd.Text, now.Sub(t.asked), now)
		if err != nil {
			return out, err
		}
		out.Result = res
		if res.Correct {
			out.What = OutCorrect
			t.advance(now)
		} else {
			out.What = OutWrong
			t.phase = Drilling
		}
	}
	return out, nil
}

// retype handles a line while the reviewer is being made to type a reading they
// missed. It is not graded: the card was graded on the attempt that failed, and
// this is practice rather than a second chance at the score.
func (t *Turn) retype(cmd Command, now time.Time) Outcome {
	out := Outcome{Card: t.card}

	if cmd.Kind == CmdSkip {
		// There has to be a way out: a reading you cannot type yet should not
		// be able to trap the session.
		out.What = OutAbandoned
		t.advance(now)
		return out
	}

	if _, ok := kana.MatchesAny(t.card.Answers, cmd.Text); ok {
		out.What = OutRetyped
		t.advance(now)
		return out
	}
	out.What = OutMistyped
	return out
}

// advance takes the next card off the session, or finishes.
func (t *Turn) advance(now time.Time) {
	card, ok := t.sess.Current()
	t.card = card
	t.asked = now
	if !ok {
		t.phase = Finished
		return
	}
	t.phase = Asking
}
