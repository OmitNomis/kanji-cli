// Command kanji-bot is the Telegram front-end: the same queue, the same
// grading and the same progress file as the kanji CLI, reviewed from a phone.
//
// It long-polls rather than taking a webhook. Telegram has exactly two delivery
// models and both need a process alive between messages, but long polling needs
// no inbound port, no public address and no certificate -- which is what makes
// it work from a machine sitting behind Tailscale, and why this is the one part
// of the program that is a daemon.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/OmitNomis/cli-kanji/internal/review"
	"github.com/OmitNomis/cli-kanji/internal/srs"
	"github.com/OmitNomis/cli-kanji/internal/store"
	"github.com/OmitNomis/cli-kanji/internal/telegram"
)

// Configuration that must not appear in a process listing comes from the
// environment: arguments are visible in ps to every user on the box, and
// systemd's EnvironmentFile hands these over without them ever being there.
const (
	envToken   = "TELEGRAM_BOT_TOKEN"
	envAllowed = "TELEGRAM_ALLOWED_CHAT_IDS"
)

const usage = `kanji-bot reviews the kanji queue over Telegram. It shares the progress
database with the kanji CLI, so a session on a commute and one at the desk are
the same queue.

Usage:
  kanji-bot [flags]

Environment:
  ` + envToken + `          from @BotFather
  ` + envAllowed + `   chat IDs allowed to use the bot, comma
                              separated. It must be set; an empty list denies
                              everyone and logs the IDs it refused, which is
                              how you find your own

Flags:
`

// Backoff bounds for a failing poll. Telegram redelivers anything that was not
// acknowledged with an offset, so waiting costs nothing but time.
const (
	minBackoff = 1 * time.Second
	maxBackoff = 2 * time.Minute
)

// staleGrace is how far before startup an update may have been sent and still
// be answered. Telegram holds undelivered updates for about a day, and grading
// cards against readings typed yesterday is worse than ignoring them.
const staleGrace = 2 * time.Minute

func main() {
	// The journal timestamps every line already.
	log.SetFlags(0)

	if err := run(os.Args[1:]); err != nil {
		log.Printf("kanji-bot: %v", err)
		os.Exit(1)
	}
}

type options struct {
	dict     string
	progress string

	newKanji int
	newWords int
	maxCards int
	grades   string
	tier     int
	gate     int

	idle time.Duration
	poll time.Duration

	remindAt int
	quiet    string
}

func run(args []string) error {
	var o options
	fs := flag.NewFlagSet("kanji-bot", flag.ExitOnError)
	fs.StringVar(&o.dict, "dict", store.DefaultDictPath(), "dictionary database")
	fs.StringVar(&o.progress, "progress", store.DefaultProgressPath(), "progress database")
	fs.IntVar(&o.newKanji, "new-kanji", 5, "new kanji to introduce per day")
	fs.IntVar(&o.newWords, "new-words", 15, "new compounds to introduce per day")
	fs.IntVar(&o.maxCards, "max", 0, "stop after this many cards (0 for everything due)")
	fs.StringVar(&o.grades, "grades", "1-8", "school grades to teach: 1-6 elementary, 8 the rest of the joyo list")
	fs.IntVar(&o.tier, "tier", 1, "lowest compound frequency tier to review: 2 common, 1 also second tier, 0 all")
	fs.IntVar(&o.gate, "gate", 1, "level a kanji must reach before its compounds are introduced")
	fs.DurationVar(&o.idle, "idle", time.Hour, "forget a chat's session after this long without a message")
	fs.DurationVar(&o.poll, "poll", 45*time.Second, "how long each long poll waits for an update")
	fs.IntVar(&o.remindAt, "remind-at", 10, "ping when a session of this many cards is waiting (0 for never)")
	fs.StringVar(&o.quiet, "quiet", "23-8", "local hours with no pings, such as 23-8 (empty for none)")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), usage)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	token := strings.TrimSpace(os.Getenv(envToken))
	if token == "" {
		return fmt.Errorf("%s is not set", envToken)
	}

	// Unset is a mistake; set and empty is a deliberate "not yet", which runs
	// and refuses everyone so the rejected IDs reach the log.
	raw, set := os.LookupEnv(envAllowed)
	if !set {
		return fmt.Errorf("%s is not set: it must name the chats allowed to use this bot", envAllowed)
	}
	allowed, err := parseAllowlist(raw)
	if err != nil {
		return err
	}
	cfg, err := o.config()
	if err != nil {
		return err
	}
	quiet, err := parseQuiet(o.quiet)
	if err != nil {
		return err
	}

	// SIGTERM is how systemd stops a service. Cancelling the context ends the
	// poll between updates rather than in the middle of one.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, o.dict, o.progress)
	if err != nil {
		return err
	}
	defer st.Close()

	client := telegram.New(token)
	me, err := client.GetMe(ctx)
	if err != nil {
		return fmt.Errorf("the bot token was not accepted: %w", err)
	}
	log.Printf("authenticated as @%s (%d allowed chats, %s idle timeout)", me.Username, len(allowed), o.idle)
	if len(allowed) == 0 {
		log.Printf("%s is empty, so every chat will be refused. Message the bot "+
			"and put the rejected ID in /etc/cli-kanji/bot.env, then restart", envAllowed)
	}

	b := &bot{
		client:  client,
		store:   st,
		cfg:     cfg,
		sched:   srs.Default(),
		allowed: allowed,
		chats:   map[int64]*chat{},
		idle:    o.idle,
		poll:    o.poll,
		started: time.Now(),

		reminder: reminder{at: o.remindAt, quiet: quiet},
	}
	return b.serve(ctx)
}

