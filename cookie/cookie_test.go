package cookie

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func roundTrip(t *testing.T, jar *Jar, name, value string) (string, error) {
	t.Helper()

	w := httptest.NewRecorder()
	jar.Set(w, name, value, Options{})

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range w.Result().Cookies() {
		r.AddCookie(c)
	}
	return jar.Get(r, name)
}

func TestRoundTrip(t *testing.T) {
	jar := New("test-key", false)

	for _, value := range []string{"hello", "", "with spaces and = signs", "unicode: café ☕"} {
		got, err := roundTrip(t, jar, "flash", value)
		if err != nil {
			t.Fatalf("Get(%q): %v", value, err)
		}
		if got != value {
			t.Errorf("round trip of %q gave %q", value, got)
		}
	}
}

func TestTamperingIsDetected(t *testing.T) {
	jar := New("test-key", false)

	w := httptest.NewRecorder()
	jar.Set(w, "flash", "user=alice", Options{})
	signed := w.Result().Cookies()[0].Value

	// Rewrite the payload but keep the original signature.
	_, sig, _ := strings.Cut(signed, ".")
	forged := "dXNlcj1hZG1pbg" + "." + sig

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "flash", Value: forged})

	if _, err := jar.Get(r, "flash"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tampered cookie returned %v, want ErrInvalid", err)
	}
}

// TestValueCannotBeMovedBetweenCookies is why the cookie name is part of the
// signed material. Without it, a value legitimately issued for one cookie could
// be replayed in another — handing a CSRF token to a session, for instance.
func TestValueCannotBeMovedBetweenCookies(t *testing.T) {
	jar := New("test-key", false)

	w := httptest.NewRecorder()
	jar.Set(w, "csrf", "token-value", Options{})
	signed := w.Result().Cookies()[0].Value

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "session", Value: signed})

	if _, err := jar.Get(r, "session"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a value signed for another cookie was accepted: %v", err)
	}
}

func TestDifferentKeyRejects(t *testing.T) {
	writer := New("key-one", false)
	reader := New("key-two", false)

	w := httptest.NewRecorder()
	writer.Set(w, "flash", "hello", Options{})

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range w.Result().Cookies() {
		r.AddCookie(c)
	}

	if _, err := reader.Get(r, "flash"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a cookie signed with another key was accepted: %v", err)
	}
}

func TestMissingCookie(t *testing.T) {
	jar := New("test-key", false)
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	if _, err := jar.Get(r, "absent"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestMalformedValues(t *testing.T) {
	jar := New("test-key", false)

	for _, value := range []string{"", "no-separator", ".", "a.b", "!!!.???"} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.AddCookie(&http.Cookie{Name: "flash", Value: value})

		if _, err := jar.Get(r, "flash"); err == nil {
			t.Errorf("malformed value %q was accepted", value)
		}
	}
}

func TestDefaultAttributes(t *testing.T) {
	jar := New("test-key", true)

	w := httptest.NewRecorder()
	jar.Set(w, "flash", "x", Options{})
	c := w.Result().Cookies()[0]

	if !c.Secure {
		t.Error("Secure was not set")
	}
	if !c.HttpOnly {
		t.Error("HttpOnly should default to true")
	}
	// Lax is what stops a cross-site POST carrying the cookie, which is half of
	// why CSRF protection works at all.
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}
	if c.Path != "/" {
		t.Errorf("Path = %q, want /", c.Path)
	}
}

func TestHTTPOnlyCanBeDisabled(t *testing.T) {
	jar := New("test-key", false)
	readable := false

	w := httptest.NewRecorder()
	jar.Set(w, "csrf", "x", Options{HTTPOnly: &readable})

	if w.Result().Cookies()[0].HttpOnly {
		t.Error("HttpOnly should be off when explicitly disabled")
	}
}

func TestDelete(t *testing.T) {
	jar := New("test-key", false)

	w := httptest.NewRecorder()
	jar.Delete(w, "flash")
	c := w.Result().Cookies()[0]

	if c.MaxAge >= 0 {
		t.Errorf("MaxAge = %d, want negative to delete", c.MaxAge)
	}
	if c.Value != "" {
		t.Errorf("Value = %q, want empty", c.Value)
	}
}
