package log

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RequestMessage is the log message middleware.Logger uses for completed HTTP
// requests. ConsoleHandler recognises it and renders those records as an
// aligned access-log line instead of generic key/value pairs.
const RequestMessage = "request"

// ConsoleHandler formats records for a human watching a terminal.
//
// It is deliberately not a general-purpose handler: it optimises for the
// reading experience during development, where nearly every line is an HTTP
// request and columns that line up matter more than machine parsability. Use
// the JSON handler in production, where the reverse is true.
type ConsoleHandler struct {
	opts   *slog.HandlerOptions
	pal    palette
	mu     *sync.Mutex
	w      io.Writer
	groups []string
	attrs  []slog.Attr
}

// NewConsoleHandler builds a console handler writing to w.
func NewConsoleHandler(w io.Writer, opts *slog.HandlerOptions, color bool) *ConsoleHandler {
	if opts == nil {
		opts = &slog.HandlerOptions{}
	}
	return &ConsoleHandler{
		opts: opts,
		pal:  palette{enabled: color},
		mu:   &sync.Mutex{},
		w:    w,
	}
}

// Enabled implements slog.Handler.
func (h *ConsoleHandler) Enabled(_ context.Context, level slog.Level) bool {
	min := slog.LevelInfo
	if h.opts.Level != nil {
		min = h.opts.Level.Level()
	}
	return level >= min
}

// WithAttrs implements slog.Handler.
func (h *ConsoleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &clone
}

// WithGroup implements slog.Handler.
func (h *ConsoleHandler) WithGroup(name string) slog.Handler {
	clone := *h
	clone.groups = append(append([]string{}, h.groups...), name)
	return &clone
}

// Handle implements slog.Handler.
func (h *ConsoleHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := make([]slog.Attr, 0, r.NumAttrs()+len(h.attrs))
	attrs = append(attrs, h.attrs...)
	r.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, a)
		return true
	})

	var line string
	if r.Message == RequestMessage {
		line = h.formatRequest(r, attrs)
	} else {
		line = h.formatGeneric(r, attrs)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, line+"\n")
	return err
}

// formatRequest renders the access-log shape:
//
//	13:57:16  GET  /posts/42        404   19 B   0.3ms
func (h *ConsoleHandler) formatRequest(r slog.Record, attrs []slog.Attr) string {
	var (
		method, path string
		status       int
		bytes        int64
		dur          time.Duration
		extra        []slog.Attr
	)

	for _, a := range attrs {
		switch a.Key {
		case "method":
			method = a.Value.String()
		case "path":
			path = a.Value.String()
		case "status":
			status = int(a.Value.Int64())
		case "bytes":
			bytes = a.Value.Int64()
		case "duration":
			dur = durationOf(a.Value)
		case "request_id":
			// Deliberately dropped: it is noise on a dev console, and the
			// value is still present in the JSON handler used in production.
		default:
			extra = append(extra, a)
		}
	}

	// Columns are kept narrow deliberately. A wide path column leaves most
	// lines trailing off into empty space, and the numbers on the right are
	// what the eye scans down.
	var b strings.Builder
	b.WriteString(h.pal.Dim(r.Time.Format("15:04:05")))
	b.WriteString("  ")
	b.WriteString(padRight(h.methodColor(method), 4))
	b.WriteString(" ")
	b.WriteString(padLeft(h.statusColor(status), 3))
	b.WriteString("  ")
	b.WriteString(padRight(truncate(path, 24), 24))
	b.WriteString("  ")
	b.WriteString(padLeft(h.durationColor(dur), 7))
	b.WriteString("  ")
	b.WriteString(padLeft(h.pal.Dim(humanBytes(bytes)), 7))

	for _, a := range extra {
		b.WriteString("  ")
		b.WriteString(h.pal.Dim(a.Key + "="))
		b.WriteString(h.valueString(a.Value))
	}
	return b.String()
}

