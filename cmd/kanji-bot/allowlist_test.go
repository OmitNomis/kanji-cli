package main

import "testing"

// The deny path is the one that matters: a bot token is a public endpoint, so
// anything other than "this exact chat" has to be refused before a session can
// exist.
func TestAllowlistDeniesByDefault(t *testing.T) {
	var unset allowlist // what a missing allowlist looks like
	if unset.permits(123456789) {
		t.Error("a nil allowlist let someone in")
	}

	// An empty list is allowed to exist -- it is how you discover your own chat
	// ID -- but it must permit nobody.
	for _, raw := range []string{"", "   \n"} {
		list, err := parseAllowlist(raw)
		if err != nil {
			t.Fatalf("parseAllowlist(%q): %v", raw, err)
		}
		if len(list) != 0 || list.permits(123456789) || list.permits(0) {
			t.Errorf("parseAllowlist(%q) let someone in", raw)
		}
	}

	if _, err := parseAllowlist("everyone"); err == nil {
		t.Error("a non-numeric chat ID was accepted")
	}
	if _, err := parseAllowlist("123,oops"); err == nil {
		t.Error("one bad entry did not reject the list")
	}
}

func TestAllowlistPermitsOnlyWhatIsListed(t *testing.T) {
	list, err := parseAllowlist("123456789, -1001234567890 987")
	if err != nil {
		t.Fatalf("parseAllowlist: %v", err)
	}

	for _, id := range []int64{123456789, -1001234567890, 987} {
		if !list.permits(id) {
			t.Errorf("chat %d was listed but is not permitted", id)
		}
	}
	for _, id := range []int64{0, 1, 12345678, 987654321} {
		if list.permits(id) {
			t.Errorf("chat %d is not on the list but was permitted", id)
		}
	}
}
