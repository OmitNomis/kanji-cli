//go:build !unix && !windows

package ui

import "os"

// terminalColumns cannot ask on this platform, so the fixed default is used.
func terminalColumns(*os.File) (int, bool) { return 0, false }
