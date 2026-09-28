package store

import (
	"os"
	"path/filepath"
)

// Environment overrides, mostly so the importer and the tests can point at a
// scratch copy without touching real progress.
const (
	EnvDict     = "CLI_KANJI_DICT"
	EnvProgress = "CLI_KANJI_PROGRESS"
)

// appDir is the per-user directory holding the progress database.
func appDir() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "cli-kanji")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "."
	}
	return filepath.Join(home, ".cli-kanji")
}

// DefaultDictPath finds the dictionary. It is shipped next to the binary, so
// the binary's own directory is checked before the per-user directory; that
// makes a self-contained "unzip and run" install work without configuration.
func DefaultDictPath() string {
	if p := os.Getenv(EnvDict); p != "" {
		return p
	}

	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "dict.db"),
			filepath.Join(dir, "data", "dict.db"),
		)
	}
	candidates = append(candidates,
		filepath.Join("data", "dict.db"),
		filepath.Join(appDir(), "dict.db"),
	)

	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	// Nothing exists yet; return the canonical build target so the error
	// message names the path the user is expected to create.
	return filepath.Join("data", "dict.db")
}

// DefaultProgressPath is always per-user: review history belongs to the person,
// not to the install.
func DefaultProgressPath() string {
	if p := os.Getenv(EnvProgress); p != "" {
		return p
	}
	return filepath.Join(appDir(), "progress.db")
}
