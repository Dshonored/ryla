// Package auth provides password hashing and session-backed authentication.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Params control the cost of hashing.
//
// The defaults follow OWASP's argon2id recommendation. They are deliberately
// slow: the whole point is that an attacker who steals the table cannot try
// billions of candidate passwords per second. Raising Memory or Time makes
// stolen hashes harder to crack and every login slower — measure before
// changing either.
type Params struct {
	// Memory in KiB.
	Memory uint32
	// Time is the number of passes.
	Time uint32
	// Threads used per hash.
	Threads uint8
	// SaltLength and KeyLength in bytes.
	SaltLength uint32
	KeyLength  uint32
}

// DefaultParams is 19 MiB, two passes, one thread.
var DefaultParams = Params{
	Memory:     19 * 1024,
	Time:       2,
	Threads:    1,
	SaltLength: 16,
	KeyLength:  32,
}

// Errors returned when a stored hash cannot be used.
var (
	ErrInvalidHash  = errors.New("auth: password hash is malformed")
	ErrIncompatible = errors.New("auth: password hash was produced by an incompatible argon2 version")
)

// Hash returns an argon2id hash of password, encoded with its parameters and
// salt so it can be verified without any external state.
func Hash(password string) (string, error) {
	return HashWith(password, DefaultParams)
}

// HashWith is Hash with explicit parameters.
func HashWith(password string, p Params) (string, error) {
	if p.Threads == 0 {
		// A zero here would panic inside argon2. Fall back to something
		// sensible rather than taking the process down over a config typo.
		p.Threads = uint8(min(runtime.NumCPU(), 255))
	}

	salt := make([]byte, p.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLength)

	// The PHC string format, so the parameters travel with the hash and can be
	// raised later without invalidating existing passwords.
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// Verify reports whether password matches encodedHash.
//
// It returns false rather than an error for a wrong password, and an error only
// when the stored hash itself cannot be parsed — a corrupt row, not a failed
// login.
func Verify(encodedHash, password string) (bool, error) {
	p, salt, key, err := decode(encodedHash)
	if err != nil {
		return false, err
	}

	candidate := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, uint32(len(key)))

	// Constant time: a byte-by-byte comparison leaks how much of the hash
	// matched through timing.
	return subtle.ConstantTimeCompare(key, candidate) == 1, nil
}

// NeedsRehash reports whether a stored hash was made with weaker parameters
// than the current defaults.
//
// Call it after a successful login, while the plaintext password is in hand —
// that is the only moment a hash can be upgraded without asking the user to do
// anything.
func NeedsRehash(encodedHash string) bool {
	p, _, _, err := decode(encodedHash)
	if err != nil {
		// Unreadable is worse than outdated; replacing it is right.
		return true
	}
	return p.Memory < DefaultParams.Memory ||
		p.Time < DefaultParams.Time ||
		p.Threads < DefaultParams.Threads
}

// DummyVerify performs a hash comparison against a throwaway value.
//
// Call it when a login fails because no such user exists. Without it, a missing
// account answers noticeably faster than a wrong password, and that difference
// tells an attacker which email addresses are registered.
func DummyVerify(password string) {
	_, _ = Verify(dummyHash, password)
}

// dummyHash is a real argon2id hash of a value nobody will guess, generated
// once at startup so DummyVerify costs the same as a genuine check.
var dummyHash = mustDummy()

func mustDummy() string {
	h, err := Hash("ryla-dummy-password-for-constant-time-login")
	if err != nil {
		return ""
	}
	return h
}

func decode(encodedHash string) (Params, []byte, []byte, error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return Params{}, nil, nil, ErrInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return Params{}, nil, nil, ErrInvalidHash
	}
	if version != argon2.Version {
		return Params{}, nil, nil, ErrIncompatible
	}

	var p Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return Params{}, nil, nil, ErrInvalidHash
	}

	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil {
		return Params{}, nil, nil, ErrInvalidHash
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil {
		return Params{}, nil, nil, ErrInvalidHash
	}

	p.SaltLength = uint32(len(salt))
	p.KeyLength = uint32(len(key))
	return p, salt, key, nil
}
