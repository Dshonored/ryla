package log

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func newTestLogger(w *bytes.Buffer, color bool) *slog.Logger {
	return slog.New(NewConsoleHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}, color))
}

func TestConsoleRequestLine(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf, false)

	log.Info(RequestMessage,
		"method", "GET",
		"path", "/posts/42",
		"status", 404,
		"bytes", 19,
		slog.Duration("duration", 320*time.Microsecond),
	)

	got := buf.String()
	for _, want := range []string{"GET", "/posts/42", "404", "19 B", "320µs"} {
		if !strings.Contains(got, want) {
			t.Errorf("request line missing %q\ngot: %s", want, got)
		}
	}
	// The generic key=value rendering must not leak into the access-log line.
	if strings.Contains(got, "method=") || strings.Contains(got, "msg=") {
		t.Errorf("request line fell back to key=value formatting\ngot: %s", got)
	}
}

func TestConsoleDropsRequestIDNoise(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf, false)

	log.Info(RequestMessage,
		"method", "GET", "path", "/", "status", 200, "bytes", 0,
		slog.Duration("duration", time.Millisecond),
		"request_id", "8cc54df93064076b023476f0",
	)

	if strings.Contains(buf.String(), "8cc54df9") {
		t.Errorf("request id should not clutter the dev console\ngot: %s", buf.String())
	}
}

func TestConsoleKeepsUnknownRequestAttrs(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf, false)

	log.Info(RequestMessage,
		"method", "POST", "path", "/posts", "status", 500, "bytes", 12,
		slog.Duration("duration", time.Second),
		"error", "no such table: posts",
	)

	got := buf.String()
	// An attribute the access-log shape does not know about is still the most
	// important thing on the line when it is an error.
	if !strings.Contains(got, "error=") || !strings.Contains(got, "no such table") {
		t.Errorf("unknown attributes were dropped\ngot: %s", got)
	}
}

func TestConsoleGenericLine(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf, false)

	log.Info("server listening", "addr", ":8082")

	got := buf.String()
	if !strings.Contains(got, "INFO") || !strings.Contains(got, "server listening") || !strings.Contains(got, "addr=:8082") {
		t.Errorf("unexpected generic line: %s", got)
	}
}

func TestConsoleRespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(NewConsoleHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}, false))

	log.Info("should not appear")
	log.Warn("should appear")

	got := buf.String()
	if strings.Contains(got, "should not appear") {
		t.Error("info record was emitted below the configured level")
	}
	if !strings.Contains(got, "should appear") {
		t.Error("warn record was dropped")
	}
}

func TestConsoleWithAttrsAndGroup(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf, false).With("service", "api").WithGroup("db")

	log.Info("connected", "driver", "sqlite")

	got := buf.String()
	if !strings.Contains(got, "service=api") {
		t.Errorf("WithAttrs value missing\ngot: %s", got)
	}
	if !strings.Contains(got, "db.driver=sqlite") {
		t.Errorf("WithGroup did not qualify the key\ngot: %s", got)
	}
}

func TestColorIsOptional(t *testing.T) {
	var plain, colored bytes.Buffer
	newTestLogger(&plain, false).Info("hello", "k", "v")
	newTestLogger(&colored, true).Info("hello", "k", "v")

	if strings.Contains(plain.String(), "\033[") {
		t.Error("escape codes leaked into uncoloured output")
	}
	if !strings.Contains(colored.String(), "\033[") {
		t.Error("coloured output contains no escape codes")
	}
}

// TestColumnsAlignWithColor is the reason padding measures visible width:
// escape sequences have length but occupy no columns, so naive padding would
// make every coloured line a different width.
func TestColumnsAlignWithColor(t *testing.T) {
	widths := map[bool]int{}

	for _, color := range []bool{false, true} {
		var buf bytes.Buffer
		log := newTestLogger(&buf, color)
		log.Info(RequestMessage,
			"method", "GET", "path", "/", "status", 200, "bytes", 100,
			slog.Duration("duration", time.Millisecond))
		widths[color] = visibleLen(strings.TrimRight(buf.String(), "\n"))
	}

	if widths[false] != widths[true] {
		t.Errorf("visible width differs with colour: plain=%d colored=%d", widths[false], widths[true])
	}
}

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{621, "621 B"},
		{1100, "1.1 kB"},
		{2_300_000, "2.3 MB"},
	}
	for _, tc := range tests {
		if got := humanBytes(tc.in); got != tc.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHumanDuration(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{0, "<1ms"},
		{320 * time.Microsecond, "320µs"},
		{1200 * time.Microsecond, "1.2ms"},
		{1500 * time.Millisecond, "1.50s"},
	}
	for _, tc := range tests {
		if got := humanDuration(tc.in); got != tc.want {
			t.Errorf("humanDuration(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDurationOfAcceptsEitherEncoding(t *testing.T) {
	want := 1500 * time.Microsecond

	// Older code logged the duration as a preformatted string; the handler must
	// still render those correctly rather than showing 0.
	if got := durationOf(slog.StringValue("1.5ms")); got != want {
		t.Errorf("durationOf(string) = %v, want %v", got, want)
	}
	if got := durationOf(slog.DurationValue(want)); got != want {
		t.Errorf("durationOf(duration) = %v, want %v", got, want)
	}
}

func TestParseFormat(t *testing.T) {
	tests := map[string]Format{
		"":         FormatAuto,
		"auto":     FormatAuto,
		"console":  FormatConsole,
		"pretty":   FormatConsole,
		"JSON":     FormatJSON,
		"text":     FormatText,
		"nonsense": FormatAuto,
	}
	for in, want := range tests {
		if got := ParseFormat(in); got != want {
			t.Errorf("ParseFormat(%q) = %q, want %q", in, got, want)
		}
	}
}
