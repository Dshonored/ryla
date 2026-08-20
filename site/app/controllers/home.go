package controllers

import (
	"net/http"

	"github.com/Dshonored/ryla"

	"ryla-site/app"
	"ryla-site/resources/views"
)

// Home serves the landing page.
type Home struct {
	Base
}

// NewHome builds the controller. Dependencies arrive through the App struct
// rather than being resolved from a container, so this wiring is visible in
// routes/web.go and checked by the compiler.
func NewHome(a *app.App) *Home {
	return &Home{Base{App: a}}
}

// Index handles GET /.
//
// The version comes from ryla.Version(), which reads the build info rather than
// a constant — so the badge in the header and the line in the footer cannot
// advertise a release that does not exist, and there is no number to remember
// to bump. A build from a local checkout reports "dev", which is the truth.
func (c *Home) Index(w http.ResponseWriter, r *http.Request) {
	site := views.Site{Version: ryla.Version(), Repo: "Dshonored/ryla"}
	c.Render(w, r, views.Landing(site, batteries, stats, statuses))
}

// What the framework already ships. This is content rather than configuration,
// so it lives beside the handler that renders it.
var batteries = []views.Battery{
	{Name: "Auth", Cmd: "ry make:auth", Body: "Registration, sign-in, email verification and password reset. Non-enumerable by wording and by timing."},
	{Name: "Two-factor", Cmd: "ry make:2fa", Body: "TOTP enrolment with a QR code, a challenge that holds the session, and single-use recovery codes."},
	{Name: "Queue", Cmd: "ry queue:work", Body: "Jobs with retries and backoff. Claims exclusively over SQL, Redis or Mongo — whichever the project uses."},
	{Name: "Migrations", Cmd: "ry migrate", Body: "Versioned Go files that carry their own schema snapshot, so history keeps doing what it did."},
	{Name: "Cache", Cmd: "cache.Remember", Body: "One Store interface over memory, the database, Redis or Mongo. Swap the driver with an env var."},
	{Name: "Mail", Cmd: "mail.Send", Body: "SMTP and a log driver, templ-rendered mail views, and queued sends that survive a restart."},
	{Name: "Scheduler", Cmd: "ry schedule:run", Body: "A cron registry with overlap prevention, so a slow task never runs twice at once."},
	{Name: "Validation", Cmd: "ry make:request", Body: "Bind and validate into a typed struct. The error bag reaches the view keyed by field name."},
	{Name: "Testing", Cmd: "ry make:test", Body: "Drives the real router with its own cookie jar and CSRF token. Failures print the response body."},
}

var stats = []views.Stat{
	{Label: "Artifact", Value: "1", Unit: "binary"},
	{Label: "Typical size", Value: "21.8", Unit: "MB"},
	{Label: "Runtime deps", Value: "0", Unit: "on the server"},
	{Label: "Verified builds", Value: "24", Unit: "per CI run"},
}

var statuses = []views.Status{
	{State: "Covered end to end", Tone: "#45d97a", What: "Every database × web-mode × language combination is scaffolded, built, vetted and tested on Linux, macOS and Windows."},
	{State: "Live-tested", Tone: "#45d97a", What: "MongoDB and its cache, session and queue stores run against a real mongo:7 container in CI."},
	{State: "Less proven", Tone: "#f5b942", What: "Postgres and MySQL compile and vet everywhere, but CI runs no migrations against a live server yet."},
	{State: "Not built", Tone: "#737373", What: "OAuth and social sign-in. Personal access tokens exist but have no generator wiring them up."},
}
