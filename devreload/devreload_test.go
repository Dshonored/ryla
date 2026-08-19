package devreload

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func html(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	})
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestScriptIsInjectedBeforeBodyClose(t *testing.T) {
	rec := get(Middleware(html("<html><body><h1>Hi</h1></body></html>")), "/")

	got := rec.Body.String()
	if !strings.Contains(got, ScriptPath) {
		t.Fatalf("script was not injected: %s", got)
	}
	// Before </body>, so the document stays well formed.
	if strings.Index(got, ScriptPath) > strings.Index(got, "</body>") {
		t.Errorf("script was injected after </body>: %s", got)
	}
}

func TestScriptIsAppendedWhenBodyTagIsMissing(t *testing.T) {
	// A fragment response is still worth reloading, and a trailing script runs
	// perfectly well.
	rec := get(Middleware(html("<h1>fragment</h1>")), "/")

	if !strings.Contains(rec.Body.String(), ScriptPath) {
		t.Errorf("script was not appended: %s", rec.Body.String())
	}
}

func TestContentLengthIsCorrected(t *testing.T) {
	body := "<html><body>hi</body></html>"
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		// A handler that sets its own length would otherwise truncate the page
		// the moment anything is added to it.
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write([]byte(body))
	}))

	rec := get(h, "/")

	declared, err := strconv.Atoi(rec.Header().Get("Content-Length"))
	if err != nil {
		t.Fatalf("Content-Length: %v", err)
	}
	if declared != rec.Body.Len() {
		t.Errorf("Content-Length = %d, actual body = %d", declared, rec.Body.Len())
	}
}

func TestNonHTMLIsUntouched(t *testing.T) {
	cases := map[string]string{
		"application/json":       `{"count":1}`,
		"text/css":               "body{}",
		"application/javascript": "console.log(1)",
	}

	for contentType, body := range cases {
		t.Run(contentType, func(t *testing.T) {
			h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", contentType)
				_, _ = w.Write([]byte(body))
			}))

			if got := get(h, "/x").Body.String(); got != body {
				t.Errorf("body was modified: %q, want %q", got, body)
			}
		})
	}
}

func TestStatusIsPreserved(t *testing.T) {
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte("<html><body>errors</body></html>"))
	}))

	rec := get(h, "/")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	// A re-rendered form after a validation failure still needs the script, or
	// the page stops reloading the moment anything goes wrong.
	if !strings.Contains(rec.Body.String(), ScriptPath) {
		t.Error("script was not injected into a 422 response")
	}
}

func TestScriptIsServed(t *testing.T) {
	rec := get(Middleware(html("<html><body></body></html>")), ScriptPath)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "EventSource") {
		t.Errorf("script does not open an EventSource: %s", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestEventStreamSendsBootID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, EventPath, nil).WithContext(ctx)

	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		Middleware(html("")).ServeHTTP(rec, req)
		close(done)
	}()

	// The first event is written immediately; give it a moment, then hang up
	// the way a browser closing a tab would.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the handler did not return after the client disconnected")
	}

	got := rec.Body.String()
	if !strings.Contains(got, bootID) {
		t.Errorf("boot id missing from the stream: %q", got)
	}
	// Without an explicit retry, EventSource waits seconds before reconnecting
	// and every reload feels broken.
	if !strings.Contains(got, "retry:") {
		t.Errorf("no retry interval was sent: %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestEnabledReadsEnvironment(t *testing.T) {
	for value, want := range map[string]bool{"": false, "0": false, "false": false, "1": true, "true": true} {
		t.Setenv(EnvVar, value)
		if got := Enabled(); got != want {
			t.Errorf("%s=%q gave Enabled() = %v, want %v", EnvVar, value, got, want)
		}
	}
}

func TestLargeBodyIsInjectedOnce(t *testing.T) {
	// Written across several Write calls, as templ does.
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html><body>")
		for i := 0; i < 100; i++ {
			_, _ = io.WriteString(w, "<p>chunk</p>")
		}
		_, _ = io.WriteString(w, "</body></html>")
	}))

	got := get(h, "/").Body.String()
	if n := strings.Count(got, ScriptPath); n != 1 {
		t.Errorf("script appears %d times, want 1", n)
	}
}
