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
	"strconv"
	"strings"
	"time"

	"github.com/OmitNomis/cli-kanji/internal/kana"
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

// loop asks each card in turn. Every answer is persisted as it is given, so
// quitting at any point, or killing the process, loses nothing but the summary.
func (a *app) loop(ctx context.Context, sess *review.Session) error {
	for {
		card, ok := sess.Current()
		if !ok {
			return nil
		}

		a.out.Card(sess.Position(), sess.Total(), sess.Remaining()-1, card)

		answered := false
		for !answered {
			asked := time.Now()
			line, ok := a.read(">")
			if !ok {
				return errQuit
			}

			switch cmd, arg := command(line); cmd {
			case cmdEmpty:
				// An empty line is a slip, not an answer. Re-ask rather than
				// marking a card wrong because Enter was hit twice.
				continue

			case cmdQuit:
				return errQuit

			case cmdSkip:
				sess.Skip()
				answered = true

			case cmdHelp:
				a.out.Help()

			case cmdInfo:
				target := arg
				if target == "" {
					target = card.Prompt
				}
				if err := a.info(ctx, target); err != nil {
					return err
				}

			case cmdStats:
				if err := a.stats(ctx); err != nil {
					return err
				}

			case cmdReveal:
				// Being shown the answer is graded as not knowing it, which is
				// what it is. An empty answer can never match, so the reveal
				// reaches the scheduler through the same grading path as any
				// other failure, and the log records it as an empty answer.
				res, err := sess.Answer(ctx, "", 0, time.Now())
				if err != nil {
					return err
				}
				a.out.Revealed(res, card)
				if err := a.drill(ctx, card); err != nil {
					return err
				}
				answered = true

			case cmdAnswer:
				res, err := sess.Answer(ctx, line, time.Since(asked), time.Now())
				if err != nil {
					return err
				}
				if res.Correct {
					a.out.Correct(res, card)
				} else {
					a.out.Wrong(res, card)
					if err := a.drill(ctx, card); err != nil {
						return err
					}
				}
				answered = true
			}
		}
	}
}

// drill makes the reviewer type the reading they just missed.
//
// Reading the right answer and pressing Enter teaches nothing; typing it is the
// whole point of a program that refuses to offer multiple choice. It is not
// graded, because the card was already graded on the attempt that failed.
func (a *app) drill(ctx context.Context, card store.Card) error {
	a.out.Drill()
	for {
		line, ok := a.read("retype >")
		if !ok {
			return errQuit
		}
		switch cmd, arg := command(line); cmd {
		case cmdEmpty:
			continue
		case cmdQuit:
			return errQuit
		case cmdSkip:
			// There has to be a way out: a reading you cannot type yet should
			// not be able to trap the session.
			return nil
		case cmdInfo:
			target := arg
			if target == "" {
				target = card.Prompt
			}
			if err := a.info(ctx, target); err != nil {
				return err
			}
		default:
			if _, ok := kana.MatchesAny(card.Answers, line); ok {
				a.out.Accepted()
				return nil
			}
			a.out.Rejected(card)
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
	if hasNext {
		a.out.Line("nothing due. next review in %s.", ui.Duration(nextDue.Sub(now)))
	} else {
		a.out.Line("nothing due and nothing scheduled: raise -new-kanji or -new-words.")
	}
	a.out.Blank()
	return nil
}

// read prints a prompt and returns a line, or false at end of input.
func (a *app) read(label string) (string, bool) {
	a.out.Ask(label)
	if !a.in.Scan() {
		// Ctrl-D, or a pipe running dry.
		fmt.Println()
		return "", false
	}
	if !a.echoes {
		fmt.Println()
	}
	return strings.TrimSpace(a.in.Text()), true
}

func (a *app) config() (review.Config, error) {
	cfg := review.DefaultConfig()
	cfg.NewKanji = a.opts.newKanji
	cfg.NewWords = a.opts.newWords
	cfg.MaxCards = a.opts.maxCards
	cfg.Selection.WordTier = a.opts.tier
	cfg.Selection.GateLevel = a.opts.gate

	minGrade, maxGrade, err := parseGrades(a.opts.grades)
	if err != nil {
		return cfg, err
	}
	cfg.Selection.MinGrade = minGrade
	cfg.Selection.MaxGrade = maxGrade
	return cfg, nil
}

// kind enumerates what a line at the prompt turned out to be.
type kind int

const (
	cmdAnswer kind = iota
	cmdEmpty
	cmdQuit
	cmdSkip
	cmdInfo
	cmdStats
	cmdHelp
	cmdReveal
)

// command classifies a line.
//
// Commands need the colon prefix because single letters collide with real
// answers: い is a reading of 五, so a bare "i" has to mean い and not "info".
// A bare "?" is safe, since no reading is a question mark.
func command(line string) (kind, string) {
	switch {
	case line == "":
		return cmdEmpty, ""
	case line == "?":
		return cmdReveal, ""
	case !strings.HasPrefix(line, ":"):
		return cmdAnswer, line
	}

	name, arg, _ := strings.Cut(strings.TrimPrefix(line, ":"), " ")
	arg = strings.TrimSpace(arg)

	switch strings.ToLower(name) {
	case "q", "quit", "exit":
		return cmdQuit, ""
	case "s", "skip", "n", "next":
		return cmdSkip, ""
	case "i", "info":
		return cmdInfo, arg
	case "stats", "progress":
		return cmdStats, ""
	case "h", "help", "?":
		return cmdHelp, ""
	case "r", "reveal", "hint":
		return cmdReveal, ""
	default:
		return cmdHelp, ""
	}
}

// parseGrades reads a grade range such as "1-6", "3" or "1-8".
func parseGrades(s string) (int, int, error) {
	s = strings.TrimSpace(s)
	lo, hi, found := strings.Cut(s, "-")
	if !found {
		hi = lo
	}

	minGrade, err := strconv.Atoi(strings.TrimSpace(lo))
	if err != nil {
		return 0, 0, fmt.Errorf("bad -grades %q: %w", s, err)
	}
	maxGrade, err := strconv.Atoi(strings.TrimSpace(hi))
	if err != nil {
		return 0, 0, fmt.Errorf("bad -grades %q: %w", s, err)
	}
	if minGrade < 1 || maxGrade > 8 || minGrade > maxGrade {
		return 0, 0, fmt.Errorf("bad -grades %q: want a range within 1-8", s)
	}
	return minGrade, maxGrade, nil
}

// shortDate trims the importer's RFC 3339 timestamp to a date for the banner.
func shortDate(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return "built " + t.Format("2006-01-02")
	}
	return ""
}
