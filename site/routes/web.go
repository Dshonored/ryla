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
)

// Register attaches every route to the application's router.
func Register(a *app.App) {
	home := controllers.NewHome(a)

	a.Router.Static("/static", resources.Static())

	a.Router.Get("/", home.Index).Name("home")
	a.Router.Get("/up", health).Name("health")

	// Addresses that something other than a person reads. They are routes
	// rather than files under /static because the address is the promise:
	// ryla.io/install.sh is what the README tells people to curl.
	wk := controllers.NewWellKnown(a)
	a.Router.Get("/install.sh", wk.Install).Name("install")
	a.Router.Get("/robots.txt", wk.Robots).Name("robots")
	a.Router.Get("/sitemap.xml", wk.Sitemap).Name("sitemap")
	a.Router.Get("/llms.txt", wk.LLMs).Name("llms")

}

// health is the liveness endpoint. Deployment tooling wants a route that
// touches nothing, so this deliberately does not check the database.
func health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
