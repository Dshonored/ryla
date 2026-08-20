package twofactor

import "github.com/Dshonored/ryla/session"

// Session keys used by the challenge.
const (
	// PassedKey marks a session as having cleared the second factor. It is
	// absent, not false, until the challenge is passed — a session restored
	// from anywhere without it is challenged again, which is the safe default.
	PassedKey = "two_factor_passed"

	// PendingSecretKey holds a sealed secret partway through enrolment, from
	// the moment the QR code is shown until the first correct code confirms
	// the user's app is actually generating the right numbers.
	//
	// It waits here rather than in the users table so that an abandoned
	// enrolment leaves nothing behind, and so that a half-scanned secret can
	// never be mistaken for an active one.
	PendingSecretKey = "two_factor_pending"
)

// MarkPassed records that this session has cleared the challenge.
//
// The session id is regenerated, for the same reason it is at login: the
// session gains privileges at this moment, and an id that survives a privilege
// change is an id worth stealing beforehand.
func MarkPassed(s *session.Session) {
	if s == nil {
		return
	}
	s.Regenerate()
	s.PutBool(PassedKey, true)
	s.Forget(PendingSecretKey)
}

// Passed reports whether this session has cleared the challenge.
//
// A user who has not enabled two-factor authentication never passes one, so
// callers must check that flag on the account before reading this — see the
// generated middleware, which does both.
func Passed(s *session.Session) bool {
	return s != nil && s.GetBool(PassedKey)
}

// PutPendingSecret stashes a sealed secret for the enrolment about to be
// confirmed. It must be sealed: the cookie session store signs its contents but
// does not encrypt them, so a plaintext secret put here is a secret handed to
// the browser.
func PutPendingSecret(s *session.Session, sealed string) {
	if s != nil {
		s.Put(PendingSecretKey, sealed)
	}
}

// PendingSecret returns the sealed secret being enrolled, or "".
func PendingSecret(s *session.Session) string {
	if s == nil {
		return ""
	}
	return s.Get(PendingSecretKey)
}

// ForgetPendingSecret abandons an enrolment in progress.
func ForgetPendingSecret(s *session.Session) {
	if s != nil {
		s.Forget(PendingSecretKey)
	}
}