// formatGeneric renders everything that is not a request:
//
//	13:57:15  INFO   server listening   addr=:8082
func (h *ConsoleHandler) formatGeneric(r slog.Record, attrs []slog.Attr) string {
	var b strings.Builder
	b.WriteString(h.pal.Dim(r.Time.Format("15:04:05")))
	b.WriteString("  ")
	b.WriteString(padRight(h.levelColor(r.Level), 5))
	b.WriteString("  ")
	b.WriteString(r.Message)

	for _, a := range attrs {
		if a.Equal(slog.Attr{}) {
			continue
		}
		b.WriteString("  ")
		b.WriteString(h.pal.Dim(h.qualify(a.Key) + "="))
		b.WriteString(h.valueString(a.Value))
	}
	return b.String()
}

// qualify prefixes a key with any enclosing group names.
func (h *ConsoleHandler) qualify(key string) string {
	if len(h.groups) == 0 {
		return key
	}
	return strings.Join(h.groups, ".") + "." + key
}

func (h *ConsoleHandler) valueString(v slog.Value) string {
	s := v.String()
	// Quote anything containing spaces so the key=value pairs stay separable
	// by eye.
	if strings.ContainsAny(s, " \t") {
		return strconv.Quote(s)
	}
	return s
}

func (h *ConsoleHandler) levelColor(l slog.Level) string {
	name := strings.ToUpper(l.String())
	switch {
	case l >= slog.LevelError:
		return h.pal.Red(name)
	case l >= slog.LevelWarn:
		return h.pal.Yellow(name)
	case l >= slog.LevelInfo:
		return h.pal.Green(name)
	default:
		return h.pal.Dim(name)
	}
}

func (h *ConsoleHandler) methodColor(m string) string {
	switch m {
	case "GET", "HEAD":
		return h.pal.Cyan(m)
	case "POST", "PUT", "PATCH":
		return h.pal.Yellow(m)
	case "DELETE":
		return h.pal.Red(m)
	default:
		return h.pal.Dim(m)
	}
}

func (h *ConsoleHandler) statusColor(status int) string {
	s := strconv.Itoa(status)
	switch {
	case status >= 500:
		return h.pal.BrightRed(s)
	case status >= 400:
		return h.pal.Yellow(s)
	case status >= 300:
		return h.pal.Cyan(s)
	case status >= 200:
		return h.pal.Green(s)
	default:
		return h.pal.Dim(s)
	}
}

// durationColor highlights slow requests, since a dev console is the cheapest
// place to notice one.
func (h *ConsoleHandler) durationColor(d time.Duration) string {
	s := humanDuration(d)
	switch {
	case d >= time.Second:
		return h.pal.Red(s)
	case d >= 300*time.Millisecond:
		return h.pal.Yellow(s)
	default:
		return h.pal.Dim(s)
	}
}

// durationOf accepts a duration attribute however it was logged: as a real
// time.Duration, as nanoseconds, or already formatted as a string.
func durationOf(v slog.Value) time.Duration {
	switch v.Kind() {
	case slog.KindDuration:
		return v.Duration()
	case slog.KindInt64:
		return time.Duration(v.Int64())
	case slog.KindString:
		d, err := time.ParseDuration(v.String())
		if err != nil {
			return 0
		}
		return d
	default:
		return 0
	}
}

// humanDuration formats a duration at a precision a human can scan.
func humanDuration(d time.Duration) string {
	switch {
	case d <= 0:
		// Windows' clock granularity reports anything fast enough as exactly
		// zero. "<1ms" is honest about the measurement floor; "0" reads like a
		// bug.
		return "<1ms"
	case d < time.Microsecond:
		return fmt.Sprintf("%dns", d.Nanoseconds())
	case d < time.Millisecond:
		return fmt.Sprintf("%.0fµs", float64(d.Nanoseconds())/1e3)
	case d < time.Second:
		return fmt.Sprintf("%.1fms", float64(d.Nanoseconds())/1e6)
	case d < time.Minute:
		return fmt.Sprintf("%.2fs", d.Seconds())
	default:
		return d.Round(time.Millisecond).String()
	}
}

// humanBytes formats a byte count in decimal units, matching what browsers and
// CDNs report.
func humanBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}
