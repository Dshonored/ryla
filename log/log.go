// Package log builds the application logger.
package log

import (
	"log/slog"
	"os"
	"strings"
)

// Options controls how the logger is constructed.
type Options struct {
	// Level is one of debug, info, warn, error. Defaults to info.
	Level string
	// JSON emits structured JSON instead of human-readable text. Production
	// deployments generally want this on so log aggregators can parse it.
	JSON bool
	// Source adds the calling file and line to every record.
	Source bool
}

// New builds a *slog.Logger from opts.
func New(opts Options) *slog.Logger {
	handlerOpts := &slog.HandlerOptions{
		Level:     ParseLevel(opts.Level),
		AddSource: opts.Source,
	}

	var h slog.Handler
	if opts.JSON {
		h = slog.NewJSONHandler(os.Stdout, handlerOpts)
	} else {
		h = slog.NewTextHandler(os.Stdout, handlerOpts)
	}
	return slog.New(h)
}

// ParseLevel maps a level name to a slog.Level, falling back to info.
func ParseLevel(name string) slog.Level {
	switch strings.ToLower(name) {
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
