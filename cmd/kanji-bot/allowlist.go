package main

import (
	"fmt"
	"strconv"
	"strings"
)

// allowlist is the set of chats the bot will talk to.
//
// A bot token is a public endpoint: anyone who learns the username can message
// the bot, and without a check they would be drilling someone else's deck and
// mutating someone else's progress. So the list is consulted before a session
// can exist, and an empty list permits nobody -- because the insecure default
// is the one that gets shipped by accident.
type allowlist map[int64]bool

// parseAllowlist reads chat IDs separated by commas or spaces.
//
// An empty string is not an error: it is a configured-but-not-yet-filled-in
// allowlist, which denies everyone and logs the IDs that were refused. That is
// how you find your own chat ID in the first place. A value that is not a
// number is an error, because it was meant to be an ID and is not one.
func parseAllowlist(s string) (allowlist, error) {
	out := allowlist{}
	for _, field := range strings.FieldsFunc(s, isSeparator) {
		id, err := strconv.ParseInt(field, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not a chat ID", envAllowed, field)
		}
		out[id] = true
	}
	return out, nil
}

// permits reports whether a chat may talk to the bot. The nil list permits
// nobody, which is what makes a missing allowlist safe rather than open.
func (a allowlist) permits(id int64) bool { return a[id] }

func isSeparator(r rune) bool {
	return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
}