func (o options) config() (review.Config, error) {
	cfg := review.DefaultConfig()
	cfg.NewKanji = o.newKanji
	cfg.NewWords = o.newWords
	cfg.MaxCards = o.maxCards
	cfg.Selection.WordTier = o.tier
	cfg.Selection.GateLevel = o.gate

	minGrade, maxGrade, err := review.ParseGrades(o.grades)
	if err != nil {
		return cfg, err
	}
	cfg.Selection.MinGrade = minGrade
	cfg.Selection.MaxGrade = maxGrade
	return cfg, nil
}

// chat is one reviewer's session, held in memory and keyed by chat ID.
//
// A restart loses the assembled queue but never loses progress, because every
// answer is persisted as it is given. The next message after a restart starts a
// fresh session, which re-reads what is due and carries on -- which is why the
// queue is not serialised anywhere.
type chat struct {
	turn *review.Turn
	cram bool
	seen time.Time
}

type bot struct {
	client  *telegram.Client
	store   *store.Store
	cfg     review.Config
	sched   srs.Scheduler
	allowed allowlist

	chats map[int64]*chat
	idle  time.Duration
	poll  time.Duration

	started time.Time

	reminder reminder
}

// serve is the long poll.
//
// Updates are handled one at a time, in order, on this goroutine. The store
// holds a single SQLite connection by design, there is exactly one reviewer,
// and a sequential loop needs no locking -- which on a 1.66 GHz Atom is worth
// more than any concurrency would be.
func (b *bot) serve(ctx context.Context) error {
	var offset int64
	backoff := minBackoff

	for ctx.Err() == nil {
		updates, err := b.client.GetUpdates(ctx, offset, b.poll)
		if err != nil {
			if ctx.Err() != nil {
				break
			}

			// Failing costs nothing: an update stays queued at Telegram until
			// an offset past it is acknowledged, so the batch comes back.
			log.Printf("poll: %v", err)

			wait := backoff
			var api *telegram.APIError
			if errors.As(err, &api) && api.RetryAfter > 0 {
				wait = api.RetryAfter
			}
			if !sleep(ctx, wait) {
				break
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}
		backoff = minBackoff

		for _, u := range updates {
			// Advance the offset first: a message that makes the handler fail
			// should not be redelivered forever.
			offset = u.UpdateID + 1
			b.handle(ctx, u)
		}
		b.expire(time.Now())
		b.remind(ctx, time.Now())
	}

	log.Printf("stopped")
	return nil
}

// handle is the gate: the allowlist, the staleness check, and then dispatch.
func (b *bot) handle(ctx context.Context, u telegram.Update) {
	msg := u.Message
	if msg == nil {
		return
	}
	id := msg.Chat.ID

	if !b.allowed.permits(id) {
		// A flat refusal with no detail: do not confirm what this is, do not
		// echo what was sent. The ID is logged because reading it out of the
		// journal is how you find your own.
		log.Printf("rejected chat %d", id)
		b.reply(ctx, id, "Not available.")
		return
	}

	if msg.Sent().Before(b.started.Add(-staleGrace)) {
		log.Printf("chat %d: ignoring an update from before this process started", id)
		return
	}

	// A sticker, a photo or a location has nothing to grade. Message text is
	// deliberately never logged.
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}

	if err := b.dispatch(ctx, id, text); err != nil {
		log.Printf("chat %d: %v", id, err)
		b.reply(ctx, id, "that failed; the server log has the detail.")
	}
}

