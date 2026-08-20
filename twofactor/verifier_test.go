package twofactor

import (
	"testing"
	"time"

	"github.com/Dshonored/ryla/session"
)

// TestVerificationIsThrottled is the reason Verifier exists. Six digits is a
// million guesses, and with the skew window three of them are live at once — so
// without a limit an attacker walks the space in an afternoon and the second
// factor is worth nothing. This test fails the moment the limiter is dropped
// from the path.
func TestVerificationIsThrottled(t *testing.T) {
	secret := newSecret(t)
	v := NewVerifierWithLimit(Options{}, 3, time.Minute)
	now := time.Now()

	for attempt := 1; attempt <= 3; attempt++ {
		if _, retryAfter := v.VerifyAt("user-1", secret.Base32, "000000", now); retryAfter != 0 {
			t.Fatalf("attempt %d was throttled while inside the budget", attempt)
		}
	}

	ok, retryAfter := v.VerifyAt("user-1", secret.Base32, "000000", now)
	if ok {
		t.Fatal("a wrong code was accepted")
	}
	if retryAfter <= 0 {
		t.Fatal("a fourth guess against a budget of three was not throttled")
	}
}

// TestThrottlingRefusesTheRightCodeToo protects the property that makes the
// limit worth anything. A throttle that still let a correct code through would
// be no obstacle at all: guessing is how the attacker finds the correct code.
func TestThrottlingRefusesTheRightCodeToo(t *testing.T) {
	secret := newSecret(t)
	v := NewVerifierWithLimit(Options{}, 2, time.Minute)
	now := time.Now()

	v.VerifyAt("user-1", secret.Base32, "000000", now)
	v.VerifyAt("user-1", secret.Base32, "000001", now)

	code := codeAt(t, secret.Base32, now, Options{})
	if ok, _ := v.VerifyAt("user-1", secret.Base32, code, now); ok {
		t.Fatal("the correct code was accepted after the budget had run out")
	}
}

// TestTheBudgetIsPerAccount protects a user under attack from being locked out
// by someone else's guessing, and stops one exhausted budget from sheltering
// every other account.
func TestTheBudgetIsPerAccount(t *testing.T) {
	secret := newSecret(t)
	v := NewVerifierWithLimit(Options{}, 2, time.Minute)
	now := time.Now()

	v.VerifyAt("user-1", secret.Base32, "000000", now)
	v.VerifyAt("user-1", secret.Base32, "000001", now)

	code := codeAt(t, secret.Base32, now, Options{})
	if ok, retryAfter := v.VerifyAt("user-2", secret.Base32, code, now); !ok {
		t.Fatalf("a second account was throttled by the first: retry after %v", retryAfter)
	}
}

// TestASuccessClearsTheRecord keeps the limit invisible to the person typing
// their own codes: two fumbled attempts followed by a correct one should not
// leave them one mistake away from being locked out.
func TestASuccessClearsTheRecord(t *testing.T) {
	secret := newSecret(t)
	v := NewVerifierWithLimit(Options{}, 3, time.Minute)
	now := time.Now()

	v.VerifyAt("user-1", secret.Base32, "000000", now)
	v.VerifyAt("user-1", secret.Base32, "000001", now)

	if ok, _ := v.VerifyAt("user-1", secret.Base32, codeAt(t, secret.Base32, now, Options{}), now); !ok {
		t.Fatal("the correct code was rejected")
	}

	// The full budget again.
	for attempt := 1; attempt <= 3; attempt++ {
		if _, retryAfter := v.VerifyAt("user-1", secret.Base32, "000000", now); retryAfter != 0 {
			t.Fatalf("attempt %d after a success was throttled", attempt)
		}
	}
}

