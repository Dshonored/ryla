// Package routes declares the application's HTTP routes.
//
// Routes are written by hand and never rewritten by a generator, so this file
// stays a reliable table of contents for the application. `ry make:controller`
// prints the lines to add rather than editing this file behind your back.
package routes

import (
	"net/http"

	"ryla-site/app"
	"ryla-site/app/controllers"
	"ryla-site/resources"
	"ryla-site/resources/views"
)

// Register attaches every route to the application's router.
func Register(a *app.App) {
	home := controllers.NewHome(a)

	a.Router.Static("/static", resources.Static())

	a.Router.Get("/", home.Index).Name("home")
	a.Router.Get("/up", health).Name("health")

	// The written pages. Each one is served twice: at its own address, where it
	// answers with markdown if the request asked for markdown, and at the same
	// address with `.md` appended, which always does. The content is one Doc
	// either way, so the two cannot disagree.
	pages := controllers.NewPages(a)
	a.Router.Get("/about", pages.Show(views.About)).Name("about")
	a.Router.Get("/contact", pages.Show(views.Contact)).Name("contact")
	a.Router.Get("/privacy", pages.Show(views.Privacy)).Name("privacy")

	a.Router.Get("/index.md", home.Markdown).Name("home.md")
	a.Router.Get("/about.md", pages.Markdown(views.About)).Name("about.md")
	a.Router.Get("/contact.md", pages.Markdown(views.Contact)).Name("contact.md")
	a.Router.Get("/privacy.md", pages.Markdown(views.Privacy)).Name("privacy.md")

	// Addresses that something other than a person reads. They are routes
	// rather than files under /static because the address is the promise:
	// ryla.io/install.sh is what the README tells people to curl.
	wk := controllers.NewWellKnown(a)
	a.Router.Get("/install.sh", wk.Install).Name("install")
	a.Router.Get("/robots.txt", wk.Robots).Name("robots")
	a.Router.Get("/sitemap.xml", wk.Sitemap).Name("sitemap")
	a.Router.Get("/llms.txt", wk.LLMs).Name("llms")
	a.Router.Get("/agents.md", wk.Agents).Name("agents")

	// What answers everything else. Without these, an unknown address gets
	// chi's bare "404 page not found" and a wrong method gets its bare "405" —
	// correct statuses with nothing in them to act on. These answer with the
	// same status and a body that says where to go instead.
	a.Router.NotFound(pages.NotFound)
	a.Router.MethodNotAllowed(pages.MethodNotAllowed)
}

// health is the liveness endpoint. Deployment tooling wants a route that
// touches nothing, so this deliberately does not check the database.
func health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
