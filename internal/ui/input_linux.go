//go:build linux

package ui

import (
	"os"

	"golang.org/x/sys/unix"
)

// EditUTF8 tells the terminal driver that input is UTF-8, so backspace in the
// line editor removes a whole character, and returns a function that puts the
// old setting back.
//
// Without IUTF8 the kernel erases one byte per backspace. A kana is three
// bytes, so correcting a typo leaves two stray bytes in the line that the
// screen no longer shows, and an answer that looked like にほんいち arrives as
// garbage followed by にほんいち. ssh clients that do not forward the flag leave
// it off, which is how this program is usually reached.
func EditUTF8(f *os.File) (restore func()) {
	fd := int(f.Fd())
	old, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil || old.Iflag&unix.IUTF8 != 0 {
		return func() {}
	}

	set := *old
	set.Iflag |= unix.IUTF8
	if unix.IoctlSetTermios(fd, unix.TCSETS, &set) != nil {
		return func() {}
	}
	return func() { unix.IoctlSetTermios(fd, unix.TCSETS, old) }
}
