package mongo

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/Dshonored/ryla/cookie"
	rylamongo "github.com/Dshonored/ryla/mongo"
	"github.com/Dshonored/ryla/session"
)

func testJar() *cookie.Jar { return cookie.New("test-key", false) }

// next carries the cookies from a response into a following request, the way a
// browser would.
func next(rec *httptest.ResponseRecorder) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge >= 0 {
			r.AddCookie(c)
		}
	}
	return r
}

// --- unit tests, no server needed -----------------------------------------

// TestNoCookieCostsNoQuery protects the common case: most requests to a public
// page carry no session cookie, and none of them should reach the database. The
// store has no collection here, so a query would panic rather than pass.
func TestNoCookieCostsNoQuery(t *testing.T) {
	s := &Store{Jar: testJar(), TTL: time.Hour}

	_, err := s.Load(httptest.NewRequest(http.MethodGet, "/", nil))
	if !errors.Is(err, session.ErrNotFound) {
		t.Errorf("Load without a cookie = %v, want ErrNotFound", err)
	}
}

// TestForgedCookieCostsNoQuery keeps an unauthenticated attacker from turning
// the session cookie into free database load, and from being handed a session
// at all.
func TestForgedCookieCostsNoQuery(t *testing.T) {
	s := &Store{Jar: testJar(), TTL: time.Hour}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: session.CookieName, Value: "forged.value"})

	_, err := s.Load(r)
	if !errors.Is(err, session.ErrNotFound) {
		t.Errorf("Load with a forged cookie = %v, want ErrNotFound", err)
	}
}

// TestDestroyForUserRefusesAnEmptyID guards against the worst possible typo: an
// empty id matches every session with no user, so obeying it would log out
// every visitor on the site.
func TestDestroyForUserRefusesAnEmptyID(t *testing.T) {
	s := &Store{Jar: testJar(), TTL: time.Hour}

	if err := s.DestroyForUser(context.Background(), ""); err == nil {
		t.Error("an empty user id was accepted")
	}
}

// TestDestroyWithoutASessionStillClearsTheCookie matters for a browser holding a
// cookie whose document has already gone: without this it would keep presenting
// a dead id on every request.
func TestDestroyWithoutASessionStillClearsTheCookie(t *testing.T) {
	s := &Store{Jar: testJar(), TTL: time.Hour}
	rec := httptest.NewRecorder()

	if err := s.Destroy(rec, httptest.NewRequest(http.MethodGet, "/", nil), nil); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge >= 0 {
		t.Errorf("the session cookie was not cleared: %+v", cookies)
	}
}

func TestLifetimeIsTheConfiguredTTL(t *testing.T) {
	if got := (&Store{TTL: 90 * time.Minute}).Lifetime(); got != 90*time.Minute {
		t.Errorf("Lifetime = %v", got)
	}
}

// TestIndexesAreDeclared checks the declarations importing this package brings
// with it. Without the TTL index expired sessions accumulate forever; without
// the user_id index "log out everywhere" is a collection scan.
func TestIndexesAreDeclared(t *testing.T) {
	var ttl, user bool
	for _, idx := range rylamongo.Registered() {
		if idx.Collection != CollectionName {
			continue
		}
		switch idx.Keys[0].Key {
		case "expires_at":
			ttl = idx.TTL > 0
		case "user_id":
			user = true
		}
	}

	if !ttl {
		t.Error("no TTL index on expires_at was declared")
	}
	if !user {
		t.Error("no index on user_id was declared")
	}
}

// --- integration tests ------------------------------------------------------

// live connects to a real MongoDB, or skips.
//
// There is no in-process MongoDB the way miniredis stands in for Redis, so these
// run where a server exists — CI provides one — and are skipped elsewhere rather
// than silently not testing anything.
func live(t *testing.T) *rylamongo.Database {
	t.Helper()

	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		t.Skip("skipping: set MONGO_TEST_URI to run the MongoDB integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// A database of its own, so these tests and any other package's cannot drop
	// each other's data when they run at the same time.
	client, db, err := rylamongo.Open(ctx, rylamongo.Config{URI: uri, Database: "ryla_session_test"})
	if err != nil {
		t.Fatalf("connect to %s: %v", uri, err)
	}
	t.Cleanup(func() {
		_ = db.Drop(context.Background())
		_ = client.Disconnect(context.Background())
	})

	// A fresh database per test, so one leaving documents behind cannot change
	// what the next one sees.
	if err := db.Drop(ctx); err != nil {
		t.Fatalf("drop: %v", err)
	}
	return db
}

func liveStore(t *testing.T) *Store {
	t.Helper()
	return New(live(t), testJar(), time.Hour)
}

// save writes a session through the store and returns the response carrying its
// cookie.
func save(t *testing.T, s *Store, sess *session.Session) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	if err := s.Save(rec, httptest.NewRequest(http.MethodGet, "/", nil), sess); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return rec
}