// dispatch decides whether a line belongs to the session or opens one.
func (b *bot) dispatch(ctx context.Context, id int64, text string) error {
	cmd := review.ParseCommand(text, review.SlashPrefix)
	now := time.Now()

	// Starting a session is the bot's own business: a turn only knows about the
	// card in front of it.
	switch cmd.Kind {
	case review.CmdReview:
		return b.begin(ctx, id, false, now)
	case review.CmdCram:
		return b.begin(ctx, id, true, now)
	}

	c := b.chats[id]
	if c == nil {
		switch cmd.Kind {
		case review.CmdStats:
			msg, err := b.statsText(ctx, now)
			if err != nil {
				return err
			}
			return b.send(ctx, id, msg)
		case review.CmdInfo:
			msg, err := b.infoText(ctx, cmd.Text)
			if err != nil {
				return err
			}
			return b.send(ctx, id, msg)
		case review.CmdHelp:
			return b.send(ctx, id, telegram.Help())
		case review.CmdQuit:
			return b.send(ctx, id, "no session to stop.")
		default:
			return b.send(ctx, id, telegram.NoSession())
		}
	}

	c.seen = now
	return b.submit(ctx, id, c, cmd, now)
}

// begin opens a session, or re-asks the current card when one is already open.
func (b *bot) begin(ctx context.Context, id int64, cram bool, now time.Time) error {
	if c := b.chats[id]; c != nil && c.cram == cram {
		if _, ok := c.turn.Card(); ok {
			c.seen = now
			return b.send(ctx, id, b.prompt(c))
		}
	}

	counts, err := b.store.Counts(ctx, now)
	if err != nil {
		return err
	}

	cfg := b.cfg
	cfg.Cram = cram
	sess, err := review.Begin(ctx, b.store, b.sched, cfg, now)
	if err != nil {
		return err
	}

	if sess.Total() == 0 {
		delete(b.chats, id)
		if cram {
			return b.send(ctx, id, "nothing left to cram: every item in the queue has been asked.")
		}
		nextDue, hasNext, err := b.store.NextDue(ctx, now)
		if err != nil {
			return err
		}
		return b.send(ctx, id, telegram.NothingDue(nextDue, hasNext, now))
	}

	c := &chat{turn: review.NewTurn(sess, now), cram: cram, seen: now}
	b.chats[id] = c
	return b.send(ctx, id, telegram.Join(telegram.Banner(counts, cram), b.prompt(c)))
}

