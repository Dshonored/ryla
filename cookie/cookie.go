// Package cookie reads and writes tamper-evident cookies.
//
// A signed cookie is still readable by the client — this is authentication, not
// encryption. It guarantees that a value came from this application and has not
// been altered, which is what CSRF tokens, flash messages and session
// identifiers need. Never put a secret in one.
package cookie

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"
)

// Errors returned when a cookie cannot be trusted. They are deliberately
// indistinguishable to a caller deciding what to do: in every case the answer
// is to treat the value as absent.
var (
	ErrNotFound = errors.New("cookie: not found")
	ErrInvalid  = errors.New("cookie: value is malformed or has been tampered with")
)

// Jar signs and verifies cookies with an application key.
type Jar struct {
	key []byte
	// Secure marks cookies as HTTPS-only. It is set from the app's URL scheme,
	// because a Secure cookie is simply never sent over plain HTTP and local
	// development would silently stop working.
	Secure bool
	// Path defaults to "/".
	Path string
	// SameSite defaults to Lax, which blocks the cross-site POSTs that CSRF
	// depends on while leaving ordinary inbound links working.
	SameSite http.SameSite
}

// New builds a Jar. The key should be the application key; anything derived
// from a password or left empty makes the signature worthless.
func New(key string, secure bool) *Jar {
	return &Jar{
		key:      []byte(key),
		Secure:   secure,
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
	}
}

// Options tune a single cookie.
type Options struct {
	// MaxAge in seconds. Zero means a session cookie, negative deletes it.
	MaxAge int
	// HTTPOnly hides the cookie from JavaScript. Default true; CSRF tokens are
	// the notable exception, since a fetch() has to read one.
	HTTPOnly *bool
}

// Set writes a signed cookie.
func (j *Jar) Set(w http.ResponseWriter, name, value string, opts Options) {
	httpOnly := true
	if opts.HTTPOnly != nil {
		httpOnly = *opts.HTTPOnly
	}

	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    j.sign(name, value),
		Path:     j.path(),
		MaxAge:   opts.MaxAge,
		Secure:   j.Secure,
		HttpOnly: httpOnly,
		SameSite: j.SameSite,
	})
}

// Get reads and verifies a signed cookie.
func (j *Jar) Get(r *http.Request, name string) (string, error) {
	c, err := r.Cookie(name)
	if err != nil {
		return "", ErrNotFound
	}
	return j.verify(name, c.Value)
}

// Delete removes a cookie. The attributes must match those it was written with,
// or the browser keeps the original.
func (j *Jar) Delete(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     j.path(),
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		Secure:   j.Secure,
		HttpOnly: true,
		SameSite: j.SameSite,
	})
}

func (j *Jar) path() string {
	if j.Path == "" {
		return "/"
	}
	return j.Path
}

// sign returns value with its MAC appended.
//
// The cookie's name is part of the signed material, so a valid value cannot be
// lifted out of one cookie and replayed in another.
func (j *Jar) sign(name, value string) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(value))
	return payload + "." + j.mac(name, payload)
}

func (j *Jar) verify(name, signed string) (string, error) {
	payload, sig, found := strings.Cut(signed, ".")
	if !found {
		return "", ErrInvalid
	}

	// Constant time, so the comparison cannot be used as an oracle to guess a
	// valid signature byte by byte.
	if subtle.ConstantTimeCompare([]byte(sig), []byte(j.mac(name, payload))) != 1 {
		return "", ErrInvalid
	}

	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", ErrInvalid
	}
	return string(raw), nil
}

func (j *Jar) mac(name, payload string) string {
	h := hmac.New(sha256.New, j.key)
	h.Write([]byte(name))
	h.Write([]byte("|"))
	h.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
