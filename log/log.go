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
		format = FormatAuto
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