// TestRecoveryCodesShareTheBudget closes the door a separate allowance would
// open. The recovery form is the more attractive one to attack, because a code
// that works there needs no phone afterwards.
func TestRecoveryCodesShareTheBudget(t *testing.T) {
	secret := newSecret(t)
	v := NewVerifierWithLimit(Options{}, 2, time.Minute)
	now := time.Now()
	stored := HashRecoveryCodes(codes(t, 4))

	v.VerifyAt("user-1", secret.Base32, "000000", now)
	v.VerifyAt("user-1", secret.Base32, "000001", now)

	if _, _, retryAfter := v.VerifyRecovery("user-1", stored, "ZZZZZ-ZZZZZ"); retryAfter <= 0 {
		t.Fatal("recovery codes were checked on a budget of their own")
	}
}

// TestVerifyRecoverySpendsTheCodeOnce is the single-use property as the
// controller sees it, throttle and all.
func TestVerifyRecoverySpendsTheCodeOnce(t *testing.T) {
	v := NewVerifier(Options{})
	plain := codes(t, 3)
	stored := HashRecoveryCodes(plain)

	remaining, ok, retryAfter := v.VerifyRecovery("user-1", stored, plain[0])
	if !ok || retryAfter != 0 {
		t.Fatalf("a fresh recovery code was refused: ok=%v retryAfter=%v", ok, retryAfter)
	}
	if CountRecoveryCodes(remaining) != 2 {
		t.Fatalf("%d codes left after spending one of three", CountRecoveryCodes(remaining))
	}

	if _, ok, _ := v.VerifyRecovery("user-1", remaining, plain[0]); ok {
		t.Fatal("a spent recovery code was accepted a second time")
	}
}

// TestAFailedRecoveryAttemptKeepsTheCodes matters because the controller writes
// back whatever it is handed: returning a shortened set on a wrong guess would
// let anyone delete a user's recovery codes from a public form.
func TestAFailedRecoveryAttemptKeepsTheCodes(t *testing.T) {
	v := NewVerifier(Options{})
	stored := HashRecoveryCodes(codes(t, 3))

	remaining, ok, _ := v.VerifyRecovery("user-1", stored, "ZZZZZ-ZZZZZ")
	if ok {
		t.Fatal("a code that was never issued was accepted")
	}
	if remaining != stored {
		t.Error("a failed attempt altered the stored codes")
	}
}

// TestASessionIsChallengedUntilItPasses protects the default. A session that
// arrived from anywhere without the mark — restored from a store, or one an
// attacker planted — must be challenged rather than waved through.
func TestASessionIsChallengedUntilItPasses(t *testing.T) {
	s := session.New(time.Hour)

	if Passed(s) {
		t.Fatal("a fresh session was treated as having cleared the challenge")
	}
	if Passed(nil) {
		t.Fatal("a missing session was treated as having cleared the challenge")
	}

	before := s.ID
	MarkPassed(s)

	if !Passed(s) {
		t.Fatal("the session was not marked as having cleared the challenge")
	}
	// The same reason login regenerates: the session gains privileges here, and
	// an id that survives a privilege change is one worth planting beforehand.
	if s.ID == before {
		t.Error("the session id was not regenerated when the challenge was passed")
	}
}

// TestPassingTheChallengeDiscardsTheEnrolmentInProgress keeps a half-scanned
// secret from lingering in the session where a later request could confirm it.
func TestPassingTheChallengeDiscardsTheEnrolmentInProgress(t *testing.T) {
	s := session.New(time.Hour)

	PutPendingSecret(s, "v1.sealed-value")
	if PendingSecret(s) != "v1.sealed-value" {
		t.Fatal("the pending secret was not stored")
	}

	MarkPassed(s)
	if PendingSecret(s) != "" {
		t.Error("the pending secret outlived the enrolment")
	}

	PutPendingSecret(s, "v1.sealed-value")
	ForgetPendingSecret(s)
	if PendingSecret(s) != "" {
		t.Error("abandoning an enrolment left the pending secret behind")
	}
}

// TestLoggingOutClearsTheChallenge: the mark lives in the session, so ending
// the session must end it. Otherwise the next sign-in on that browser would
// skip the second factor.
func TestLoggingOutClearsTheChallenge(t *testing.T) {
	s := session.New(time.Hour)
	MarkPassed(s)

	s.Invalidate()

	if Passed(s) {
		t.Fatal("the challenge mark survived the session being invalidated")
	}
}