// raw reads a stored document as the server holds it, for the fields the typed
// Document deliberately hides.
func raw(t *testing.T, s *Store, id string) bson.M {
	t.Helper()

	var out bson.M
	if err := s.Sessions.C.FindOne(context.Background(), bson.M{"_id": id}).Decode(&out); err != nil {
		t.Fatalf("read document %q: %v", id, err)
	}
	return out
}

func TestLiveSessionSurvivesAcrossRequests(t *testing.T) {
	s := liveStore(t)

	sess := session.New(s.Lifetime())
	sess.Put("colour", "blue")
	rec := save(t, s, sess)

	got, err := s.Load(next(rec))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.ID != sess.ID {
		t.Errorf("id = %q, want %q", got.ID, sess.ID)
	}
	if got.Get("colour") != "blue" {
		t.Errorf("value = %q, want %q", got.Get("colour"), "blue")
	}
}

// TestLiveOnlyTheIDIsInTheCookie is the whole reason to use this store over the
// cookie one: the browser holds a reference, not the data, so the server decides
// on every request whether the session is still valid.
func TestLiveOnlyTheIDIsInTheCookie(t *testing.T) {
	s := liveStore(t)

	sess := session.New(s.Lifetime())
	sess.Put("secret_ish", "the value")
	rec := save(t, s, sess)

	value, err := s.Jar.Get(next(rec), session.CookieName)
	if err != nil {
		t.Fatalf("read the cookie back: %v", err)
	}
	if value != sess.ID {
		t.Errorf("the cookie carries %q, want just the id %q", value, sess.ID)
	}
}

// TestLiveDestroyRevokesTheSession is what a cookie store cannot do: the browser
// still holds a valid, correctly signed cookie, and it must stop working the
// moment the server-side document goes.
func TestLiveDestroyRevokesTheSession(t *testing.T) {
	s := liveStore(t)

	sess := session.New(s.Lifetime())
	sess.Put("auth", "yes")
	rec := save(t, s, sess)
	held := next(rec)

	if err := s.Destroy(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil), sess); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	if _, err := s.Load(held); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("a destroyed session still loaded: %v", err)
	}
}

// TestLiveExpiredSessionIsNotLoaded protects against trusting MongoDB's reaper,
// which runs roughly once a minute: an expired document is routinely still
// there, and honouring it would extend every session by up to a minute.
func TestLiveExpiredSessionIsNotLoaded(t *testing.T) {
	s := liveStore(t)

	sess := session.New(s.Lifetime())
	sess.Put("auth", "yes")
	sess.ExpiresAt = time.Now().Add(-time.Minute)
	rec := save(t, s, sess)

	if _, err := s.Load(next(rec)); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("an expired session loaded: %v", err)
	}

	// Load also clears up as it goes, so an expired session nobody returns to is
	// the only work left for the TTL index.
	n, err := s.Sessions.Count(context.Background(), rylamongo.Filter{"_id": sess.ID})
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 0 {
		t.Error("the expired document was left behind")
	}
}

// TestLiveDestroyForUserEndsEverySessionOfThatUser is "log out everywhere", and
// what a password change depends on. Other users must not be touched.
func TestLiveDestroyForUserEndsEverySessionOfThatUser(t *testing.T) {
	s := liveStore(t)

	held := make([]*http.Request, 0, 3)
	for _, userID := range []string{"7", "7", "8"} {
		sess := session.New(s.Lifetime())
		sess.Put(session.UserIDKey, userID)
		held = append(held, next(save(t, s, sess)))
	}

	if err := s.DestroyForUser(context.Background(), "7"); err != nil {
		t.Fatalf("DestroyForUser: %v", err)
	}

	for i, r := range held[:2] {
		if _, err := s.Load(r); !errors.Is(err, session.ErrNotFound) {
			t.Errorf("session %d of the user survived: %v", i, err)
		}
	}
	if _, err := s.Load(held[2]); err != nil {
		t.Errorf("another user's session was destroyed: %v", err)
	}
}

