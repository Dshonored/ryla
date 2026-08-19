package session

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Dshonored/ryla/cookie"
)

func testStore() *CookieStore {
	return NewCookieStore(cookie.New("test-key", false), time.Hour)
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// serve runs a handler through the middleware and returns the response.
func serve(t *testing.T, store Store, req *http.Request, h http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	Middleware(store, quietLogger())(h).ServeHTTP(rec, req)
	return rec
}

// next carries cookies from a response into a following request.
func next(rec *httptest.ResponseRecorder) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge >= 0 {
			r.AddCookie(c)
		}
	}
	return r
}

func TestValuesSurviveAcrossRequests(t *testing.T) {
	store := testStore()

	first := serve(t, store, httptest.NewRequest(http.MethodGet, "/", nil),
		func(w http.ResponseWriter, r *http.Request) {
			From(r.Context()).Put("colour", "blue")
		})

	var got string
	serve(t, store, next(first), func(w http.ResponseWriter, r *http.Request) {
		got = From(r.Context()).Get("colour")
	})

	if got != "blue" {
		t.Errorf("value = %q, want %q", got, "blue")
	}
}

// TestSessionIsSavedBeforeARedirect is the case that makes or breaks login: a
// handler that redirects commits its headers immediately, so a session saved
// after the handler returns would never reach the browser.
func TestSessionIsSavedBeforeARedirect(t *testing.T) {
	store := testStore()

	rec := serve(t, store, httptest.NewRequest(http.MethodPost, "/login", nil),
		func(w http.ResponseWriter, r *http.Request) {
			From(r.Context()).Put("auth_user_id", "42")
			http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		})

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(rec.Result().Cookies()) == 0 {
		t.Fatal("no session cookie was written before the redirect")
	}

	var got string
	serve(t, store, next(rec), func(w http.ResponseWriter, r *http.Request) {
		got = From(r.Context()).Get("auth_user_id")
	})
	if got != "42" {
		t.Errorf("the session did not survive the redirect: %q", got)
	}
}

func TestUnchangedSessionIsNotRewritten(t *testing.T) {
	store := testStore()

	first := serve(t, store, httptest.NewRequest(http.MethodGet, "/", nil),
		func(w http.ResponseWriter, r *http.Request) {
			From(r.Context()).Put("k", "v")
		})

	// Reading without changing anything must not cost a write or refresh the
	// cookie on every single request.
	second := serve(t, store, next(first), func(w http.ResponseWriter, r *http.Request) {
		_ = From(r.Context()).Get("k")
	})

	if len(second.Result().Cookies()) != 0 {
		t.Error("an unmodified session rewrote its cookie")
	}
}

func TestRegenerateChangesIDAndKeepsData(t *testing.T) {
	s := New(time.Hour)
	s.Put("user", "alice")
	before := s.ID

	s.Regenerate()

	if s.ID == before {
		t.Error("the id did not change")
	}
	// Keeping the data is what lets login regenerate without losing the flash
	// message it is about to set.
	if s.Get("user") != "alice" {
		t.Error("data was lost on regenerate")
	}
}

func TestInvalidateClearsData(t *testing.T) {
	s := New(time.Hour)
	s.Put("user", "alice")
	before := s.ID

	s.Invalidate()

	if s.ID == before {
		t.Error("the id did not change")
	}
	if s.Get("user") != "" {
		t.Error("data survived invalidation")
	}
}

func TestExpiredSessionIsNotLoaded(t *testing.T) {
	store := NewCookieStore(cookie.New("test-key", false), -time.Hour)

	first := serve(t, store, httptest.NewRequest(http.MethodGet, "/", nil),
		func(w http.ResponseWriter, r *http.Request) {
			From(r.Context()).Put("k", "v")
		})

	var got string
	serve(t, store, next(first), func(w http.ResponseWriter, r *http.Request) {
		got = From(r.Context()).Get("k")
	})

	if got != "" {
		t.Errorf("an expired session was loaded: %q", got)
	}
}

