// Package controllers holds the application's HTTP handlers.
package controllers

import (
	"encoding/json"
	"net/http"

	"github.com/a-h/templ"

	"github.com/Dshonored/ryla"
	"github.com/Dshonored/ryla/view"

	"ryla-site/app"
	"ryla-site/resources/views"
)

// Base is embedded by every controller. It carries the application and the
// handful of response helpers that would otherwise be repeated in each handler.
type Base struct {
	App *app.App
}

// Site is what every page renders against: the running framework version and
// the repository, named in one place so a page cannot disagree with the one
// beside it.
//
// The version comes from ryla.Version(), which reads the build info rather than
// a constant — so the badge in the header and the line in the footer cannot
// advertise a release that does not exist, and there is no number to remember
// to bump. A build from a local checkout reports "dev", which is the truth.
func (c Base) Site() views.Site {
	return views.Site{Version: ryla.Version(), Repo: "Dshonored/ryla"}
}

// Render writes a templ component as the response body, turning a render
// failure into a logged 500 rather than a half-written page.
func (c Base) Render(w http.ResponseWriter, r *http.Request, component templ.Component) {
	c.RenderStatus(w, r, http.StatusOK, component)
}

// RenderStatus is Render with an explicit status code.
func (c Base) RenderStatus(w http.ResponseWriter, r *http.Request, status int, component templ.Component) {
	if err := view.RenderStatus(w, r, status, component); err != nil {
		c.App.Log.Error("render view", "error", err, "path", r.URL.Path)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Redirect sends a See Other redirect, the correct status after a form POST.
func (c Base) Redirect(w http.ResponseWriter, r *http.Request, url string) {
	http.Redirect(w, r, url, http.StatusSeeOther)
}

// JSON writes v as a JSON response.
func (c Base) JSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		c.App.Log.Error("encode json", "error", err, "path", r.URL.Path)
	}
}
