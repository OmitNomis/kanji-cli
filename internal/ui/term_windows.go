//go:build windows

package ui

import (
	"os"

	"golang.org/x/sys/windows"
)

// codePageUTF8 is the Windows code page identifier for UTF-8. x/sys/windows
// declares SetConsoleOutputCP but not the constant it takes.
const codePageUTF8 = 65001

// IsTerminal reports whether f is a console handle.
func IsTerminal(f *os.File) bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(f.Fd()), &mode) == nil
}

// enableUTF8 switches the console to the UTF-8 code page and turns on virtual
// terminal processing.
//
// Without the first, every kanji this program prints on Windows comes out as
// mojibake; without the second, the colour escapes print literally. Both fail
// harmlessly when output is redirected, which is why the errors are ignored.
func enableUTF8() {
	windows.SetConsoleOutputCP(codePageUTF8)
	windows.SetConsoleCP(codePageUTF8)

	handle := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if windows.GetConsoleMode(handle, &mode) == nil {
		windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
	}
}
