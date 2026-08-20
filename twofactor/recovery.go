package twofactor

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"
)

// DefaultRecoveryCodes is how many codes are issued at enrolment.
//
// Enough that losing a printout of one does not matter, few enough that
// somebody might actually keep them somewhere sensible.
const DefaultRecoveryCodes = 8

const (
	// recoveryAlphabet is Crockford's base32: no I, L, O or U, so nothing in a
	// code can be misread as a one, a zero, or a rude word. Thirty-two symbols
	// is also a power of two, which is what lets the codes be generated
	// uniformly by masking random bytes rather than by rejection sampling.
	recoveryAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

	// recoveryCodeLength gives fifty bits per code. That is far beyond guessing
	// — the point of the length is that the codes need no slow hash behind
	// them, see HashRecoveryCodes.
	recoveryCodeLength = 10

	// recoveryGroup is where the separating dash goes, purely so a human can
	// read a code back off a piece of paper without losing their place.
	recoveryGroup = 5
)

// GenerateRecoveryCodes returns n fresh codes in plaintext.
//
// This is the only time they exist in a readable form. Show them once, tell the
// user to keep them somewhere that is not the phone holding the authenticator,
// and store nothing but HashRecoveryCodes of them.
func GenerateRecoveryCodes(n int) ([]string, error) {
	if n < 1 {
		n = DefaultRecoveryCodes
	}

	codes := make([]string, 0, n)
	for range n {
		raw := make([]byte, recoveryCodeLength)
		if _, err := rand.Read(raw); err != nil {
			return nil, fmt.Errorf("twofactor: generating recovery codes: %w", err)
		}

		var b strings.Builder
		for i, v := range raw {
			if i > 0 && i%recoveryGroup == 0 {
				b.WriteByte('-')
			}
			b.WriteByte(recoveryAlphabet[v&31])
		}
		codes = append(codes, b.String())
	}
	return codes, nil
}

// HashRecoveryCodes turns a set of codes into the single value to store.
//
// SHA-256, not the argon2id used for passwords. A recovery code is not a
// password: it is fifty bits this package generated, so there is no dictionary
// to run against it and no cheap candidate list to try — a slow hash defends
// against an attack that cannot happen here. It would cost something, though,
// because a submitted code has to be checked against every stored hash to find
// out which one it is, and that would be one argon2 run per code on a form
// anyone can post to.
//
// The codes are stored newline-separated, which needs no encoder and cannot
// fail: nothing generated above contains a newline.
func HashRecoveryCodes(codes []string) string {
	hashes := make([]string, 0, len(codes))
	for _, code := range codes {
		normalized := normalizeRecoveryCode(code)
		if normalized == "" {
			continue
		}
		hashes = append(hashes, hashRecoveryCode(normalized))
	}
	return strings.Join(hashes, "\n")
}

// ConsumeRecoveryCode spends a code, returning the value to store in place of
// the old one.
//
// Single use is the whole point of a recovery code — it is a written-down
// password with no second factor behind it, so one that stayed valid after
// being used would be a permanent bypass of the thing it exists to rescue. This
// function removes the match; persisting the value it returns is what actually
// spends it, so write it back in the same transaction that signs the user in.
//
// ok is false when nothing matched, and stored comes back unchanged.
func ConsumeRecoveryCode(stored, code string) (remaining string, ok bool) {
	normalized := normalizeRecoveryCode(code)
	if normalized == "" || stored == "" {
		return stored, false
	}
	want := hashRecoveryCode(normalized)

	kept := make([]string, 0, 8)
	for _, hash := range splitRecoveryCodes(stored) {
		// Constant time, and the loop does not stop early: a comparison that
		// returns as soon as it matches tells anyone timing it roughly where in
		// the list their guess landed.
		if subtle.ConstantTimeCompare([]byte(hash), []byte(want)) == 1 && !ok {
			ok = true
			continue
		}
		kept = append(kept, hash)
	}

	if !ok {
		return stored, false
	}
	return strings.Join(kept, "\n"), true
}

// CountRecoveryCodes reports how many unused codes are left, which is what a
// "you have two codes remaining" warning needs.
func CountRecoveryCodes(stored string) int {
	return len(splitRecoveryCodes(stored))
}

func hashRecoveryCode(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func splitRecoveryCodes(stored string) []string {
	out := make([]string, 0, 8)
	for _, line := range strings.Split(stored, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// normalizeRecoveryCode reduces a code to the characters that carry meaning, so
// that a user retyping one is not defeated by the dash, by lower case, or by
// the spaces a PDF reader inserts when the code is copied out of a printout.
func normalizeRecoveryCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if strings.ContainsRune(recoveryAlphabet, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
