// Package telegram is a Bot API client and the message formatting a review
// session needs: long polling, sending, and turning cards into HTML.
//
// The client half depends on nothing outside the standard library. The Bot API
// is JSON over HTTPS and the three methods this program uses are small, so a
// bot library would pull a dependency tree onto a machine whose whole appeal is
// that it needs none -- and would put the single static cgo-free binary at risk
// for no gain.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// DefaultAPI is the Bot API endpoint. It is a field on the client so the tests
// can point at an httptest server.
const DefaultAPI = "https://api.telegram.org"

// MaxMessage is the Bot API limit on a message, in characters. Messages are
// split well below it, because the API counts UTF-16 code units rather than
// runes and this package would rather not have to care about the difference.
const (
	MaxMessage = 4096
	splitAt    = 3000
)

// maxResponse bounds a reply body. A hundred updates of text cannot approach
// this, and a runaway response should not be read into a 988 MB machine.
const maxResponse = 4 << 20

// Client talks to one bot.
type Client struct {
	token   string
	baseURL string
	http    *http.Client
}

// New returns a client for a bot token.
//
// There is deliberately no overall HTTP timeout: a long poll holds the
// connection open for as long as it was asked to, and every call here carries a
// context deadline of its own instead.
func New(token string) *Client {
	return NewWithAPI(token, DefaultAPI)
}

// NewWithAPI is New against a different endpoint: a locally run Bot API server,
// or a test double.
func NewWithAPI(token, baseURL string) *Client {
	return &Client{
		token:   token,
		baseURL: strings.TrimSuffix(baseURL, "/"),
		http:    &http.Client{},
	}
}

// User is a Telegram account, bots included.
type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

// Chat is where a message was sent. For a bot talking to one person the chat ID
// and the user ID are the same number, which is what makes a chat ID usable as
// an identity in the allowlist.
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// Message is the only kind of update this bot asks for.
type Message struct {
	MessageID int64 `json:"message_id"`
	From      *User `json:"from"`
	Chat      Chat  `json:"chat"`

	// Date is the unix time the message was sent, which is how a restart tells
	// a backlog from something typed just now.
	Date int64  `json:"date"`
	Text string `json:"text"`
}

// Sent reports when the message was sent.
func (m *Message) Sent() time.Time { return time.Unix(m.Date, 0) }

// Update is one entry from getUpdates.
type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message"`
}

// APIError is a refusal from the Bot API itself, as opposed to a network
// failure. The description is the useful part; the status code alone rarely
// says what was wrong with the request.
type APIError struct {
	Method      string
	Code        int
	Description string

	// RetryAfter is how long the API asked us to wait, set on a 429.
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("telegram %s: %d %s (retry after %s)", e.Method, e.Code, e.Description, e.RetryAfter)
	}
	return fmt.Sprintf("telegram %s: %d %s", e.Method, e.Code, e.Description)
}

// GetMe confirms the token is accepted and reports who it belongs to. The bot
// calls it once at startup: a wrong token should fail immediately and say so,
// not look like a chat nobody is talking to.
func (c *Client) GetMe(ctx context.Context) (User, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var me User
	if err := c.call(ctx, "getMe", struct{}{}, &me); err != nil {
		return User{}, err
	}
	return me, nil
}

// GetUpdates is one long poll. The call returns as soon as there is anything to
// return, and otherwise waits out the timeout and returns nothing.
//
// Passing the offset is what acknowledges the previous batch: Telegram keeps
// redelivering an update until an offset past it is seen, so a crash between
// polls costs nothing.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error) {
	// The HTTP deadline has to outlast the poll the server was asked to hold,
	// or every quiet period looks like a network failure.
	ctx, cancel := context.WithTimeout(ctx, timeout+30*time.Second)
	defer cancel()

	req := struct {
		Offset         int64    `json:"offset,omitempty"`
		Timeout        int      `json:"timeout"`
		AllowedUpdates []string `json:"allowed_updates"`
	}{
		Offset:  offset,
		Timeout: int(timeout.Seconds()),

		// Asking for messages only keeps edits, reactions and channel posts out
		// of the loop entirely rather than having to filter them.
		AllowedUpdates: []string{"message"},
	}

	var updates []Update
	if err := c.call(ctx, "getUpdates", req, &updates); err != nil {
		return nil, err
	}
	return updates, nil
}