// TestLiveUserIDIsStoredAtTheTopLevel protects what DestroyForUser relies on: an
// indexed field, not a value buried in the session data.
func TestLiveUserIDIsStoredAtTheTopLevel(t *testing.T) {
	s := liveStore(t)

	sess := session.New(s.Lifetime())
	sess.Put(session.UserIDKey, "42")
	save(t, s, sess)

	if got := raw(t, s, sess.ID)["user_id"]; got != "42" {
		t.Errorf("user_id = %v, want %q", got, "42")
	}
}

// TestLiveAnonymousSessionCarriesNoUserID keeps every crawler out of the user_id
// index, which is sparse precisely so it holds only sessions with a login.
func TestLiveAnonymousSessionCarriesNoUserID(t *testing.T) {
	s := liveStore(t)

	sess := session.New(s.Lifetime())
	sess.Put("colour", "blue")
	save(t, s, sess)

	if _, present := raw(t, s, sess.ID)["user_id"]; present {
		t.Error("an anonymous session was given a user_id field")
	}
}

// TestLiveLogoutClearsTheStoredUserID covers a session that is written again
// after its login is dropped: a stale user_id would leave the old account able
// to revoke it, and the new one unable to.
func TestLiveLogoutClearsTheStoredUserID(t *testing.T) {
	s := liveStore(t)

	sess := session.New(s.Lifetime())
	sess.Put(session.UserIDKey, "42")
	save(t, s, sess)

	sess.Forget(session.UserIDKey)
	save(t, s, sess)

	if _, present := raw(t, s, sess.ID)["user_id"]; present {
		t.Error("user_id survived the logout")
	}
}

// TestLiveSavingTwiceUpdatesOneDocument keeps a busy session from writing a new
// document per request, which would fill the collection and break revocation.
func TestLiveSavingTwiceUpdatesOneDocument(t *testing.T) {
	s := liveStore(t)

	sess := session.New(s.Lifetime())
	sess.Put("step", "one")
	save(t, s, sess)

	sess.Put("step", "two")
	rec := save(t, s, sess)

	n, err := s.Sessions.Count(context.Background(), rylamongo.Filter{})
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 1 {
		t.Errorf("%d documents after two saves, want 1", n)
	}

	got, err := s.Load(next(rec))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Get("step") != "two" {
		t.Errorf("step = %q, want %q", got.Get("step"), "two")
	}
}

// TestLiveRegenerateStartsANewDocument covers session fixation: after login the
// id changes, and the new id must be the one the browser and the store agree on.
func TestLiveRegenerateStartsANewDocument(t *testing.T) {
	s := liveStore(t)

	sess := session.New(s.Lifetime())
	sess.Put("cart", "abc")
	first := next(save(t, s, sess))
	before := sess.ID

	sess.Regenerate()
	second := next(save(t, s, sess))

	got, err := s.Load(second)
	if err != nil {
		t.Fatalf("Load after regenerate: %v", err)
	}
	if got.ID == before {
		t.Error("the cookie still carries the old id")
	}
	if got.Get("cart") != "abc" {
		t.Errorf("data was lost on regenerate: %q", got.Get("cart"))
	}

	// The old document is deliberately still valid until it expires or is
	// destroyed; what matters is that it is a different session.
	old, err := s.Load(first)
	if err == nil && old.ID != before {
		t.Error("the old cookie resolved to the new session")
	}
}

// TestLiveIndexSyncCreatesTheDeclaredIndexes checks the declarations actually
// apply to a server, since a TTL index MongoDB rejects would only show up as
// sessions never being collected.
func TestLiveIndexSyncCreatesTheDeclaredIndexes(t *testing.T) {
	db := live(t)
	ctx := context.Background()

	syncer := rylamongo.NewSyncer(db, quietLogger())
	if err := syncer.Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	status, err := syncer.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	want := map[string]bool{"sessions_expires_at_ttl": false, "sessions_user_id": false}
	for _, st := range status {
		if st.Collection == CollectionName {
			if _, ok := want[st.Name]; ok {
				want[st.Name] = true
			}
		}
	}
	for name, created := range want {
		if !created {
			t.Errorf("index %s was not created", name)
		}
	}
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