// submit hands one line to the turn and sends what came of it.
//
// A chat is not a terminal: the feedback and whatever is being asked next go
// out as a single message rather than as two notifications.
func (b *bot) submit(ctx context.Context, id int64, c *chat, cmd review.Command, now time.Time) error {
	out, err := c.turn.Submit(ctx, cmd, now)
	if err != nil {
		return err
	}

	var blocks []string
	reprompt := false

	switch out.What {
	case review.OutNothing:
		// Nothing happened and the card still stands; saying so would be noise.
		return nil

	case review.OutHelp:
		blocks = append(blocks, telegram.Help())
		reprompt = true

	case review.OutStats:
		msg, err := b.statsText(ctx, now)
		if err != nil {
			return err
		}
		blocks = append(blocks, msg)
		reprompt = true

	case review.OutInfo:
		msg, err := b.infoText(ctx, out.Target)
		if err != nil {
			return err
		}
		blocks = append(blocks, msg)
		reprompt = true

	case review.OutCorrect:
		blocks = append(blocks, telegram.Correct(out.Result, out.Card))
		reprompt = true

	case review.OutWrong:
		blocks = append(blocks, telegram.Wrong(out.Result, out.Card), telegram.Drill())

	case review.OutRevealed:
		blocks = append(blocks, telegram.Revealed(out.Result, out.Card), telegram.Drill())

	case review.OutMistyped:
		blocks = append(blocks, telegram.Mistyped(out.Card))

	case review.OutRetyped:
		blocks = append(blocks, telegram.Retyped())
		reprompt = true

	case review.OutSkipped, review.OutAbandoned:
		blocks = append(blocks, telegram.Skipped())
		reprompt = true
	}

	if c.turn.Phase() == review.Finished {
		delete(b.chats, id)
		summary, err := b.summaryText(ctx, c.turn.Session(), now)
		if err != nil {
			return err
		}
		return b.send(ctx, id, telegram.Join(append(blocks, summary)...))
	}

	if reprompt {
		blocks = append(blocks, b.prompt(c))
	}
	return b.send(ctx, id, telegram.Join(blocks...))
}

// prompt is whatever the turn is currently waiting for: a card, or the reading
// it wants retyped.
func (b *bot) prompt(c *chat) string {
	if c.turn.Phase() == review.Drilling {
		return telegram.Drill()
	}
	card, ok := c.turn.Card()
	if !ok {
		return ""
	}
	sess := c.turn.Session()
	return telegram.Card(sess.Position(), sess.Total(), sess.Remaining()-1, card)
}

func (b *bot) statsText(ctx context.Context, now time.Time) (string, error) {
	counts, err := b.store.Counts(ctx, now)
	if err != nil {
		return "", err
	}
	nextDue, hasNext, err := b.store.NextDue(ctx, now)
	if err != nil {
		return "", err
	}
	week, err := b.store.Activity(ctx, now.AddDate(0, 0, -7))
	if err != nil {
		return "", err
	}
	return telegram.Stats(counts, week, nextDue, hasNext, now), nil
}

func (b *bot) infoText(ctx context.Context, target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "give me something to look up: /info 国境", nil
	}

	items, err := b.store.Lookup(ctx, target, 4)
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return telegram.NoEntry(target), nil
	}

	var parts []string
	for _, it := range items {
		var compounds []store.Item
		if it.IsKanji() {
			compounds, err = b.store.CompoundsFor(ctx, []rune(it.Prompt)[0], 8)
			if err != nil {
				return "", err
			}
		}
		parts = append(parts, telegram.Info(it, compounds))
	}
	return telegram.Join(parts...), nil
}

func (b *bot) summaryText(ctx context.Context, sess *review.Session, now time.Time) (string, error) {
	counts, err := b.store.Counts(ctx, now)
	if err != nil {
		return "", err
	}
	nextDue, hasNext, err := b.store.NextDue(ctx, now)
	if err != nil {
		return "", err
	}
	return telegram.Summary(sess, counts, nextDue, hasNext, now), nil
}

// expire forgets sessions nobody has touched for a while, so an abandoned chat
// does not pin a queue in memory on a 988 MB box. Nothing is lost: the answers
// are already on disk.
func (b *bot) expire(now time.Time) {
	for id, c := range b.chats {
		if now.Sub(c.seen) > b.idle {
			delete(b.chats, id)
			log.Printf("chat %d: session forgotten after %s idle", id, b.idle)
		}
	}
}

// send delivers a message, and says nothing when there is nothing to say.
func (b *bot) send(ctx context.Context, id int64, html string) error {
	if strings.TrimSpace(html) == "" {
		return nil
	}
	return b.client.SendMessage(ctx, id, html)
}

// reply is send for messages that are themselves the error path: a failure to
// deliver one is worth a log line and nothing more.
func (b *bot) reply(ctx context.Context, id int64, html string) {
	if err := b.send(ctx, id, html); err != nil {
		log.Printf("chat %d: send: %v", id, err)
	}
}

// sleep waits, unless the process is being stopped.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
