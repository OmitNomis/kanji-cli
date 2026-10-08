//go:build !linux

package ui

import "os"

// EditUTF8 is a no-op off Linux. The Windows console reads UTF-16 and edits
// whole characters already, and the BSDs and macOS have no portable switch for
// it; a line mangled there is still caught by the reader, which refuses
// anything that is not valid UTF-8.
func EditUTF8(f *os.File) (restore func()) { return func() {} }
