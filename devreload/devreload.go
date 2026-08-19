// Package devreload reloads the browser when the server restarts.
//
// Go has to recompile, so `ry dev` rebuilds and restarts the process on every
// save. The page in the browser has no way to know that happened. This wires up
// the missing half: a small script, injected into HTML responses, holds an
// event-stream connection to the server. A restart drops it; the script
// reconnects, notices the server is a different instance, and reloads.
//
// Using the disconnect as the signal rather than an explicit "reload now"
// message is what makes this survive a failed build: the watcher stops the old
// process before compiling, so a broken build simply means nothing is listening
// yet, and the page reloads when a good build brings the server back.
//
// It is active only when RYLA_DEV is set, which `ry dev` does. Nothing here
// runs in production.
package devreload

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Paths served by the middleware. The prefix is namespaced so it cannot
// collide with an application route.
const (
	Prefix     = "/_ryla/"
	EventPath  = Prefix + "reload"
	ScriptPath = Prefix + "reload.js"
)

// EnvVar switches the middleware on.
const EnvVar = "RYLA_DEV"

// Enabled reports whether live reload should run.
func Enabled() bool {
	v := os.Getenv(EnvVar)
	return v != "" && v != "0" && v != "false"
}

// bootID identifies this process. A page that reconnects and sees a different
// value knows the server was replaced.
var bootID = newBootID()

func newBootID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Only reachable with no entropy source. The clock is a poor unique
		// value but this is a development convenience, not a security boundary.
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

// Middleware serves the reload endpoints and injects the client script into
// HTML responses.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case EventPath:
			serveEvents(w, r)
			return
		case ScriptPath:
			serveScript(w)
			return
		}

		rec := &injector{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		rec.finish()
	})
}

// serveEvents holds an event-stream connection open until the client goes away
// or the process ends.
func serveEvents(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	// Nothing between here and the browser should buffer an event stream.
	h.Set("X-Accel-Buffering", "no")

	rc := http.NewResponseController(w)

	// retry tells EventSource how long to wait before reconnecting. The default
	// is several seconds, which would make every reload feel broken.
	fmt.Fprintf(w, "retry: 250\ndata: {\"boot\":%q}\n\n", bootID)
	if err := rc.Flush(); err != nil {
		return
	}

	// Keepalives stop an idle proxy deciding the connection is dead.
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		}
	}
}

func serveScript(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(clientScript))
}

const clientScript = `(function () {
	"use strict";

	var boot = null;
	var source = new EventSource(` + "\"" + EventPath + "\"" + `);

	source.onmessage = function (event) {
		var data;
		try {
			data = JSON.parse(event.data);
		} catch (err) {
			return;
		}
		if (boot === null) {
			boot = data.boot;
			return;
		}
		// A different boot id means the server was replaced, so the page is
		// stale. EventSource reconnects on its own after a restart.
		if (data.boot !== boot) {
			window.location.reload();
		}
	};
})();
`

var scriptTag = []byte(`<script src="` + ScriptPath + `" defer></script>`)

// injector buffers HTML responses so the reload script can be added, and passes
// everything else straight through.
type injector struct {
	http.ResponseWriter

	status  int
	html    bool
	wrote   bool
	buf     bytes.Buffer
	flushed bool
}

func (i *injector) WriteHeader(status int) {
	if i.wrote {
		return
	}
	i.wrote = true
	i.status = status

	contentType := i.Header().Get("Content-Type")
	i.html = strings.HasPrefix(strings.TrimSpace(strings.ToLower(contentType)), "text/html")

	if i.html {
		// The body is about to grow, so any length the handler set is wrong.
		// It is rewritten in finish once the final size is known.
		i.Header().Del("Content-Length")
		return
	}
	i.ResponseWriter.WriteHeader(status)
}

func (i *injector) Write(b []byte) (int, error) {
	if !i.wrote {
		i.WriteHeader(http.StatusOK)
	}
	if i.html {
		return i.buf.Write(b)
	}
	return i.ResponseWriter.Write(b)
}

// Flush passes through for non-HTML responses. An HTML response is held until
// finish, since the script has to go in before anything is sent.
func (i *injector) Flush() {
	if i.html {
		return
	}
	i.flushed = true
	_ = http.NewResponseController(i.ResponseWriter).Flush()
}

// Unwrap keeps http.ResponseController working for handlers that hijack or
// control deadlines.
func (i *injector) Unwrap() http.ResponseWriter { return i.ResponseWriter }

func (i *injector) finish() {
	if !i.html {
		if !i.wrote {
			i.ResponseWriter.WriteHeader(i.status)
		}
		return
	}

	body := inject(i.buf.Bytes())
	i.Header().Set("Content-Length", strconv.Itoa(len(body)))
	i.ResponseWriter.WriteHeader(i.status)
	_, _ = i.ResponseWriter.Write(body)
}

// inject places the script tag just before </body>, falling back to appending
// it. Appending still works: a script at the end of the document runs.
func inject(body []byte) []byte {
	marker := []byte("</body>")
	idx := bytes.LastIndex(body, marker)
	if idx < 0 {
		if marker := []byte("</BODY>"); bytes.Contains(body, marker) {
			idx = bytes.LastIndex(body, marker)
		}
	}
	if idx < 0 {
		return append(body, scriptTag...)
	}

	out := make([]byte, 0, len(body)+len(scriptTag))
	out = append(out, body[:idx]...)
	out = append(out, scriptTag...)
	out = append(out, body[idx:]...)
	return out
}
