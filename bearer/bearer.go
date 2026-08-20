// Package bearer authenticates API and mobile clients with personal access
// tokens sent in an Authorization: Bearer header.
//
// A token is a long random string shown to its owner exactly once, when it is
// created. Only its SHA-256 digest is stored, so a stolen tokens table hands
// over digests and no working credentials — the same reason passwords are
// hashed. Showing the plaintext once is not a UX preference: it is what makes
// the digest the only copy the server holds, and there is no "reveal token"
// endpoint here because there is nothing left to reveal.
//
// Authenticate puts the user id in the session under the key session login
// uses, so a handler cannot tell a token-authenticated request from a
// cookie-authenticated one. That is the point: auth.ID, auth.UintID and an
// application's own CurrentUser middleware keep working unchanged, and one set
// of handlers serves both the browser and the API.
//
// This package and csrf.Protect's Skip depend on each other. That exemption is
// safe only because a bearer token is not an ambient credential: a browser
// never attaches it to a cross-site request, so there is nothing for an
// attacker's page to trigger. Authenticate a skipped route with a cookie
// instead and the exemption becomes a hole, which is why Authenticate refuses
// to fall back to the session when a token is present but bad.
package bearer

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Prefix marks a Ryla personal access token.
//
// It exists so that secret scanners — GitHub's, and anything grepping a leaked
// dump — can recognise a token on sight and have it revoked before it is used.
// It is not secret and the server reads nothing from it beyond a cheap early
// rejection.
const Prefix = "ryla_pat_"

// secretBytes is the entropy behind a token. 256 bits is not a number anyone
// searches, which is what allows the stored digest to be a fast hash.
const secretBytes = 32

// Errors returned when a token cannot be used.
var (
	// ErrInvalidToken covers both a malformed token and one that names no row.
	// The two are deliberately indistinguishable: telling a caller that a value
	// is "well formed but unknown" confirms the shape of a valid token to
	// somebody who does not have one.
	ErrInvalidToken = errors.New("bearer: token is invalid")
	// ErrExpired is separate because it is not a secret. Whoever presents an
	// expired token already holds it, so naming the reason costs nothing and
	// saves them guessing why a token they own stopped working.
	ErrExpired = errors.New("bearer: token has expired")
	// ErrNotFound means no such token belongs to that user.
	ErrNotFound = errors.New("bearer: no such token")
)

// Token is one personal access token row.
//
// The plaintext is not a field, because it is never stored. Everything here can
// safely be shown to the token's owner.
type Token struct {
	ID uint `gorm:"column:id;primaryKey"`

	// UserID is the id as a string, matching session.Record.UserID and the
	// value auth.Login writes. Keeping the same representation is what lets a
	// handler read one id whichever credential produced it.
	UserID string `gorm:"column:user_id;index;size:64"`

	// Name is the label the owner gave it, e.g. "CI deploy key". It is what
	// makes a revoke list usable a year later.
	Name string `gorm:"column:name;size:120"`

	// Digest is the SHA-256 of the plaintext, hex encoded. Unique, because two
	// rows sharing a digest would mean crypto/rand repeated itself.
	Digest string `gorm:"column:digest;uniqueIndex;size:64"`

	// Scopes is the comma-separated scope list. Empty means unrestricted.
	Scopes string `gorm:"column:scopes;size:255"`

	// LastUsedAt is nil until the token is first used.
	LastUsedAt *time.Time `gorm:"column:last_used_at"`

	// ExpiresAt is nil for a token that never expires.
	ExpiresAt *time.Time `gorm:"column:expires_at;index"`

	CreatedAt time.Time `gorm:"column:created_at"`
}

// TableName pins the table name.
func (Token) TableName() string { return "personal_access_tokens" }

// Expired reports whether the token has passed its expiry.
func (t *Token) Expired() bool {
	return t.ExpiresAt != nil && time.Now().After(*t.ExpiresAt)
}

// ScopeList returns the scopes carried by the token.
func (t *Token) ScopeList() []string {
	if t.Scopes == "" {
		return nil
	}
	return strings.Split(t.Scopes, ",")
}

// Can reports whether the token carries scope.
//
// A token created with no scopes is unrestricted. Scopes are opt-in, and the
// opposite rule — no scopes means no access — would break every token already
// issued the day an application started calling Can.
func (t *Token) Can(scope string) bool {
	if t.Scopes == "" {
		return true
	}
	for _, s := range t.ScopeList() {
		if s == scope || s == "*" {
			return true
		}
	}
	return false
}

// Options tunes a token at creation.
type Options struct {
	// ExpiresIn bounds the token's life. Zero means it never expires, which is
	// what a long-lived CI credential wants and what a mobile client should
	// not have.
	ExpiresIn time.Duration
	// Scopes restricts what the token may do. Empty means unrestricted.
	Scopes []string
}

// Created is the result of issuing a token.
type Created struct {
	// Token is the stored row.
	Token *Token
	// Plaintext is the only copy of the credential that will ever exist. Show
	// it to the user now; once this value goes out of scope nothing in the
	// system — not the database, not a support engineer — can produce it again.
	Plaintext string
}

// generate returns a new token plaintext.
func generate() (string, error) {
	b := make([]byte, secretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("bearer: read random bytes: %w", err)
	}
	return Prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// digest hashes a token for storage and lookup.
//
// SHA-256 rather than argon2, and that is not a weakening. A password is
// short and human-chosen, so its hash has to be slow to make guessing
// expensive; a token is 256 bits from crypto/rand, and no amount of hardware
// gets through that. What the fast hash buys is a deterministic value the
// database can index, so verifying a token is one indexed lookup instead of a
// scan running argon2 against every row — the difference between an API that
// answers and one that falls over.
func digest(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// joinScopes packs scopes into the column.
//
// A scope containing a comma is refused rather than escaped: the separator is
// the whole storage format, and a scope that smuggles one in would silently
// become two scopes, granting something nobody asked for.
func joinScopes(scopes []string) (string, error) {
	cleaned := make([]string, 0, len(scopes))
	for _, s := range scopes {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if strings.Contains(s, ",") {
			return "", fmt.Errorf("bearer: scope %q contains a comma", s)
		}
		cleaned = append(cleaned, s)
	}
	return strings.Join(cleaned, ","), nil
}