func TestTamperedCookieStartsFresh(t *testing.T) {
	store := testStore()

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "forged.value"})

	var authenticated bool
	serve(t, store, r, func(w http.ResponseWriter, r *http.Request) {
		authenticated = From(r.Context()).Has("auth_user_id")
	})

	if authenticated {
		t.Error("a tampered cookie produced an authenticated session")
	}
}

func TestFromWithoutMiddlewareIsUsable(t *testing.T) {
	// A detached session is far easier to debug than a nil dereference in a
	// handler someone forgot to wrap.
	s := From(httptest.NewRequest(http.MethodGet, "/", nil).Context())
	if s == nil {
		t.Fatal("From returned nil")
	}
	s.Put("k", "v")
	if s.Get("k") != "v" {
		t.Error("the detached session does not work")
	}
}

func TestTypedHelpers(t *testing.T) {
	s := New(time.Hour)

	s.PutInt("count", 42)
	if got := s.GetInt("count", 0); got != 42 {
		t.Errorf("GetInt = %d", got)
	}
	if got := s.GetInt("absent", 7); got != 7 {
		t.Errorf("GetInt default = %d, want 7", got)
	}

	s.PutBool("admin", true)
	if !s.GetBool("admin") {
		t.Error("GetBool = false")
	}
	if s.GetBool("absent") {
		t.Error("GetBool on an absent key = true")
	}
}

func TestPullRemoves(t *testing.T) {
	s := New(time.Hour)
	s.Put("k", "v")

	if got := s.Pull("k"); got != "v" {
		t.Errorf("Pull = %q", got)
	}
	if s.Has("k") {
		t.Error("Pull did not remove the key")
	}
}

func TestOversizedSessionIsRejected(t *testing.T) {
	store := testStore()
	store.MaxBytes = 100

	// Browsers silently drop a cookie over ~4 KB, which would look like the
	// session randomly vanishing. Failing loudly points at the real fix.
	rec := serve(t, store, httptest.NewRequest(http.MethodGet, "/", nil),
		func(w http.ResponseWriter, r *http.Request) {
			s := From(r.Context())
			for i := 0; i < 50; i++ {
				s.Put(string(rune('a'+i%26))+"key", "some reasonably long value here")
			}
		})

	if len(rec.Result().Cookies()) != 0 {
		t.Error("an oversized session was written to a cookie anyway")
	}
}

// TestUntouchedSessionIsNeverPersisted keeps the sessions table from filling up
// with rows for visitors who never had anything stored — crawlers above all.
func TestUntouchedSessionIsNeverPersisted(t *testing.T) {
	store := testStore()

	rec := serve(t, store, httptest.NewRequest(http.MethodGet, "/", nil),
		func(w http.ResponseWriter, r *http.Request) {
			// A public page that reads nothing and writes nothing.
			_, _ = w.Write([]byte("hello"))
		})

	if len(rec.Result().Cookies()) != 0 {
		t.Error("a session was persisted for a request that never touched it")
	}
}

func TestReadingDoesNotDirtyASession(t *testing.T) {
	store := testStore()

	rec := serve(t, store, httptest.NewRequest(http.MethodGet, "/", nil),
		func(w http.ResponseWriter, r *http.Request) {
			s := From(r.Context())
			_ = s.Get("absent")
			_ = s.Has("absent")
			_ = s.GetInt("absent", 0)
		})

	if len(rec.Result().Cookies()) != 0 {
		t.Error("reading from a session marked it dirty")
	}
}

func TestWritingPersistsTheSession(t *testing.T) {
	store := testStore()

	rec := serve(t, store, httptest.NewRequest(http.MethodGet, "/", nil),
		func(w http.ResponseWriter, r *http.Request) {
			From(r.Context()).Put("k", "v")
		})

	if len(rec.Result().Cookies()) == 0 {
		t.Error("a session that was written to was not persisted")
	}
}
