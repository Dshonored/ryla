package flash

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Dshonored/ryla/cookie"
)

func store() *Store { return New(cookie.New("test-key", false)) }

// carry moves the cookies a response set onto the next request, the way a
// browser would across a redirect.
func carry(rec *httptest.ResponseRecorder) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge >= 0 {
			r.AddCookie(c)
		}
	}
	return r
}

func TestRoundTripAcrossRedirect(t *testing.T) {
	s := store()

	// The POST handler queues a message, then redirects.
	post := httptest.NewRecorder()
	s.Success(post, httptest.NewRequest(http.MethodPost, "/posts", nil), "Post created.")

	// The page the browser lands on reads it.
	get := httptest.NewRecorder()
	messages := s.Pull(get, carry(post))

	if len(messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(messages))
	}
	if messages[0].Text != "Post created." || messages[0].Kind != Success {
		t.Errorf("message = %+v", messages[0])
	}
}

// TestPullClearsMessages is the whole point of a flash: refreshing the page
// must not show the banner a second time.
func TestPullClearsMessages(t *testing.T) {
	s := store()

	post := httptest.NewRecorder()
	s.Info(post, httptest.NewRequest(http.MethodPost, "/", nil), "Saved.")

	first := httptest.NewRecorder()
	req := carry(post)
	if len(s.Pull(first, req)) != 1 {
		t.Fatal("first read returned nothing")
	}

	// The response deleting the cookie is what the browser applies next.
	second := httptest.NewRecorder()
	if got := s.Pull(second, carry(first)); len(got) != 0 {
		t.Errorf("a second read returned %d messages, want 0", len(got))
	}
}

func TestMultipleMessagesAccumulate(t *testing.T) {
	s := store()

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", nil)

	s.Success(rec, r, "Saved.")

	// Adding a second message has to see the first, which only works because
	// Add reads what is already pending.
	r2 := carry(rec)
	rec2 := httptest.NewRecorder()
	s.Warning(rec2, r2, "But check your email.")

	messages := s.Pull(httptest.NewRecorder(), carry(rec2))
	if len(messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(messages))
	}
	if messages[0].Kind != Success || messages[1].Kind != Warning {
		t.Errorf("order or kinds wrong: %+v", messages)
	}
}

func TestNoMessages(t *testing.T) {
	s := store()

	got := s.Pull(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if len(got) != 0 {
		t.Errorf("got %d messages from a bare request", len(got))
	}
}

func TestTamperedCookieIsIgnored(t *testing.T) {
	s := store()

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "forged.value"})

	// A bad flash cookie is not worth an error page — there is nothing useful
	// to tell the user, so it reads as no messages.
	if got := s.Pull(httptest.NewRecorder(), r); len(got) != 0 {
		t.Errorf("a tampered cookie produced %d messages", len(got))
	}
}

func TestAllKinds(t *testing.T) {
	s := store()

	for _, tc := range []struct {
		add  func(http.ResponseWriter, *http.Request, string)
		kind Kind
	}{
		{s.Success, Success},
		{s.Info, Info},
		{s.Warning, Warning},
		{s.Error, Error},
	} {
		rec := httptest.NewRecorder()
		tc.add(rec, httptest.NewRequest(http.MethodPost, "/", nil), "text")

		got := s.Pull(httptest.NewRecorder(), carry(rec))
		if len(got) != 1 || got[0].Kind != tc.kind {
			t.Errorf("kind %q did not round trip: %+v", tc.kind, got)
		}
	}
}
