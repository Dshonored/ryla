// Package view renders templ components to an HTTP response.
package view

import (
	"bytes"
	"net/http"

	"github.com/a-h/templ"
)

// Render writes a component to the response with status 200.
func Render(w http.ResponseWriter, r *http.Request, c templ.Component) error {
	return RenderStatus(w, r, http.StatusOK, c)
}

// RenderStatus writes a component to the response with an explicit status.
//
// The component is rendered into a buffer first. Templ writes incrementally, so
// rendering straight to the ResponseWriter would commit a 200 and flush half a
// page before a failing component could be noticed — leaving no way to send a
// proper error. Buffering costs one allocation per response and makes the
// failure mode a clean 500.
func RenderStatus(w http.ResponseWriter, r *http.Request, status int, c templ.Component) error {
	var buf bytes.Buffer
	if err := c.Render(r.Context(), &buf); err != nil {
		return err
	}

	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	w.WriteHeader(status)
	_, err := buf.WriteTo(w)
	return err
}
