// Package flash carries a short message across a redirect.
//
// After a successful POST the correct response is a redirect, which loses
// everything the handler knew. A flash survives exactly one request: it is
// written into a signed cookie, read on the next page, and cleared.
//
// The cookie is signed rather than encrypted, so a flash is readable by the
// client. Never put anything private in one.
package flash

import (
	"encoding/json"
	"net/http"

	"github.com/Dshonored/ryla/cookie"
)

// CookieName holds pending messages.
const CookieName = "ryla_flash"

// Kind classifies a message so the template can style it.
type Kind string

const (
	Success Kind = "success"
	Info    Kind = "info"
	Warning Kind = "warning"
	Error   Kind = "error"
)

// Message is one flash.
type Message struct {
	Kind Kind   `json:"k"`
	Text string `json:"t"`
}

// Store reads and writes flashes.
type Store struct {
	Jar *cookie.Jar
}

// New builds a Store.
func New(jar *cookie.Jar) *Store { return &Store{Jar: jar} }

// Add queues a message for the next request. Call it before writing the
// redirect: a cookie is a header, and headers cannot be set once the response
// body has started.
func (s *Store) Add(w http.ResponseWriter, r *http.Request, kind Kind, text string) {
	messages := s.peek(r)
	messages = append(messages, Message{Kind: kind, Text: text})

	raw, err := json.Marshal(messages)
	if err != nil {
		return
	}
	s.Jar.Set(w, CookieName, string(raw), cookie.Options{})
}

// Success queues a success message.
func (s *Store) Success(w http.ResponseWriter, r *http.Request, text string) {
	s.Add(w, r, Success, text)
}

// Error queues an error message.
func (s *Store) Error(w http.ResponseWriter, r *http.Request, text string) {
	s.Add(w, r, Error, text)
}

// Info queues an informational message.
func (s *Store) Info(w http.ResponseWriter, r *http.Request, text string) {
	s.Add(w, r, Info, text)
}

// Warning queues a warning message.
func (s *Store) Warning(w http.ResponseWriter, r *http.Request, text string) {
	s.Add(w, r, Warning, text)
}

// Pull returns the pending messages and clears them, so a refresh does not show
// the same banner again.
func (s *Store) Pull(w http.ResponseWriter, r *http.Request) []Message {
	messages := s.peek(r)
	if len(messages) > 0 {
		s.Jar.Delete(w, CookieName)
	}
	return messages
}

// peek reads without clearing.
func (s *Store) peek(r *http.Request) []Message {
	raw, err := s.Jar.Get(r, CookieName)
	if err != nil || raw == "" {
		// A tampered or unreadable cookie is treated as no messages. There is
		// nothing useful to tell the user, and a flash is not worth an error
		// page.
		return nil
	}

	var messages []Message
	if err := json.Unmarshal([]byte(raw), &messages); err != nil {
		return nil
	}
	return messages
}
