package log

import (
	"io"
	"os"
	"strings"
)

// ANSI escape sequences. These are written by hand rather than pulled from a
// styling library on purpose: this package is linked into every generated
// application, and colouring a log line is not worth a dependency tree.
const (
	reset     = "\033[0m"
	bold      = "\033[1m"
	dim       = "\033[2m"
	red       = "\033[31m"
	green     = "\033[32m"
	yellow    = "\033[33m"
	blue      = "\033[34m"
	magenta   = "\033[35m"
	cyan      = "\033[36m"
	white     = "\033[37m"
	brightRed = "\033[91m"
)

// palette applies colour, or does not. A disabled palette returns strings
// untouched, so calling code never has to branch on whether colour is on.
type palette struct{ enabled bool }

func (p palette) wrap(code, s string) string {
	if !p.enabled || s == "" {
		return s
	}
	return code + s + reset
}

func (p palette) Dim(s string) string     { return p.wrap(dim, s) }
func (p palette) Bold(s string) string    { return p.wrap(bold, s) }
func (p palette) Red(s string) string     { return p.wrap(red, s) }
func (p palette) Green(s string) string   { return p.wrap(green, s) }
func (p palette) Yellow(s string) string  { return p.wrap(yellow, s) }
func (p palette) Blue(s string) string    { return p.wrap(blue, s) }
func (p palette) Magenta(s string) string { return p.wrap(magenta, s) }
func (p palette) Cyan(s string) string    { return p.wrap(cyan, s) }
func (p palette) White(s string) string   { return p.wrap(white, s) }

// BrightRed is reserved for things that should be impossible to miss.
func (p palette) BrightRed(s string) string { return p.wrap(brightRed, s) }

// ColorEnabled reports whether coloured output is appropriate for w.
//
// The order matters and follows what the ecosystem has settled on: an explicit
// NO_COLOR wins over everything, an explicit force flag comes next, and only
// then do we guess from whether the destination is a terminal. Guessing last
// is what makes `ry dev | tee log.txt` and CI output clean without anyone
// having to configure it.
func ColorEnabled(w io.Writer) bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	if v := os.Getenv("TERM"); v == "dumb" {
		return false
	}
	if isTruthy(os.Getenv("FORCE_COLOR")) || isTruthy(os.Getenv("CLICOLOR_FORCE")) {
		return true
	}
	// CI systems capture output to a file or a web log where escape codes are
	// noise, and almost all of them set this.
	if _, ok := os.LookupEnv("CI"); ok {
		return false
	}
	return isTerminal(w)
}

// isTerminal reports whether w is an interactive terminal.
func isTerminal(w io.Writer) bool {
	f, ok := w.(interface{ Stat() (os.FileInfo, error) })
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

// visibleLen is the length of s ignoring ANSI escape sequences, so padding
// still lines up once colour has been applied.
func visibleLen(s string) int {
	n, inEscape := 0, false
	for _, r := range s {
		switch {
		case inEscape:
			if r == 'm' {
				inEscape = false
			}
		case r == '\033':
			inEscape = true
		default:
			n++
		}
	}
	return n
}

// padRight pads s to width, measuring only the visible characters.
func padRight(s string, width int) string {
	if n := visibleLen(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}

// padLeft right-aligns s within width, measuring only visible characters.
func padLeft(s string, width int) string {
	if n := visibleLen(s); n < width {
		return strings.Repeat(" ", width-n) + s
	}
	return s
}

// truncate shortens s to width, marking the cut with an ellipsis so a long URL
// cannot break the column alignment.
func truncate(s string, width int) string {
	if width <= 1 || len([]rune(s)) <= width {
		return s
	}
	return string([]rune(s)[:width-1]) + "…"
}
