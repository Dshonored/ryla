package csrf

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Dshonored/ryla/cookie"
)

func handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
}

func protected(cfg Config) http.Handler {
	if cfg.Jar == nil {
		cfg.Jar = cookie.New("test-key", false)
	}
	return Protect(cfg)(handler())
}

// issue performs a GET to obtain a token and its cookie, as a browser loading a
// form would.
func issue(t *testing.T, h http.Handler) (token string, jarCookie *http.Cookie) {
	t.Helper()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/form", nil)

	var captured string
	inner := Protect(Config{Jar: cookie.New("test-key", false)})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			captured = Token(r.Context())
		}))
	inner.ServeHTTP(rec, req)

	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no CSRF cookie was issued")
	}
	return captured, cookies[0]
}

func TestSafeMethodsPass(t *testing.T) {
	h := protected(Config{})

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, "/", nil))

		if rec.Code != http.StatusOK {
			t.Errorf("%s was rejected with %d", method, rec.Code)
		}
	}
}

func TestTokenIsIssuedAndReadable(t *testing.T) {
	token, c := issue(t, nil)

	if token == "" {
		t.Fatal("no token was placed on the context")
	}
	if c.Name != CookieName {
		t.Errorf("cookie name = %q, want %q", c.Name, CookieName)
	}
	// The token cookie must be readable by scripts, or a fetch() cannot send
	// the header. Its secrecy is not what protects it — same-origin policy is.
	if c.HttpOnly {
		t.Error("the CSRF cookie must not be HttpOnly")
	}
}

func TestPostWithoutTokenIsRejected(t *testing.T) {
	h := protected(Config{})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestPostWithFormTokenIsAccepted(t *testing.T) {
	token, c := issue(t, nil)
	h := protected(Config{})

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(FieldName+"="+token))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(c)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
}

func TestPostWithHeaderTokenIsAccepted(t *testing.T) {
	token, c := issue(t, nil)
	h := protected(Config{})

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set(HeaderName, token)
	req.AddCookie(c)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestForgedTokenIsRejected is the attack the package exists to stop: a
// cross-site form causes the browser to send the cookie, but the attacker
// cannot read it, so the value they submit is a guess.
func TestForgedTokenIsRejected(t *testing.T) {
	_, c := issue(t, nil)
	h := protected(Config{})

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(FieldName+"=guessed-value"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(c)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("a mismatched token was accepted with status %d", rec.Code)
	}
}

func TestTamperedCookieIsRejected(t *testing.T) {
	token, _ := issue(t, nil)
	h := protected(Config{})

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(FieldName+"="+token))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CookieName, Value: "forged.signature"})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// An unreadable cookie causes a fresh token to be issued, which cannot
	// match the submitted one.
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestExistingTokenIsReused(t *testing.T) {
	jar := cookie.New("test-key", false)
	h := Protect(Config{Jar: jar})(handler())

	_, c := issue(t, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(c)
	h.ServeHTTP(rec, req)

	// Rotating the token on every request would invalidate a form the moment
	// the user opened a second tab.
	if len(rec.Result().Cookies()) != 0 {
		t.Error("a new token was issued despite a valid one being present")
	}
}

func TestSkipBypassesTheCheck(t *testing.T) {
	h := protected(Config{
		Skip: func(r *http.Request) bool { return strings.HasPrefix(r.URL.Path, "/api/") },
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/things", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("skipped path was rejected with %d", rec.Code)
	}
}

func TestFieldRendersHiddenInput(t *testing.T) {
	var field string
	h := Protect(Config{Jar: cookie.New("test-key", false)})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			field = Field(r.Context())
		}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if !strings.Contains(field, `name="`+FieldName+`"`) || !strings.Contains(field, "hidden") {
		t.Errorf("Field() = %q", field)
	}
}
