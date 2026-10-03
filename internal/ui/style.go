package ui

import (
	"os"
	"strings"
)

// Style wraps text in ANSI escapes, or does nothing when colour is off. Every
// method is safe on the zero value, which is the colourless one.
type Style struct {
	on bool
}

// NewStyle decides whether to colour output.
//
// Colour is suppressed when NO_COLOR is set (per the no-color.org convention),
// when TERM says the terminal cannot do it, and when output is redirected,
// because escape codes in a piped log are noise.
func NewStyle(force bool) Style {
	if force {
		return Style{on: true}
	}
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return Style{}
	}
	switch strings.ToLower(os.Getenv("TERM")) {
	case "dumb", "":
		// An unset TERM on Windows is normal and the console still handles
		// colour, so only treat "dumb" as decisive elsewhere.
		if os.Getenv("TERM") == "dumb" {
			return Style{}
		}
	}
	return Style{on: IsTerminal(os.Stdout)}
}

// Enabled reports whether escapes are being emitted.
func (s Style) Enabled() bool { return s.on }

func (s Style) wrap(code, text string) string {
	if !s.on || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s Style) Bold(text string) string   { return s.wrap("1", text) }
func (s Style) Dim(text string) string    { return s.wrap("2", text) }
func (s Style) Red(text string) string    { return s.wrap("31", text) }
func (s Style) Green(text string) string  { return s.wrap("32", text) }
func (s Style) Yellow(text string) string { return s.wrap("33", text) }
func (s Style) Blue(text string) string   { return s.wrap("34", text) }
func (s Style) Cyan(text string) string   { return s.wrap("36", text) }

// BoldCyan is the prompt itself: the one thing on screen that should pull the
// eye before anything else.
func (s Style) BoldCyan(text string) string { return s.wrap("1;36", text) }
