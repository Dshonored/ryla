package middleware

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ok is the handler behind the middleware. It writes a body so a test can tell
// "redirected" from "served" without reading the status alone.
func ok() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("#!/bin/sh\n"))
	})
}

func request(proto string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "http://ryla.io/install.sh?x=1", nil)
	if proto != "" {
		r.Header.Set("X-Forwarded-Proto", proto)
	}
	return r
}

// TestPlaintextIsRedirectedRatherThanServed is the whole point of this
// middleware. The body it withholds is an install script that the command
// beside it pipes into a shell, so answering a plaintext request with content
// hands whatever is between the visitor and here the choice of what runs.
func TestPlaintextIsRedirectedRatherThanServed(t *testing.T) {
	rec := httptest.NewRecorder()
	ForceHTTPS()(ok()).ServeHTTP(rec, request("http"))

	if rec.Code != http.StatusPermanentRedirect {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusPermanentRedirect)
	}
	if got, want := rec.Header().Get("Location"), "https://ryla.io/install.sh?x=1"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	if strings.Contains(rec.Body.String(), "#!/bin/sh") {
		t.Error("the script was served before the redirect")
	}
}

// TestASecureRequestIsServedAndToldToStaySecure checks the header that stops
// the redirect from being needed a second time.
func TestASecureRequestIsServedAndToldToStaySecure(t *testing.T) {
	rec := httptest.NewRecorder()
	ForceHTTPS()(ok()).ServeHTTP(rec, request("https"))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if hsts := rec.Header().Get("Strict-Transport-Security"); !strings.Contains(hsts, "max-age=") {
		t.Errorf("Strict-Transport-Security = %q, want a max-age", hsts)
	}
}

// TestAProxyListIsReadFromTheLeft covers the header as several proxies leave
// it: "https, http" means the browser's own hop was TLS, whatever happened
// after it.
func TestAProxyListIsReadFromTheLeft(t *testing.T) {
	rec := httptest.NewRecorder()
	ForceHTTPS()(ok()).ServeHTTP(rec, request("https, http"))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want the request treated as secure", rec.Code)
	}
}

// TestAMissingHeaderIsLeftAlone is what keeps `ry dev` on a laptop working, and
// what stops a redirect loop if whatever ends up in front of this never sets
// the header: nothing said the request was plaintext, so there is no https
// address to send it to.
func TestAMissingHeaderIsLeftAlone(t *testing.T) {
	rec := httptest.NewRecorder()
	ForceHTTPS()(ok()).ServeHTTP(rec, request(""))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want the request served", rec.Code)
	}
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("a request with no proxy in front of it was promised HSTS")
	}
}

// TestAnEmptyHeaderCountsAsAbsent covers a proxy that sets the header to
// nothing, which is a proxy that has told us nothing.
func TestAnEmptyHeaderCountsAsAbsent(t *testing.T) {
	rec := httptest.NewRecorder()

	req := request("")
	req.Header.Set("X-Forwarded-Proto", "")
	ForceHTTPS()(ok()).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want the request served", rec.Code)
	}
}

// TestDirectTLSIsNotRedirected covers the day this stops sitting behind a proxy
// that terminates TLS for it. The connection is already secure, and bouncing it
// to the address it is already at is a loop.
func TestDirectTLSIsNotRedirected(t *testing.T) {
	rec := httptest.NewRecorder()

	req := request("http")
	req.TLS = &tls.ConnectionState{}
	ForceHTTPS()(ok()).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want the request served", rec.Code)
	}
}

// TestCloudflareVisitorSchemeWins is the header that made the difference.
//
// Cloudflare terminates TLS at its edge and reaches this process over its own
// connection, so X-Forwarded-Proto says https for a visitor who typed http.
// Reading it alone meant the redirect never fired in production while looking,
// from the inside and from the tests, exactly as though it had.
func TestCloudflareVisitorSchemeWins(t *testing.T) {
	rec := httptest.NewRecorder()

	req := request("https") // what Cloudflare tells the origin either way
	req.Header.Set("CF-Visitor", `{"scheme":"http"}`)
	ForceHTTPS()(ok()).ServeHTTP(rec, req)

	if rec.Code != http.StatusPermanentRedirect {
		t.Errorf("status = %d, want a redirect for a plaintext visitor", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "#!/bin/sh") {
		t.Error("the script was served to a plaintext visitor")
	}
}

// TestASecureVisitorBehindCloudflareIsServed is the same header saying the
// visitor was already on https, which must not become a redirect to the address
// they are on.
func TestASecureVisitorBehindCloudflareIsServed(t *testing.T) {
	rec := httptest.NewRecorder()

	req := request("https")
	req.Header.Set("CF-Visitor", `{"scheme":"https"}`)
	ForceHTTPS()(ok()).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Header().Get("Strict-Transport-Security") == "" {
		t.Error("a secure visitor was not told to stay secure")
	}
}

// TestAnUnreadableVisitorHeaderFallsBack covers the header arriving as
// something other than the JSON object it is documented to be. Falling back to
// X-Forwarded-Proto keeps the old behaviour rather than deciding the request
// came from nowhere.
func TestAnUnreadableVisitorHeaderFallsBack(t *testing.T) {
	rec := httptest.NewRecorder()

	req := request("http")
	req.Header.Set("CF-Visitor", "not-json")
	ForceHTTPS()(ok()).ServeHTTP(rec, req)

	if rec.Code != http.StatusPermanentRedirect {
		t.Errorf("status = %d, want the X-Forwarded-Proto answer", rec.Code)
	}
}
