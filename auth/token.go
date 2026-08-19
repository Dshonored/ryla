package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Errors returned when a token cannot be trusted.
var (
	ErrTokenInvalid = errors.New("auth: token is malformed or has been tampered with")
	ErrTokenExpired = errors.New("auth: token has expired")
)

// Purposes for signed tokens. A token is bound to its purpose, so a link that
// verifies an email address cannot be replayed to reset a password.
const (
	PurposeVerifyEmail   = "verify-email"
	PurposeResetPassword = "reset-password"
)

// Signer issues and checks short-lived signed tokens.
//
// Tokens carry their subject and expiry in the clear and are protected by an
// HMAC, so nothing has to be stored server-side to issue one. That is the point:
// an email verification link works without a tokens table, and a link that has
// expired says so rather than looking like a database miss.
//
// Because the subject is readable, never put anything private in it.
type Signer struct {
	key []byte
}

// NewSigner builds a signer from the application key.
func NewSigner(key string) *Signer { return &Signer{key: []byte(key)} }

// Sign issues a token for a subject, valid for ttl.
func (s *Signer) Sign(purpose, subject string, ttl time.Duration) string {
	expires := time.Now().Add(ttl).Unix()
	payload := base64.RawURLEncoding.EncodeToString([]byte(subject)) + "." + strconv.FormatInt(expires, 10)
	return payload + "." + s.mac(purpose, payload)
}

// Verify checks a token and returns its subject.
func (s *Signer) Verify(purpose, token string) (string, error) {
	subjectPart, rest, ok := strings.Cut(token, ".")
	if !ok {
		return "", ErrTokenInvalid
	}
	expiresPart, sig, ok := strings.Cut(rest, ".")
	if !ok {
		return "", ErrTokenInvalid
	}

	payload := subjectPart + "." + expiresPart

	// Constant time, so a wrong signature cannot be refined byte by byte from
	// timing differences.
	if subtle.ConstantTimeCompare([]byte(sig), []byte(s.mac(purpose, payload))) != 1 {
		return "", ErrTokenInvalid
	}

	expires, err := strconv.ParseInt(expiresPart, 10, 64)
	if err != nil {
		return "", ErrTokenInvalid
	}
	// Checked after the signature, so an attacker learns nothing from the
	// difference between "expired" and "forged".
	if time.Now().Unix() > expires {
		return "", ErrTokenExpired
	}

	subject, err := base64.RawURLEncoding.DecodeString(subjectPart)
	if err != nil {
		return "", ErrTokenInvalid
	}
	return string(subject), nil
}

// mac binds the signature to the purpose, so a token issued for one flow cannot
// be replayed in another.
func (s *Signer) mac(purpose, payload string) string {
	h := hmac.New(sha256.New, s.key)
	h.Write([]byte(purpose))
	h.Write([]byte("|"))
	h.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
