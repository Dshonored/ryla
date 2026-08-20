package twofactor

import (
	"time"

	"github.com/Dshonored/ryla/ratelimit"
)

// Defaults for the throttle in front of verification.
//
// Five attempts a quarter of an hour, per account. With the default skew three
// codes are live at any moment, so a blind guess lands three times in a
// million; at this rate an attacker gets a few hundred tries a day and needs
// centuries. Loosen it and that number moves fast — the limit is doing all of
// the work here, because the secret behind the code never changes.
//
// It is per account rather than per address on purpose. A limit counted by IP
// is spent from many machines at once, and the account is what is under attack.
const (
	DefaultAttempts = 5
	DefaultWindow   = 15 * time.Minute
)

// Verifier checks codes and recovery codes, throttled per subject.
//
// It exists so that the rate limit cannot be left out by accident. A six-digit
// code is a million guesses, which is an afternoon's work unthrottled, and the
// natural place to forget the limiter is exactly the handler where it matters.
//
// The limiter is in-memory and per-process, like the rest of the ratelimit
// package: behind several instances each keeps its own count, so the effective
// limit multiplies by the number of them.
type Verifier struct {
	opts     Options
	attempts *ratelimit.Limiter
}

// NewVerifier builds a verifier with the default throttle.
func NewVerifier(opts Options) *Verifier {
	return NewVerifierWithLimit(opts, DefaultAttempts, DefaultWindow)
}

// NewVerifierWithLimit is NewVerifier with an explicit budget.
func NewVerifierWithLimit(opts Options, attempts int, window time.Duration) *Verifier {
	return &Verifier{
		opts:     opts.withDefaults(),
		attempts: ratelimit.New(attempts, window),
	}
}

// Verify checks a code from the authenticator app.
//
// subject identifies the account being verified — the user id, not the address
// they typed, so that changing the spelling of an email cannot buy a fresh
// budget.
//
// retryAfter is non-zero only when the subject has been throttled, which is the
// case worth telling the user apart from a wrong code: "that code is not right"
// and "you have run out of attempts, try again in nine minutes" are different
// problems and lead to different next steps.
func (v *Verifier) Verify(subject, secret, code string) (ok bool, retryAfter time.Duration) {
	return v.VerifyAt(subject, secret, code, time.Now())
}

// VerifyAt is Verify at an explicit time, so that drift and expiry can be
// tested without waiting for a code to go stale.
func (v *Verifier) VerifyAt(subject, secret, code string, t time.Time) (ok bool, retryAfter time.Duration) {
	allowed, wait := v.attempts.Allow(subject)
	if !allowed {
		return false, wait
	}

	if !VerifyAt(secret, code, t, v.opts) {
		return false, 0
	}

	// A correct code clears the record, so somebody who fat-fingered two codes
	// before getting one right is not left one attempt from being locked out.
	v.attempts.Reset(subject)
	return true, 0
}

// VerifyRecovery spends a recovery code, on the same budget as Verify.
//
// Sharing the budget matters: a separate allowance for recovery codes would
// hand an attacker a second, unthrottled door into the same account, and the
// recovery form is the more attractive one because a code that works there
// needs no phone afterwards.
//
// remaining is the value to store in place of stored, and is only meaningfully
// different from it when ok is true. Persist it before completing the sign-in;
// until it is written, the code has not actually been spent.
func (v *Verifier) VerifyRecovery(subject, stored, code string) (remaining string, ok bool, retryAfter time.Duration) {
	allowed, wait := v.attempts.Allow(subject)
	if !allowed {
		return stored, false, wait
	}

	remaining, ok = ConsumeRecoveryCode(stored, code)
	if !ok {
		return stored, false, 0
	}

	v.attempts.Reset(subject)
	return remaining, true, 0
}

// Reset clears a subject's attempt history. Call it after the account's
// password has been changed, or after an administrator has unlocked it.
func (v *Verifier) Reset(subject string) { v.attempts.Reset(subject) }

// Options returns the settings this verifier was built with, so a caller
// generating a secret can be sure it matches what will later check the codes.
func (v *Verifier) Options() Options { return v.opts }
