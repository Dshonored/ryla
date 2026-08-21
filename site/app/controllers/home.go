package controllers

import (
	"net/http"
	"strings"

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
// One address, two representations: the page a person came for, and — when the
// request says `Accept: text/markdown` — the same offer as markdown, for a
// client that is going to strip the markup anyway. Vary: Accept goes on both,
// so no cache between here and the client can serve one to an audience that
// asked for the other.
func (c *Home) Index(w http.ResponseWriter, r *http.Request) {
	site := c.Site()

	if prefersMarkdown(r) {
		writeMarkdown(w, http.StatusOK, landingDoc(site).Markdown())
		return
	}

	negotiable(w, views.LandingMeta(site))
	c.Render(w, r, views.Landing(site, batteries, stats, statuses))
}

// Markdown handles GET /index.md: the landing page, by address rather than by
// negotiation.
func (c *Home) Markdown(w http.ResponseWriter, r *http.Request) {
	writeMarkdownAlias(w, views.Origin+"/", landingDoc(c.Site()).Markdown())
}

// landingDoc is the landing page as content.
//
// It is built from the same batteries, stats and statuses the page renders, so
// the markdown an agent reads cannot claim a feature the page does not, or miss
// one it added. The prose is written for a reader who wants the facts without
// the choreography.
func landingDoc(s views.Site) views.Doc {
	version := s.Version
	if version == "" {
		version = "dev"
	}

	batteryPoints := make([]views.Point, 0, len(batteries))
	for _, b := range batteries {
		batteryPoints = append(batteryPoints, views.Point{
			Term:   b.Name + " (`" + b.Cmd + "`)",
			Detail: b.Body,
		})
	}

	statusPoints := make([]views.Point, 0, len(statuses))
	for _, st := range statuses {
		statusPoints = append(statusPoints, views.Point{Term: st.State, Detail: st.What})
	}

	measurements := make([]string, 0, len(stats))
	for _, st := range stats {
		measurements = append(measurements, st.Label+": "+st.Value+" "+st.Unit)
	}

	return views.Doc{
		Slug:    "",
		Eyebrow: "Ryla",
		Title:   "Ryla — a batteries-included web framework for Go",
		Summary: views.Description,
		Sections: []views.Section{
			{
				Heading: "Install",
				Body: []string{
					"    curl -fsSL " + views.Origin + "/install.sh | sh",
					"The script installs a Go toolchain if the machine has none, installs `ry`, and " +
						"adds both to your shell's PATH, on macOS and Linux. On Windows, or with Go " +
						"already present:",
					"    go install github.com/Dshonored/ryla/cmd/ry@latest",
					"Current version: " + version + ". Module path: github.com/Dshonored/ryla. Licence: MIT.",
				},
			},
			{
				Heading: "One command",
				Body: []string{
					"`ry new` asks four questions — a database, a web mode, a frontend language and a " +
						"stylesheet — and wires the answers together. Every combination compiles: " +
						"twenty-four of them are scaffolded and built on every CI run, on Linux, macOS " +
						"and Windows.",
				},
				Points: []views.Point{
					{Term: "Database", Detail: "sqlite, postgres, mysql, mongo"},
					{Term: "Web mode", Detail: "mvc, api, react, svelte"},
					{Term: "Frontend", Detail: "TypeScript or JavaScript"},
					{Term: "Styling", Detail: "Tailwind or plain CSS"},
				},
			},
			{
				Heading: "Batteries",
				Body:    []string{"Not a list of packages you assemble. One framework where the session store, the queue driver and the mailer already know about each other."},
				Points:  batteryPoints,
			},
			{
				Heading: "No container, no facades",
				Body: []string{
					"`App` is a plain struct. Controllers hold a pointer to it and reach for what they " +
						"need, so every dependency is visible and the compiler checks all of it — " +
						"nothing resolves at runtime.",
					"Generators never edit your files. `ry make:controller` writes a new file and prints " +
						"the route lines to add, so `routes/web.go` stays a table of contents you can " +
						"trust. Views are compiled: templ turns components into Go, so a typo or a wrong " +
						"argument is a build error rather than a blank space in production.",
				},
			},
			{
				Heading: "Deploy",
				Body: []string{
					"`ry build` compiles the views, the frontend bundle and the static assets into a " +
						"single static binary that carries its own commands. The machine that runs it " +
						"needs no Go toolchain, no Node and no `ry`.",
					strings.Join(measurements, ". ") + ".",
				},
			},
			{
				Heading: "Status",
				Body:    []string{"Ryla is pre-1.0, and specific about it: features are sorted by how well each one is actually tested rather than by whether it is finished."},
				Points:  statusPoints,
			},
		},
		Links: views.HomeLinks(),
	}
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
