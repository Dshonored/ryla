// Package middleware provides the default HTTP middleware stack.
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	rylog "github.com/Dshonored/ryla/log"
)

type ctxKey int

const (
	requestIDKey ctxKey = iota
	loggerKey
)

// RequestID attaches a unique id to each request, exposes it on the context and
// echoes it back in the X-Request-Id header. An inbound X-Request-Id is trusted
// and reused so a trace survives across services.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = newID()
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

// RequestIDFrom returns the request id attached by RequestID, or "".
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// WithLogger puts a request-scoped logger on the context so handlers deep in
// the call stack can log with the request id already attached.
func WithLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			l := log
			if id := RequestIDFrom(r.Context()); id != "" {
				l = l.With("request_id", id)
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), loggerKey, l)))
		})
	}
}

// LoggerFrom returns the request-scoped logger, falling back to the default
// slog logger so callers never have to nil-check.
func LoggerFrom(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// Logger logs one line per completed request at info level, or warn for 5xx.
func Logger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &recorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rec, r)

			l := LoggerFrom(r.Context())
			if l == slog.Default() {
				l = log
			}

			// A page load drags in every stylesheet, script and image with it,
			// and those lines drown the request you actually wanted to read.
			// Successful asset serves drop to debug rather than disappearing,
			// so LOG_LEVEL=debug still shows them and a failing asset is never
			// hidden.
			level := slog.LevelInfo
			switch {
			case rec.status >= http.StatusInternalServerError:
				level = slog.LevelWarn
			case rec.status < http.StatusBadRequest && isAsset(r.URL.Path):
				level = slog.LevelDebug
			}
			// Logged as a real time.Duration rather than a preformatted
			// string so each handler can render it its own way: the console
			// handler aligns and colours it, the JSON handler emits nanoseconds
			// that a log pipeline can aggregate.
			attrs := []any{
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"bytes", rec.written,
				slog.Duration("duration", time.Since(start)),
			}
			l.Log(r.Context(), level, rylog.RequestMessage, attrs...)
		})
	}
}

// Recover turns a panic in a handler into a 500 instead of a dead server. In
// debug mode the panic value and stack are written to the response, which is
// what you want locally and never want in production.
func Recover(log *slog.Logger, debugMode bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				// A client disconnecting mid-write is normal, not a crash.
				if rec == http.ErrAbortHandler {
					panic(rec)
				}

				stack := debug.Stack()
				LoggerFrom(r.Context()).Error("panic recovered",
					"error", rec,
					"method", r.Method,
					"path", r.URL.Path,
					"stack", string(stack),
				)

				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				if debugMode {
					_, _ = w.Write([]byte("panic: "))
					_, _ = w.Write([]byte(toString(rec)))
					_, _ = w.Write([]byte("\n\n"))
					_, _ = w.Write(stack)
					return
				}
				_, _ = w.Write([]byte("Internal Server Error"))
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// recorder captures the status code and byte count for logging.
type recorder struct {
	http.ResponseWriter
	status  int
	written int
	wrote   bool
}

func (rec *recorder) WriteHeader(status int) {
	if rec.wrote {
		return
	}
	rec.status = status
	rec.wrote = true
	rec.ResponseWriter.WriteHeader(status)
}

func (rec *recorder) Write(b []byte) (int, error) {
	if !rec.wrote {
		rec.WriteHeader(http.StatusOK)
	}
	n, err := rec.ResponseWriter.Write(b)
	rec.written += n
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer, keeping
// Flush and Hijack working for streaming and websocket handlers.
func (rec *recorder) Unwrap() http.ResponseWriter { return rec.ResponseWriter }

func newID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}

func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case error:
		return t.Error()
	default:
		return fmt.Sprintf("%v", v)
	}
}

// assetExtensions are served, not handled: a request for one is infrastructure
// noise on a dev console rather than something the application did.
var assetExtensions = map[string]bool{
	".css": true, ".js": true, ".mjs": true, ".map": true,
	".ico": true, ".png": true, ".jpg": true, ".jpeg": true,
	".gif": true, ".svg": true, ".webp": true, ".avif": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true,
	".mp4": true, ".webm": true,
}

// isAsset reports whether path looks like a static file.
func isAsset(path string) bool {
	if strings.HasPrefix(path, "/static/") || strings.HasPrefix(path, "/assets/") {
		return true
	}
	// The dev-reload endpoints are the tooling talking to itself, and the event
	// stream stays open for as long as the tab does.
	if strings.HasPrefix(path, "/_ryla/") {
		return true
	}
	return assetExtensions[strings.ToLower(filepath.Ext(path))]
}
