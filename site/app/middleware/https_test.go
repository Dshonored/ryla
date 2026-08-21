package middleware

import (
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
	ForceHTTPS(true)(ok()).ServeHTTP(rec, request("http"))

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

// TestAMissingHeaderIsTreatedAsPlaintext covers the request that arrives with
// nothing to say for itself. Assuming the best would mean any caller could skip
// the redirect by leaving the header off.
func TestAMissingHeaderIsTreatedAsPlaintext(t *testing.T) {
	rec := httptest.NewRecorder()
	ForceHTTPS(true)(ok()).ServeHTTP(rec, request(""))

	if rec.Code != http.StatusPermanentRedirect {
		t.Errorf("status = %d, want a redirect", rec.Code)
	}
}

// TestASecureRequestIsServedAndToldToStaySecure checks the header that stops
// the redirect from being needed a second time.
func TestASecureRequestIsServedAndToldToStaySecure(t *testing.T) {
	rec := httptest.NewRecorder()
	ForceHTTPS(true)(ok()).ServeHTTP(rec, request("https"))

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
	ForceHTTPS(true)(ok()).ServeHTTP(rec, request("https, http"))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want the request to be treated as secure", rec.Code)
	}
}

// TestItStaysOutOfTheWayWhenDisabled is what keeps `ry dev` on localhost
// working: there is no TLS there, and a redirect would send every request to an
// address that answers nothing.
func TestItStaysOutOfTheWayWhenDisabled(t *testing.T) {
	rec := httptest.NewRecorder()
	ForceHTTPS(false)(ok()).ServeHTTP(rec, request("http"))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want the request served", rec.Code)
	}
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("a plaintext development server promised HSTS")
	}
}
