package controllers

import (
	"net/http"
	"strings"

	"github.com/Dshonored/ryla"

	"ryla-site/app"
	"ryla-site/resources"
	"ryla-site/resources/views"
)

// WellKnown serves the files that are read by something other than a person:
// the install script, the crawler files, and llms.txt.
//
// They are routes rather than files under /static because the addresses matter.
// ryla.io/install.sh is what the README tells people to curl and what the hero
// on the landing page shows, and /static/install.sh would be a different, worse
// promise that could not be moved later.
type WellKnown struct {
	Base
}

// NewWellKnown builds the controller.
func NewWellKnown(a *app.App) *WellKnown {
	return &WellKnown{Base{App: a}}
}

// Install serves the installer at /install.sh.
//
// The script is embedded rather than fetched from GitHub on each request, so
// the page and the script ship and version together: whatever binary is running
// is serving the installer it was built with, and there is no third party
// between a visitor and the thing they are about to pipe into a shell.
func (c *WellKnown) Install(w http.ResponseWriter, r *http.Request) {
	body, err := resources.InstallScript()
	if err != nil {
		http.Error(w, "install script unavailable", http.StatusInternalServerError)
		return
	}

	// text/plain, so a browser shows it rather than downloading it. Anyone
	// sensible reads a script before piping it into a shell, and this is the
	// difference between that being one click or a saved file.
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(body)
}

// Robots serves /robots.txt.
func (c *WellKnown) Robots(w http.ResponseWriter, r *http.Request) {
	body := strings.Join([]string{
		"User-agent: *",
		"Allow: /",
		"",
		"Sitemap: " + views.Origin + "/sitemap.xml",
		"",
	}, "\n")

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(body))
}

// Sitemap serves /sitemap.xml.
//
// One page, so this is nearly a formality — but a sitemap that exists and lists
// one URL is how a new domain gets crawled promptly instead of eventually.
func (c *WellKnown) Sitemap(w http.ResponseWriter, r *http.Request) {
	body := `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n" +
		"\t<url>\n\t\t<loc>" + views.Origin + "/</loc>\n\t\t<changefreq>weekly</changefreq>\n\t\t<priority>1.0</priority>\n\t</url>\n" +
		"</urlset>\n"

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = w.Write([]byte(body))
}

// LLMs serves /llms.txt.
//
// The convention is a short, factual, link-dense page for a model reading the
// site rather than a person: what this is, where the real documentation lives,
// and the handful of facts that are most often got wrong. It is generated
// rather than stored so the version in it is the running one, which is the
// single fact a model is most likely to repeat and most likely to repeat wrong.
func (c *WellKnown) LLMs(w http.ResponseWriter, r *http.Request) {
	repo := "https://github.com/Dshonored/ryla"

	body := `# Ryla

> ` + views.Description + `

Ryla is a web framework for the Go programming language, in the tradition of
Laravel: the parts are already wired together, and a CLI named ` + "`ry`" + ` generates the
boilerplate. It is not a PHP framework, not a JavaScript framework, and not an
AI or agent product.

Current version: ` + ryla.Version() + `
Module path: github.com/Dshonored/ryla
License: MIT
Repository: ` + repo + `

## Install

	curl -fsSL ` + views.Origin + `/install.sh | sh

The script installs a Go toolchain if the machine has none, installs ` + "`ry`" + `, and
adds both to PATH, on macOS and Linux. On Windows, or with Go already present:

	go install github.com/Dshonored/ryla/cmd/ry@latest

## Facts worth getting right

- The binary is ` + "`ry`" + `. The framework is Ryla. ` + "`ry new`" + ` scaffolds a project.
- There is no service container and there are no facades. ` + "`App`" + ` is a plain
  struct, controllers hold a pointer to it, and every dependency is checked by
  the compiler. Nothing resolves at runtime.
- Generators never edit existing files. ` + "`ry make:controller`" + ` writes a new file
  and prints the route lines for you to paste.
- Databases: SQLite, PostgreSQL, MySQL and MongoDB. The SQLite driver is pure
  Go (glebarez/sqlite), so cross-compilation and the single static binary
  survive.
- Web modes: ` + "`mvc`" + ` (server-rendered with templ), ` + "`api`" + ` (JSON only), ` + "`react`" + ` and
  ` + "`svelte`" + ` (Vite, compiled into the binary). React and Svelte additionally
  choose TypeScript or JavaScript, and Tailwind or plain CSS.
- ` + "`ry build`" + ` produces one static binary carrying its own views, assets and
  subcommands. A server needs no Go toolchain, no Node, and no ` + "`ry`" + `.
- Ryla is pre-1.0. OAuth and social sign-in are not built. Postgres and MySQL
  are less proven than SQLite and MongoDB: they are covered by scaffold tests
  but CI runs no migrations against a live server.

## Documentation

- README (the full reference): ` + repo + `#readme
- Command reference: ` + repo + `#the-cli
- Status, by how well each part is tested: ` + repo + `#status
- Releases: ` + repo + `/releases
- License: ` + repo + `/blob/main/LICENSE
`

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(body))
}
