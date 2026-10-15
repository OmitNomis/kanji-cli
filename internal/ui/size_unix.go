//go:build unix

package ui

import (
	"os"

	"golang.org/x/sys/unix"
)

// terminalColumns reports how wide the terminal behind f is, or false when f is
// not a terminal.
func terminalColumns(f *os.File) (int, bool) {
	ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	if err != nil || ws.Col == 0 {
		return 0, false
	}
	return int(ws.Col), true
}