// SendMessage sends HTML, splitting anything too long for one message.
//
// HTML rather than MarkdownV2 on purpose: MarkdownV2 requires escaping a long
// list of punctuation, and glosses are full of semicolons, brackets, hyphens
// and exclamation marks. HTML needs three escapes and is far harder to get
// subtly wrong.
func (c *Client) SendMessage(ctx context.Context, chatID int64, html string) error {
	for _, part := range split(html, splitAt) {
		if err := c.sendOne(ctx, chatID, part); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) sendOne(ctx context.Context, chatID int64, html string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req := struct {
		ChatID    int64  `json:"chat_id"`
		Text      string `json:"text"`
		ParseMode string `json:"parse_mode"`
	}{
		ChatID:    chatID,
		Text:      html,
		ParseMode: "HTML",
	}
	return c.call(ctx, "sendMessage", req, nil)
}

// call makes one API request. Everything is POSTed as JSON, which avoids
// having to think about form encoding for text full of kanji.
func (c *Client) call(ctx context.Context, method string, req, out any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("telegram %s: encode request: %w", method, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return c.scrub(fmt.Errorf("telegram %s: %w", method, err))
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return c.scrub(fmt.Errorf("telegram %s: %w", method, err))
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return c.scrub(fmt.Errorf("telegram %s: read response: %w", method, err))
	}

	var env struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		// Anything unparseable came from in front of the API -- a proxy, a
		// captive portal -- so the status line is all there is to report. The
		// body is not quoted: it is untrusted and possibly enormous.
		return fmt.Errorf("telegram %s: HTTP %d with an unreadable body", method, resp.StatusCode)
	}

	if !env.OK {
		return &APIError{
			Method:      method,
			Code:        env.ErrorCode,
			Description: env.Description,
			RetryAfter:  time.Duration(env.Parameters.RetryAfter) * time.Second,
		}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return fmt.Errorf("telegram %s: decode result: %w", method, err)
	}
	return nil
}

// scrubbed hides the token in an error message while keeping the chain intact,
// so errors.Is still finds a cancelled context underneath.
type scrubbed struct {
	msg string
	err error
}

func (e *scrubbed) Error() string { return e.msg }
func (e *scrubbed) Unwrap() error { return e.err }

// scrub keeps the token out of anything that might be logged. net/http puts the
// request URL in every error it returns, and the token is in the path, so an
// unscrubbed poll failure would write the credential to the journal.
func (c *Client) scrub(err error) error {
	if err == nil || c.token == "" {
		return err
	}
	msg := strings.ReplaceAll(err.Error(), c.token, "[token]")
	if msg == err.Error() {
		return err
	}
	return &scrubbed{msg: msg, err: err}
}

// split breaks a message on line boundaries so no HTML tag is ever cut in half.
// Every tag this package emits opens and closes on one line, which is what
// makes that safe.
func split(text string, limit int) []string {
	if utf8.RuneCountInString(text) <= limit {
		return []string{text}
	}

	var parts []string
	var buf strings.Builder
	flush := func() {
		if buf.Len() > 0 {
			parts = append(parts, strings.TrimRight(buf.String(), "\n"))
			buf.Reset()
		}
	}

	for _, line := range strings.SplitAfter(text, "\n") {
		switch {
		case utf8.RuneCountInString(line) > limit:
			// One absurdly long line, which the formatters do not produce; cut
			// it on a rune boundary rather than drop it.
			flush()
			for len(line) > 0 {
				line = cut(line, limit, &parts)
			}
		case utf8.RuneCountInString(buf.String())+utf8.RuneCountInString(line) > limit:
			flush()
			buf.WriteString(line)
		default:
			buf.WriteString(line)
		}
	}
	flush()
	return parts
}

// cut takes the first limit runes off a line, appends them, and returns what is
// left.
func cut(line string, limit int, parts *[]string) string {
	n, end := 0, len(line)
	for i := range line {
		if n == limit {
			end = i
			break
		}
		n++
	}
	*parts = append(*parts, line[:end])
	return line[end:]
}
