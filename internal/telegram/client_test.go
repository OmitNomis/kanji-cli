package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testToken = "123456:AA-secret-token"

// recorder is an API stand-in: it keeps what was asked of it and replies with
// whatever the test set.
type recorder struct {
	paths  []string
	bodies []map[string]any
	reply  string
	status int
}

func (r *recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	var parsed map[string]any
	_ = json.Unmarshal(body, &parsed)

	r.paths = append(r.paths, req.URL.Path)
	r.bodies = append(r.bodies, parsed)

	if r.status != 0 {
		w.WriteHeader(r.status)
	}
	io.WriteString(w, r.reply)
}

func serve(t *testing.T, rec *recorder) *Client {
	t.Helper()
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)

	c := New(testToken)
	c.baseURL = srv.URL
	return c
}

func TestGetUpdatesSendsTheOffsetAndDecodes(t *testing.T) {
	rec := &recorder{reply: `{"ok":true,"result":[
		{"update_id":41,"message":{"message_id":7,"date":1760000000,
		 "chat":{"id":99,"type":"private"},"text":"shashin"}}]}`}
	c := serve(t, rec)

	updates, err := c.GetUpdates(context.Background(), 41, 2*time.Second)
	if err != nil {
		t.Fatalf("GetUpdates: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("got %d updates, want 1", len(updates))
	}
	if updates[0].Message.Chat.ID != 99 || updates[0].Message.Text != "shashin" {
		t.Errorf("decoded %+v, want chat 99 saying shashin", updates[0].Message)
	}

	if want := "/bot" + testToken + "/getUpdates"; rec.paths[0] != want {
		t.Errorf("called %s, want %s", rec.paths[0], want)
	}
	if got := rec.bodies[0]["offset"]; got != float64(41) {
		t.Errorf("offset = %v, want 41", got)
	}
	if got := rec.bodies[0]["timeout"]; got != float64(2) {
		t.Errorf("timeout = %v, want 2 seconds", got)
	}
}

func TestSendMessageUsesHTML(t *testing.T) {
	rec := &recorder{reply: `{"ok":true,"result":{}}`}
	c := serve(t, rec)

	if err := c.SendMessage(context.Background(), 99, "<b>写 真</b>"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if len(rec.bodies) != 1 {
		t.Fatalf("sent %d messages, want 1", len(rec.bodies))
	}
	body := rec.bodies[0]
	if body["chat_id"] != float64(99) {
		t.Errorf("chat_id = %v, want 99", body["chat_id"])
	}
	if body["parse_mode"] != "HTML" {
		t.Errorf("parse_mode = %v, want HTML", body["parse_mode"])
	}
	if body["text"] != "<b>写 真</b>" {
		t.Errorf("text = %v, want the HTML unchanged", body["text"])
	}
}

func TestSendMessageSplitsSomethingTooLong(t *testing.T) {
	rec := &recorder{reply: `{"ok":true,"result":{}}`}
	c := serve(t, rec)

	long := strings.TrimRight(strings.Repeat("<b>line</b>\n", 1000), "\n")
	if err := c.SendMessage(context.Background(), 99, long); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if len(rec.bodies) < 2 {
		t.Fatalf("sent %d messages, want it split", len(rec.bodies))
	}
	for i, body := range rec.bodies {
		text, _ := body["text"].(string)
		if len([]rune(text)) > MaxMessage {
			t.Errorf("part %d is %d characters, over the API limit", i, len([]rune(text)))
		}
		// A tag cut in half would be rejected by Telegram, so every part has to
		// be whole lines.
		if strings.Count(text, "<b>") != strings.Count(text, "</b>") {
			t.Errorf("part %d split a tag: %.40q", i, text)
		}
	}
}

func TestAPIRefusalBecomesAnAPIError(t *testing.T) {
	rec := &recorder{
		status: http.StatusTooManyRequests,
		reply:  `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":7}}`,
	}
	c := serve(t, rec)

	err := c.SendMessage(context.Background(), 99, "hi")
	var api *APIError
	if !errors.As(err, &api) {
		t.Fatalf("error = %v, want an *APIError", err)
	}
	if api.Code != 429 || api.RetryAfter != 7*time.Second {
		t.Errorf("got code %d retry %s, want 429 and 7s", api.Code, api.RetryAfter)
	}
	if api.Method != "sendMessage" {
		t.Errorf("method = %q, want sendMessage", api.Method)
	}
}

// A poll failure is logged, and net/http puts the request URL in every error it
// returns. The token is in that URL, so an unscrubbed error would write the
// credential to the journal.
func TestErrorsNeverCarryTheToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now, so the request fails in transport

	c := New(testToken)
	c.baseURL = url

	_, err := c.GetMe(context.Background())
	if err == nil {
		t.Fatal("the call against a closed server succeeded")
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatalf("the token leaked into an error: %v", err)
	}
	if !strings.Contains(err.Error(), "[token]") {
		t.Errorf("error = %v, want the token replaced rather than removed", err)
	}
}

// Scrubbing must not break the chain, or the poll loop cannot tell a shutdown
// from a network failure.
func TestScrubbingKeepsTheErrorChain(t *testing.T) {
	rec := &recorder{reply: `{"ok":true,"result":{}}`}
	c := serve(t, rec)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.GetMe(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want it to unwrap to context.Canceled", err)
	}
}

func TestSplitKeepsWholeLines(t *testing.T) {
	text := "aaa\nbbb\nccc\nddd"
	// The trailing newline of a line counts towards the limit, so 8 is what
	// holds two three-character lines.
	parts := split(text, 8)
	if len(parts) != 2 {
		t.Fatalf("split into %d parts (%q), want 2", len(parts), parts)
	}
	if parts[0] != "aaa\nbbb" || parts[1] != "ccc\nddd" {
		t.Errorf("parts = %q, want whole lines", parts)
	}
	if got := split("short", 100); len(got) != 1 || got[0] != "short" {
		t.Errorf("split of a short message = %q, want it untouched", got)
	}
}

func TestSetMyCommands(t *testing.T) {
	rec := &recorder{reply: `{"ok":true,"result":true}`}
	c := serve(t, rec)

	cmds := []BotCommand{{"review", "start or resume a session"}}
	if err := c.SetMyCommands(context.Background(), cmds); err != nil {
		t.Fatalf("SetMyCommands: %v", err)
	}
	if want := "/bot" + testToken + "/setMyCommands"; rec.paths[0] != want {
		t.Errorf("called %s, want %s", rec.paths[0], want)
	}
	got, _ := rec.bodies[0]["commands"].([]any)
	if len(got) != 1 {
		t.Fatalf("sent commands %v, want one", rec.bodies[0]["commands"])
	}
	if entry, _ := got[0].(map[string]any); entry["command"] != "review" {
		t.Errorf("sent %v, want the review command", entry)
	}
}
