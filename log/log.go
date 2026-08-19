// Package log builds the application logger.
package log

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// Format selects how log records are rendered.
type Format string

const (
	// FormatAuto picks Console when writing to a terminal and JSON otherwise,
	// which gives readable local output and parsable production output with no
	// configuration in either case.
	FormatAuto Format = "auto"
	// FormatConsole is the compact, coloured, column-aligned dev format.
	FormatConsole Format = "console"
	// FormatText is slog's key=value output.
	FormatText Format = "text"
	// FormatJSON is one JSON object per record.
	FormatJSON Format = "json"
)

// ParseFormat maps a configuration string to a Format, defaulting to auto.
func ParseFormat(s string) Format {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "console", "pretty", "dev":
		return FormatConsole
	case "text", "logfmt":
		return FormatText
	case "json":
		return FormatJSON
	default:
		return FormatAuto
	}
}

// Options controls how the logger is constructed.
type Options struct {
	// Level is one of debug, info, warn, error. Defaults to info.
	Level string
	// Format selects the output shape. Defaults to FormatAuto.
	Format Format
	// Source adds the calling file and line to every record.
	Source bool
	// Output defaults to os.Stdout.
	Output io.Writer
	// NoColor forces colour off even on a terminal. Colour is also disabled
	// automatically by NO_COLOR, TERM=dumb, CI, or a non-terminal destination.
	NoColor bool

	// JSON forces JSON output.
	//
	// Deprecated: set Format to FormatJSON instead. This field predates Format
	// and is kept because applications generated before Format existed set it,
	// and an upgrade that stops those compiling would make `ry update` a thing
	// people learn to avoid. It is honoured whenever Format is left at auto.
	JSON bool
}

// New builds a *slog.Logger from opts.
func New(opts Options) *slog.Logger {
	out := opts.Output
	if out == nil {
		out = os.Stdout
	}

	handlerOpts := &slog.HandlerOptions{
		Level:     ParseLevel(opts.Level),
		AddSource: opts.Source,
	}

	format := opts.Format
	if format == "" {
		// Fall back to the environment before anything else.
		//
		// This is what lets an application generated before Format existed pick
		// up the console output after nothing more than `ry update`: its
		// config package never learned to read LOG_FORMAT, and a framework
		// upgrade cannot rewrite generated source. `ry dev` sets the variable
		// for the process it supervises, so old and new projects behave alike.
		format = ParseFormat(os.Getenv("LOG_FORMAT"))
	}
	// The deprecated JSON field only speaks when nothing else has, so both an
	// explicit Format and LOG_FORMAT win over it.
	if opts.JSON && format == FormatAuto && os.Getenv("LOG_FORMAT") == "" {
		format = FormatJSON
	}
	if format == FormatAuto {
		if isTerminal(out) {
			format = FormatConsole
		} else {
			format = FormatJSON
		}
	}

	switch format {
	case FormatConsole:
		color := !opts.NoColor && ColorEnabled(out)
		if color {
			// Legacy Windows consoles need to be told to interpret escape
			// sequences; without this they print the raw codes.
			enableVirtualTerminal(out)
		}
		return slog.New(NewConsoleHandler(out, handlerOpts, color))
	case FormatText:
		return slog.New(slog.NewTextHandler(out, handlerOpts))
	default:
		return slog.New(slog.NewJSONHandler(out, handlerOpts))
	}
}

// ParseLevel maps a level name to a slog.Level, falling back to info.
func ParseLevel(name string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
