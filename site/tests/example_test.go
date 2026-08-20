// Package tests holds the application's feature tests.
//
// A feature test drives the application the way a browser does: the request
// goes through the real router and the whole middleware stack — sessions, CSRF,
// logging, recovery — rather than calling a handler function directly. That is
// what makes one worth writing. A handler called in isolation cannot tell you
// that its route is registered, that the middleware order is right, or that the
// form it renders is accepted by the CSRF check.
//
// Run them with `go test ./...`.
package tests

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	rytest "github.com/Dshonored/ryla/testing"

	"ryla-site/app"
	"ryla-site/resources/views"
	"ryla-site/routes"
)

var (
	buildOnce sync.Once
	built     *app.App
)

// application builds the whole application against a throwaway in-memory
// database with the migrations applied.
//
// Once for the test binary, not once per test: jobs and named routes register
// themselves into process-wide registries that refuse a duplicate, so a second
// app.New in the same process panics rather than quietly costing time. The
// trade is that tests share one database — clean up anything you write, or read
// it back through a fresh client rather than assuming an empty table.
func application(t *testing.T) *app.App {
	t.Helper()

	buildOnce.Do(func() {
		// Points DB_* at the throwaway database and fills in the rest of a
		// configuration that is safe to test against: a published signing key,
		// mail that is logged rather than sent, and no queue worker needed.
		// Real environment variables beat .env, so this wins over the file.
		rytest.Env(t)

		a, err := app.New()
		if err != nil {
			t.Fatalf("build the application: %v", err)
		}
		rytest.Migrate(t, a.DB)

		// The same call cmd/app makes. Without it the router has no routes and
		// every request below would answer 404.
		routes.Register(a)

		built = a
	})

	if built == nil {
		t.Fatal("the application could not be built; see the failure above")
	}
	return built
}

// client returns a browser-like client with its own cookie jar, so a test can
// never arrive already signed in because a different one logged in.
//
// It carries the application's CSRF token on every unsafe request, so a POST to
// a real form route works without hand-building one. To start from a signed-in
// user — which most tests worth writing do — call client(t).LoginID(user.ID).
func client(t *testing.T) *rytest.Client {
	t.Helper()

	a := application(t)
	return rytest.New(t, rytest.Config{
		Handler: a.Router,
		Jar:     a.Cookies,
		Session: a.Session,
	})
}

// TestTheLandingPageRenders is the smallest end-to-end proof there is: the
// application boots, the route is registered, and the view compiles and renders.
//
// It asserts on the offer and the call to action rather than on decoration,
// because those are the two things the page exists to do — a landing page that
// renders without its headline or its install command has failed while
// returning 200.
func TestTheLandingPageRenders(t *testing.T) {
	client(t).Get("/").
		AssertOK().
		AssertContains(
			"Laravel's productivity",
			"ryla.io/",
			"install.sh | sh",
			"Dshonored/ryla",
		)
}

// TestTheRepositoryIsNamedInBothTheHeaderAndTheFooter guards a decision that is
// easy to undo by accident while editing markup: a visitor should be able to
// find the source from either end of the page without scrolling to the other.
func TestTheRepositoryIsNamedInBothTheHeaderAndTheFooter(t *testing.T) {
	body := client(t).Get("/").AssertOK().Body()

	const repo = "Dshonored/ryla"
	head, foot, ok := strings.Cut(body, "<footer")
	if !ok {
		t.Fatal("the page has no footer")
	}
	if !strings.Contains(head, repo) {
		t.Error("the repository is not named above the footer")
	}
	if !strings.Contains(foot, repo) {
		t.Error("the repository is not named in the footer")
	}
}

// TestTheInstallScriptIsServedVerbatim covers the most consequential route on
// the site. Its address is published in the README and shown in the hero, and
// people pipe what it returns straight into a shell — so it has to be the real
// script, and it has to render in a browser rather than download, so that
// reading it first is one click.
func TestTheInstallScriptIsServedVerbatim(t *testing.T) {
	r := client(t).Get("/install.sh").
		AssertOK().
		AssertHeader("Content-Type", "text/plain; charset=utf-8")

	if !strings.HasPrefix(r.Body(), "#!/bin/sh") {
		t.Errorf("the install script does not begin with a shebang; got %.40q", r.Body())
	}

	// The copy served here is vendored from the repository root at build time.
	// If that step is ever dropped, this is what notices.
	want, err := os.ReadFile(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Skipf("the repository install.sh is not readable from here: %v", err)
	}
	if r.Body() != string(want) {
		t.Error("the served install script differs from the repository's install.sh")
	}
}

// TestTheCrawlerFilesAgree checks the three addresses read by something other
// than a person. They are cheap to get subtly wrong — a sitemap naming the
// wrong origin, or a robots.txt pointing at a sitemap that is not there — and
// nothing about the page looks broken when they are.
func TestTheCrawlerFilesAgree(t *testing.T) {
	const origin = "https://ryla.io"

	client(t).Get("/robots.txt").
		AssertOK().
		AssertHeader("Content-Type", "text/plain; charset=utf-8").
		AssertContains("User-agent: *", "Sitemap: "+origin+"/sitemap.xml")

	client(t).Get("/sitemap.xml").
		AssertOK().
		AssertHeader("Content-Type", "application/xml; charset=utf-8").
		AssertContains("<loc>" + origin + "/</loc>")

	client(t).Get("/llms.txt").
		AssertOK().
		AssertHeader("Content-Type", "text/plain; charset=utf-8").
		AssertContains(
			"github.com/Dshonored/ryla",
			origin+"/install.sh",
			// The facts a model is most likely to repeat, and most likely to
			// get wrong.
			"no service container",
			"pre-1.0",
		)
}

// TestThePageDeclaresItselfToSearchAndSocial covers the tags nobody looks at
// until a link has already been shared badly.
func TestThePageDeclaresItselfToSearchAndSocial(t *testing.T) {
	client(t).Get("/").
		AssertOK().
		AssertContains(
			`<link rel="canonical" href="https://ryla.io/">`,
			`<meta property="og:url" content="https://ryla.io/">`,
			`<meta name="twitter:card"`,
			`content="https://ryla.io/static/og.png"`,
			`application/ld+json`,
			`"@type":"SoftwareSourceCode"`,
		)
}

// TestTheSearchConsoleTagSurvives guards the one tag whose absence is silent.
//
// Google re-checks ownership periodically, so a refactor that drops this from
// the head does not break the page or fail a build — it quietly unverifies the
// property weeks later, and the first symptom is search data going missing.
func TestTheSearchConsoleTagSurvives(t *testing.T) {
	client(t).Get("/").
		AssertOK().
		AssertContains(`<meta name="google-site-verification" content="` + views.SearchConsoleToken + `">`)
}

// TestTheHealthEndpointReportsOK covers the route a load balancer polls. It is
// the one endpoint whose contract belongs to something outside this codebase,
// so a change to its shape should break a test rather than a deployment.
func TestTheHealthEndpointReportsOK(t *testing.T) {
	var body struct {
		Status string `json:"status"`
	}

	client(t).Get("/up").
		AssertOK().
		AssertHeader("Content-Type", "application/json").
		AssertJSON(&body)

	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
}
