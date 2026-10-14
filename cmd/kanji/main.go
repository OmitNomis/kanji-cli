// Command kanji is a terminal kanji SRS. It shows a character or a compound and
// you type the reading in kana; there are no options to pick from.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OmitNomis/cli-kanji/internal/review"
	"github.com/OmitNomis/cli-kanji/internal/srs"
	"github.com/OmitNomis/cli-kanji/internal/store"
	"github.com/OmitNomis/cli-kanji/internal/ui"
)

const usage = `kanji is a terminal kanji SRS. It shows a character or a compound and you type
the reading; romaji and kana both work, and there is nothing to choose from.

Usage:
  kanji [flags]           start a review session
  kanji stats             show progress without reviewing
  kanji info <kanji|word> look up an entry and the compounds that use it
  kanji version           report the dictionary build

Flags:
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, errQuit) {
			return
		}
		fmt.Fprintf(os.Stderr, "kanji: %v\n", err)
		os.Exit(1)
	}
}

// errQuit unwinds the review loop when the reviewer asks to stop. It is not a
// failure, so main swallows it.
var errQuit = errors.New("quit")

type options struct {
	dict     string
	progress string

	newKanji int
	newWords int
	maxCards int
	grades   string
	tier     int
	gate     int
	cram     bool
	ahead    bool

	width int
	color bool
}

func run(args []string) error {
	var o options
	fs := flag.NewFlagSet("kanji", flag.ExitOnError)
	fs.StringVar(&o.dict, "dict", store.DefaultDictPath(), "dictionary database")
	fs.StringVar(&o.progress, "progress", store.DefaultProgressPath(), "progress database")
	fs.IntVar(&o.newKanji, "new-kanji", 5, "new kanji to introduce per day")
	fs.IntVar(&o.newWords, "new-words", 15, "new compounds to introduce per day")
	fs.IntVar(&o.maxCards, "max", 0, "stop after this many cards (0 for everything due)")
	fs.StringVar(&o.grades, "grades", "1-8", "school grades to teach: 1-6 elementary, 8 the rest of the joyo list")
	fs.IntVar(&o.tier, "tier", 1, "lowest compound frequency tier to review: 2 common, 1 also second tier, 0 all")
	fs.IntVar(&o.gate, "gate", 1, "level a kanji must reach before its compounds are introduced")
	fs.BoolVar(&o.ahead, "ahead", false, "review what comes due in the next day as well, and take the whole new-item budget this session")
	fs.BoolVar(&o.cram, "cram", false, "ignore due times: ask everything in the queue now, and take the whole new-item budget this session")
	fs.IntVar(&o.width, "width", ui.DefaultWidth, "terminal width in columns")
	fs.BoolVar(&o.color, "color", false, "force colour even when output is redirected")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), usage)
		fs.PrintDefaults()
	}

	// The subcommand, if any, comes first and is not a flag.
	command := "review"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command = args[0]
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	out := ui.New(os.Stdout, o.width, o.color)

	st, err := store.Open(ctx, o.dict, o.progress)
	if err != nil {
		return err
	}
	defer st.Close()

	app := &app{
		store:  st,
		out:    out,
		in:     bufio.NewScanner(os.Stdin),
		opts:   o,
		echoes: ui.IsTerminal(os.Stdin),
	}

	if app.echoes {
		defer ui.EditUTF8(os.Stdin)()
	}

	switch command {
	case "review":
		return app.review(ctx)
	case "stats":
		return app.stats(ctx)
	case "info":
		return app.info(ctx, strings.Join(fs.Args(), ""))
	case "version":
		return app.version(ctx)
	default:
		return fmt.Errorf("unknown command %q (try review, stats, info or version)", command)
	}
}

type app struct {
	store *store.Store
	out   *ui.Renderer
	in    *bufio.Scanner
	opts  options

	// echoes is true when the terminal itself echoes what is typed. When input
	// is a pipe or a file nothing is echoed, so the newline that ends the input
	// line has to be written here or every answer collides with its feedback.
	echoes bool
}

func (a *app) review(ctx context.Context) error {
	cfg, err := a.config()
	if err != nil {
		return err
	}

	now := time.Now()
	counts, err := a.store.Counts(ctx, now)
	if err != nil {
		return err
	}
	built, _ := a.store.Meta(ctx, "built_at")
	a.out.Banner(counts, shortDate(built))

	sess, err := review.Begin(ctx, a.store, srs.Default(), cfg, now)
	if err != nil {
		return err
	}
	if sess.Total() == 0 {
		return a.nothingDue(ctx, now)
	}
	a.out.Help()

	err = a.loop(ctx, sess)
	if err != nil && !errors.Is(err, errQuit) {
		return err
	}

	// The summary reflects the queue as it stands now, not as it stood when the
	// session opened.
	now = time.Now()
	counts, cerr := a.store.Counts(ctx, now)
	if cerr != nil {
		return cerr
	}
	nextDue, hasNext, cerr := a.store.NextDue(ctx, now)
	if cerr != nil {
		return cerr
	}
	a.out.Summary(sess, counts, nextDue, hasNext, now)
	return nil
}

// loop asks each card in turn, letting review.Turn decide what a line means.
// Every answer is persisted as it is given, so quitting at any point, or killing
// the process, loses nothing but the summary.
func (a *app) loop(ctx context.Context, sess *review.Session) error {
	turn := review.NewTurn(sess, time.Now())
	draw := true

	for {
		card, ok := turn.Card()
		if !ok {
			return nil
		}

		// The card is redrawn after anything that scrolls it away. Looking up
		// an entry should not cost you sight of what you were answering.
		if draw {
			a.out.Card(sess.Position(), sess.Total(), sess.Remaining()-1, card)
			draw = false
		}

		prompt := ">"
		if turn.Phase() == review.Drilling {
			prompt = "retype >"
		}
		line, ok := a.read(prompt)
		if !ok {
			return errQuit
		}

		cmd := review.ParseCommand(line, review.ColonPrefix)
		out, err := turn.Submit(ctx, cmd, time.Now())
		if err != nil {
			return err
		}

		switch out.What {
		case review.OutQuit:
			return errQuit

		case review.OutNothing:
			// An empty line is a slip, not an answer: re-ask rather than
			// marking a card wrong because Enter was hit twice.
			//
			// Anything else landing here opens a session, which the terminal
			// does with a flag rather than mid-session, so say what there is.
			if cmd.Kind != review.CmdEmpty {
				a.out.Help()
			}

		case review.OutHelp:
			a.out.Help()

		case review.OutInfo:
			if err := a.info(ctx, out.Target); err != nil {
				return err
			}
			draw = turn.Phase() == review.Asking

		case review.OutStats:
			if err := a.stats(ctx); err != nil {
				return err
			}
			draw = turn.Phase() == review.Asking

		case review.OutCorrect:
			a.out.Correct(out.Result, out.Card)
			draw = true

		case review.OutWrong:
			a.out.Wrong(out.Result, out.Card)
			a.out.Drill()

		case review.OutRevealed:
			a.out.Revealed(out.Result, out.Card)
			a.out.Drill()

		case review.OutMistyped:
			a.out.Rejected(out.Card)

		case review.OutRetyped:
			a.out.Accepted()
			draw = true

		case review.OutSkipped, review.OutAbandoned:
			draw = true
		}
	}
}

func (a *app) stats(ctx context.Context) error {
	now := time.Now()
	counts, err := a.store.Counts(ctx, now)
	if err != nil {
		return err
	}
	nextDue, hasNext, err := a.store.NextDue(ctx, now)
	if err != nil {
		return err
	}
	week, err := a.store.Activity(ctx, now.AddDate(0, 0, -7))
	if err != nil {
		return err
	}
	a.out.Stats(counts, week, nextDue, hasNext, now)
	return nil
}

func (a *app) info(ctx context.Context, target string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return errors.New("info needs a kanji or a compound")
	}

	items, err := a.store.Lookup(ctx, target, 4)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		a.out.Line("no entry for %s", target)
		return nil
	}

	for _, it := range items {
		var compounds []store.Item
		if it.IsKanji() {
			compounds, err = a.store.CompoundsFor(ctx, []rune(it.Prompt)[0], 12)
			if err != nil {
				return err
			}
		}
		a.out.Info(it, compounds)
	}
	return nil
}

func (a *app) version(ctx context.Context) error {
	now := time.Now()
	counts, err := a.store.Counts(ctx, now)
	if err != nil {
		return err
	}
	for _, key := range []string{"built_at", "sources", "min_word_common"} {
		v, err := a.store.Meta(ctx, key)
		if err != nil {
			return err
		}
		if v != "" {
			a.out.Line("%-16s %s", key, v)
		}
	}
	a.out.Line("%-16s %d kanji, %d compounds", "reviewable", counts.DictKanjiItems, counts.DictWordItems)
	return nil
}

func (a *app) nothingDue(ctx context.Context, now time.Time) error {
	nextDue, hasNext, err := a.store.NextDue(ctx, now)
	if err != nil {
		return err
	}
	a.out.Blank()
	switch {
	case a.opts.ahead && !a.opts.cram:
		a.out.Line("nothing coming due within a day, and nothing left to introduce.")
	case a.opts.cram:
		// Cramming already ignored every due time, so there is nothing to wait
		// for: an empty queue here means there is nothing left to introduce.
		a.out.Line("nothing left to cram: raise -new-kanji or -new-words, or widen -grades.")
	case hasNext:
		a.out.Line("nothing due. next review in %s.", ui.Duration(nextDue.Sub(now)))
	default:
		a.out.Line("nothing due and nothing scheduled: raise -new-kanji or -new-words.")
	}
	a.out.Blank()
	return nil
}

// read prints a prompt and returns a line, or false at end of input.
//
// A line that is not valid UTF-8 is asked for again rather than graded. It only
// happens when the terminal's line editor backspaced through half a kana, so
// what arrived is not what the screen showed, and marking it wrong would cost a
// card for the terminal's mistake.
func (a *app) read(label string) (string, bool) {
	for {
		a.out.Ask(label)
		if !a.in.Scan() {
			// Ctrl-D, or a pipe running dry.
			fmt.Println()
			return "", false
		}
		if !a.echoes {
			fmt.Println()
		}
		line := a.in.Text()
		if utf8.ValidString(line) {
			return strings.TrimSpace(line), true
		}
		a.out.Garbled()
	}
}

func (a *app) config() (review.Config, error) {
	cfg := review.DefaultConfig()
	cfg.NewKanji = a.opts.newKanji
	cfg.NewWords = a.opts.newWords
	cfg.MaxCards = a.opts.maxCards
	cfg.Selection.WordTier = a.opts.tier
	cfg.Selection.GateLevel = a.opts.gate
	switch {
	case a.opts.cram:
		cfg.Pace = review.Cram
	case a.opts.ahead:
		cfg.Pace = review.Ahead
	}

	minGrade, maxGrade, err := review.ParseGrades(a.opts.grades)
	if err != nil {
		return cfg, err
	}
	cfg.Selection.MinGrade = minGrade
	cfg.Selection.MaxGrade = maxGrade
	return cfg, nil
}

// shortDate trims the importer's RFC 3339 timestamp to a date for the banner.
func shortDate(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return "built " + t.Format("2006-01-02")
	}
	return ""
}
