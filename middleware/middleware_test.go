package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func run(t *testing.T, level slog.Level, method, path string, status int) string {
	t.Helper()

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: level}))

	h := Logger(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, path, nil))

	return buf.String()
}

// TestSuccessfulAssetsAreQuiet is the difference between a readable dev console
// and one where a single page load buries the request you cared about.
func TestSuccessfulAssetsAreQuiet(t *testing.T) {
	for _, path := range []string{"/static/app.css", "/static/demo.js", "/logo.svg", "/favicon.ico"} {
		if got := run(t, slog.LevelInfo, http.MethodGet, path, 200); got != "" {
			t.Errorf("GET %s logged at info level: %s", path, got)
		}
	}
}

func TestFailingAssetsAreStillLogged(t *testing.T) {
	// A missing stylesheet is a real problem and must never be swallowed.
	if got := run(t, slog.LevelInfo, http.MethodGet, "/static/missing.css", 404); got == "" {
		t.Error("a failing asset request was not logged")
	}
}

func TestAssetsAppearAtDebugLevel(t *testing.T) {
	got := run(t, slog.LevelDebug, http.MethodGet, "/static/app.css", 200)
	if !strings.Contains(got, "/static/app.css") {
		t.Errorf("LOG_LEVEL=debug should still show assets, got: %s", got)
	}
}

func TestPagesAreAlwaysLogged(t *testing.T) {
	for _, path := range []string{"/", "/posts", "/posts/42"} {
		if got := run(t, slog.LevelInfo, http.MethodGet, path, 200); got == "" {
			t.Errorf("GET %s was not logged", path)
		}
	}
}

func TestServerErrorsLogAsWarn(t *testing.T) {
	got := run(t, slog.LevelInfo, http.MethodGet, "/boom", 500)
	if !strings.Contains(got, "level=WARN") {
		t.Errorf("a 500 should log at warn level, got: %s", got)
	}
}

func TestIsAsset(t *testing.T) {
	assets := []string{"/static/app.css", "/assets/x.js", "/a/b/logo.png", "/f.woff2", "/favicon.ico"}
	pages := []string{"/", "/posts", "/posts/42", "/up", "/demo/counter"}

	for _, p := range assets {
		if !isAsset(p) {
			t.Errorf("isAsset(%q) = false, want true", p)
		}
	}
	for _, p := range pages {
		if isAsset(p) {
			t.Errorf("isAsset(%q) = true, want false", p)
		}
	}
}
