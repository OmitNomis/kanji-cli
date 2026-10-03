//go:build !windows

package ui

import "os"

// IsTerminal reports whether f is a character device, which is the cheapest
// usable proxy for "a person is watching" without pulling in a dependency.
func IsTerminal(f *os.File) bool {
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

// enableUTF8 is a no-op outside Windows: every other terminal this will run on
// already speaks UTF-8.
func enableUTF8() {}
