package testing_test

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/Dshonored/ryla/auth"
	"github.com/Dshonored/ryla/cookie"
	"github.com/Dshonored/ryla/csrf"
	"github.com/Dshonored/ryla/migrate"
	"github.com/Dshonored/ryla/router"
	"github.com/Dshonored/ryla/session"
	rytest "github.com/Dshonored/ryla/testing"
)

// widget stands in for a table an application's own migration would create.
type widget struct {
	ID   uint
	Name string
}

// Registered from init rather than from a test, because the migration registry
// is global to the test binary and Register panics on a duplicate id — which is
// exactly what a second pass under `go test -count=2` would produce.
func init() {
	migrate.Register("20260101000000_create_widgets",
		func(tx *gorm.DB) error { return tx.AutoMigrate(&widget{}) },
		func(tx *gorm.DB) error { return tx.Migrator().DropTable(&widget{}) },
	)
}

// stack builds an application-shaped handler: the session and CSRF middleware a
// generated project wires up, in the order it wires them.
func stack(t *testing.T) rytest.Config {
	t.Helper()

	jar := cookie.New(rytest.Key, false)
	store := session.NewCookieStore(jar, time.Hour)
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))

	r := router.New()
	r.Use(session.Middleware(store, discard))
	r.Use(csrf.Protect(csrf.Config{Jar: jar}))

	r.Get("/up", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	r.Get("/whoami", func(w http.ResponseWriter, req *http.Request) {
		if !auth.CheckRequest(req) {
			http.Error(w, "guest", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte("user " + auth.ID(session.From(req.Context()))))
	})
	r.Post("/notes", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/notes/"+req.FormValue("title"), http.StatusSeeOther)
	})
	r.Get("/counter", func(w http.ResponseWriter, req *http.Request) {
		s := session.From(req.Context())
		s.PutInt("count", s.GetInt("count", 0)+1)
		_, _ = w.Write([]byte("count=" + strconv.Itoa(s.GetInt("count", 0))))
	})

	return rytest.Config{Handler: r, Jar: jar, Session: store}
}

// TestEnvIsolatesEachTestsDatabase protects the property the whole harness rests
// on: two tests must never share a database, or one leaving rows behind
// silently changes what the next one sees.
func TestEnvIsolatesEachTestsDatabase(t *testing.T) {
	first := rytest.Env(t)
	second := rytest.DSN(t)

	if first == second {
		t.Fatalf("two DSNs from one test are identical: %s", first)
	}
	if !strings.Contains(first, "mode=memory") {
		t.Errorf("DSN %q is not in-memory; a test run would leave a database file behind", first)
	}
	if got := os.Getenv("DB_DSN"); got != first {
		t.Errorf("DB_DSN = %q, want the DSN Env returned", got)
	}
	if got := os.Getenv("APP_KEY"); got != rytest.Key {
		t.Errorf("APP_KEY = %q, want the published test key", got)
	}
}

// TestMigrateAppliesTheProjectsMigrations checks that a harness database has the
// schema the application's own migrations produce, rather than whatever
// AutoMigrate would infer from a model today.
func TestMigrateAppliesTheProjectsMigrations(t *testing.T) {
	gdb := rytest.OpenSQLite(t)
	rytest.Migrate(t, gdb)

	if !gdb.Migrator().HasTable(&widget{}) {
		t.Error("the registered migration did not run")
	}
}

func TestClientReachesRoutesThroughTheRealStack(t *testing.T) {
	c := rytest.New(t, stack(t))

	var body struct {
		Status string `json:"status"`
	}
	c.Get("/up").AssertOK().AssertHeader("Content-Type", "application/json").AssertJSON(&body)

	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
}

// TestCookiesSurviveBetweenRequests is what makes a multi-step test possible at
// all: without it every request would start a new session, and nothing that
// depends on the previous one could be asserted.
func TestCookiesSurviveBetweenRequests(t *testing.T) {
	c := rytest.New(t, stack(t))

	c.Get("/counter").AssertOK().AssertContains("count=1")
	c.Get("/counter").AssertOK().AssertContains("count=2")
}

// TestUnsafeRequestsCarryTheCSRFToken covers the trap that would otherwise meet
// everyone writing their first POST test: the real middleware stack answers a
// tokenless form submission with 403, and the failure says nothing about why.
func TestUnsafeRequestsCarryTheCSRFToken(t *testing.T) {
	c := rytest.New(t, stack(t))

	c.Post("/notes", url.Values{"title": {"hello"}}).AssertRedirect("/notes/hello")
}

// TestWithoutAJarTheCSRFCheckStillApplies keeps the convenience above from
// quietly disabling a protection: a client given no jar is rejected, not waved
// through.
func TestWithoutAJarTheCSRFCheckStillApplies(t *testing.T) {
	cfg := stack(t)
	cfg.Jar = nil

	c := rytest.New(t, cfg)
	c.Post("/notes", url.Values{"title": {"hello"}}).AssertStatus(http.StatusForbidden)
}

func TestLoginAuthenticatesTheFollowingRequests(t *testing.T) {
	c := rytest.New(t, stack(t))

	c.Get("/whoami").AssertStatus(http.StatusUnauthorized)

	c.LoginID(42)
	c.Get("/whoami").AssertOK().AssertContains("user 42")

	c.Logout()
	c.Get("/whoami").AssertStatus(http.StatusUnauthorized)
}

// TestAFailedAssertionShowsTheBody is the reason these assertions exist rather
// than a bare comparison: a 500 carrying the panic in its body says what broke,
// where "status = 500, want 200" sends you hunting through logs.
func TestAFailedAssertionShowsTheBody(t *testing.T) {
	spy := &recorder{TB: t}
	c := rytest.New(spy, stack(t))

	c.Get("/whoami").AssertOK()

	if len(spy.errors) != 1 {
		t.Fatalf("got %d failures, want 1", len(spy.errors))
	}
	for _, want := range []string{"GET", "/whoami", "status = 401", "guest"} {
		if !strings.Contains(spy.errors[0], want) {
			t.Errorf("the failure message does not mention %q:\n%s", want, spy.errors[0])
		}
	}
}

// recorder captures assertion failures instead of failing the surrounding test,
// so the quality of a failure message can itself be tested.
type recorder struct {
	testing.TB
	errors []string
}

func (r *recorder) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}
