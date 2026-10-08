package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OmitNomis/cli-kanji/internal/review"
	"github.com/OmitNomis/cli-kanji/internal/store"
	"github.com/OmitNomis/cli-kanji/internal/telegram"
)

const (
	mine     = int64(99)        // the one chat on the allowlist
	stranger = int64(123456789) // anybody else
)

var now = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

// sent is what the bot said, in order.
type sent struct {
	chatID int64
	text   string
}

// api is a Bot API stand-in that records what was sent.
type api struct {
	messages []sent
}

func (a *api) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/sendMessage") {
		var msg struct {
			ChatID int64  `json:"chat_id"`
			Text   string `json:"text"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &msg)
		a.messages = append(a.messages, sent{msg.ChatID, msg.Text})
	}
	io.WriteString(w, `{"ok":true,"result":{}}`)
}

// harness is a bot wired to a fake API and a one-kanji dictionary.
func harness(t *testing.T) (*bot, *api) {
	t.Helper()

	rec := &api{}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)

	cfg := review.DefaultConfig()
	cfg.NewKanji = 1
	cfg.NewWords = 0

	b := &bot{
		client:  telegram.NewWithAPI("123456:test-token", srv.URL),
		store:   fixture(t),
		cfg:     cfg,
		allowed: allowlist{mine: true},
		chats:   map[int64]*chat{},
		idle:    time.Hour,
		poll:    time.Second,
		started: now.Add(-time.Minute),
	}
	return b, rec
}

// update wraps text as an update from a chat, sent now.
func update(id int64, text string) telegram.Update {
	return telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: id, Type: "private"},
			Date: now.Unix(),
			Text: text,
		},
	}
}

// A bot token is a public endpoint. An unlisted chat must be refused flatly:
// nothing confirming what this is, nothing echoed back, and no session.
func TestStrangersAreRefusedWithoutDetail(t *testing.T) {
	b, rec := harness(t)
	b.handle(context.Background(), update(stranger, "/review"))

	if len(rec.messages) != 1 {
		t.Fatalf("sent %d messages to a stranger, want 1", len(rec.messages))
	}
	got := rec.messages[0]
	if got.chatID != stranger {
		t.Errorf("replied to %d, want %d", got.chatID, stranger)
	}
	if got.text != "Not available." {
		t.Errorf("refusal was %q, want a flat one", got.text)
	}
	for _, leak := range []string{"kanji", "review", "cram", "日"} {
		if strings.Contains(strings.ToLower(got.text), leak) {
			t.Errorf("the refusal mentions %q", leak)
		}
	}
	if len(b.chats) != 0 {
		t.Error("a stranger opened a session")
	}
}

// A restart must not replay a day of queued messages: Telegram holds
// undelivered updates for about 24 hours.
func TestUpdatesFromBeforeStartupAreIgnored(t *testing.T) {
	b, rec := harness(t)

	u := update(mine, "/review")
	u.Message.Date = now.Add(-2 * time.Hour).Unix()
	b.handle(context.Background(), u)

	if len(rec.messages) != 0 {
		t.Errorf("answered a stale update: %v", rec.messages)
	}
}

func TestNonTextMessagesAreIgnored(t *testing.T) {
	b, rec := harness(t)
	b.handle(context.Background(), update(mine, "   "))

	if len(rec.messages) != 0 {
		t.Errorf("replied to a message with no text: %v", rec.messages)
	}
}

// The end-to-end path the whole daemon exists for: a card is asked, an answer
// is graded, and the grade is on disk before the reply goes out.
func TestReviewAsksAnswersAndPersists(t *testing.T) {
	ctx := context.Background()
	b, rec := harness(t)

	b.handle(ctx, update(mine, "/review"))
	if len(rec.messages) != 1 {
		t.Fatalf("/review sent %d messages, want 1", len(rec.messages))
	}
	opening := rec.messages[0].text
	if !strings.Contains(opening, "<b>日</b>") {
		t.Fatalf("the card was not asked:\n%s", opening)
	}
	if b.chats[mine] == nil {
		t.Fatal("no session was opened")
	}

	// Romaji, because a phone keyboard has no IME.
	b.handle(ctx, update(mine, "nichi"))
	if len(rec.messages) != 2 {
		t.Fatalf("an answer sent %d messages in total, want 2", len(rec.messages))
	}
	feedback := rec.messages[1].text
	if !strings.Contains(feedback, "✅") {
		t.Errorf("a correct answer was not acknowledged:\n%s", feedback)
	}

	// The queue held one card, so the session is over and the summary rides
	// along with the feedback rather than as a second notification.
	if !strings.Contains(feedback, "session") {
		t.Errorf("the session summary is missing:\n%s", feedback)
	}
	if b.chats[mine] != nil {
		t.Error("the finished session was not forgotten")
	}

	// And the important half: it is on disk.
	cards, err := b.store.Scheduled(ctx, 10)
	if err != nil {
		t.Fatalf("Scheduled: %v", err)
	}
	if len(cards) != 1 {
		t.Fatalf("%d items in the queue, want 1", len(cards))
	}
	if cards[0].Key != "k:日" || cards[0].State.Level != 1 {
		t.Errorf("persisted %s at level %d, want k:日 at level 1", cards[0].Key, cards[0].State.Level)
	}
}

// A wrong answer has to be retyped, in the bot exactly as in the terminal.
func TestWrongAnswerDrillsInTheChat(t *testing.T) {
	ctx := context.Background()
	b, rec := harness(t)

	b.handle(ctx, update(mine, "/review"))
	b.handle(ctx, update(mine, "zzz"))

	feedback := rec.messages[len(rec.messages)-1].text
	if !strings.Contains(feedback, "type it to carry on") {
		t.Fatalf("a wrong answer did not ask for a retype:\n%s", feedback)
	}
	if b.chats[mine] == nil || b.chats[mine].turn.Phase() != review.Drilling {
		t.Fatal("the turn is not drilling after a wrong answer")
	}

	// A wrong retype does not let the session past.
	b.handle(ctx, update(mine, "zzz"))
	if b.chats[mine].turn.Phase() != review.Drilling {
		t.Error("a wrong retype moved the session on")
	}

	b.handle(ctx, update(mine, "にち"))
	if c := b.chats[mine]; c != nil && c.turn.Phase() == review.Drilling {
		t.Error("the right reading did not end the drill")
	}
}

func TestAnswerWithNoSessionSaysSo(t *testing.T) {
	b, rec := harness(t)
	b.handle(context.Background(), update(mine, "nichi"))

	if len(rec.messages) != 1 {
		t.Fatalf("sent %d messages, want 1", len(rec.messages))
	}
	if !strings.Contains(rec.messages[0].text, "/review") {
		t.Errorf("an answer with no session should point at /review, got %q", rec.messages[0].text)
	}
}

func TestIdleSessionsAreForgotten(t *testing.T) {
	b, _ := harness(t)
	b.handle(context.Background(), update(mine, "/review"))
	if b.chats[mine] == nil {
		t.Fatal("no session to expire")
	}

	b.expire(time.Now().Add(-time.Minute))
	if b.chats[mine] == nil {
		t.Error("a session in use was expired")
	}

	b.expire(time.Now().Add(2 * time.Hour))
	if b.chats[mine] != nil {
		t.Error("an idle session was kept, which pins a queue in memory")
	}
}

// fixture builds a dictionary of one kanji and opens a store on it. The bot
// needs a real store because every answer goes through it.
func fixture(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()

	dir := t.TempDir()
	dictPath := filepath.Join(dir, "dict.db")

	dict, err := store.OpenDict(ctx, dictPath)
	if err != nil {
		t.Fatalf("OpenDict: %v", err)
	}

	cp := int64([]rune("日")[0])
	exec(t, dict.DB(), `INSERT INTO kanji (id, literal, grade, stroke_count) VALUES (?, ?, 1, 4)`, cp, "日")
	exec(t, dict.DB(), `
		INSERT INTO item (key, kind, ref_id, prompt, answers, normalized, hint, gloss,
		                  grade, max_grade, kanji_count, common, sort_key)
		VALUES ('k:日', 'kanji', ?, '日', 'にち', 'にち', 'ニチ', 'day; sun', 1, 1, 1, 2, 1)`, cp)

	if err := dict.Close(); err != nil {
		t.Fatalf("close dict: %v", err)
	}

	s, err := store.Open(ctx, dictPath, filepath.Join(dir, "progress.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func exec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}
