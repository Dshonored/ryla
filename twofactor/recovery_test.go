package twofactor

import (
	"strings"
	"testing"
)

func codes(t *testing.T, n int) []string {
	t.Helper()

	out, err := GenerateRecoveryCodes(n)
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}
	return out
}

// TestARecoveryCodeWorksExactlyOnce is the property the whole recovery
// mechanism rests on. A recovery code is a written-down password with no second
// factor behind it, so one that still worked after being used would be a
// permanent bypass of the thing it exists to rescue — and the failure would be
// invisible, because the first use looks perfectly correct.
func TestARecoveryCodeWorksExactlyOnce(t *testing.T) {
	plain := codes(t, 4)
	stored := HashRecoveryCodes(plain)

	remaining, ok := ConsumeRecoveryCode(stored, plain[1])
	if !ok {
		t.Fatal("a freshly issued recovery code was rejected")
	}
	if CountRecoveryCodes(remaining) != 3 {
		t.Fatalf("%d codes left after spending one of four", CountRecoveryCodes(remaining))
	}

	// The same code, against the value that was stored in response.
	if _, ok := ConsumeRecoveryCode(remaining, plain[1]); ok {
		t.Fatal("a recovery code was accepted a second time")
	}

	// And it stays refused however many times it is tried.
	for range 3 {
		if _, ok := ConsumeRecoveryCode(remaining, plain[1]); ok {
			t.Fatal("a spent recovery code came back to life")
		}
	}
}

// TestSpendingOneCodeLeavesTheOthersUsable guards against the obvious way to
// implement single use badly: clearing the whole set. That would lock a user
// out of the account the moment they used their first code.
func TestSpendingOneCodeLeavesTheOthersUsable(t *testing.T) {
	plain := codes(t, 4)
	stored := HashRecoveryCodes(plain)

	stored, ok := ConsumeRecoveryCode(stored, plain[0])
	if !ok {
		t.Fatal("the first code was rejected")
	}

	for _, code := range plain[1:] {
		next, ok := ConsumeRecoveryCode(stored, code)
		if !ok {
			t.Fatalf("code %q was rejected after a different one had been spent", code)
		}
		stored = next
	}

	if CountRecoveryCodes(stored) != 0 {
		t.Errorf("%d codes left after spending all four", CountRecoveryCodes(stored))
	}
}

// TestAnUnknownCodeChangesNothing matters because the stored value is written
// back after every attempt. If a failed attempt returned a shortened set, a
// wrong guess would quietly destroy a user's codes.
func TestAnUnknownCodeChangesNothing(t *testing.T) {
	stored := HashRecoveryCodes(codes(t, 3))

	remaining, ok := ConsumeRecoveryCode(stored, "ZZZZZ-ZZZZZ")
	if ok {
		t.Fatal("a code that was never issued was accepted")
	}
	if remaining != stored {
		t.Error("a failed attempt altered the stored codes")
	}
	if CountRecoveryCodes(remaining) != 3 {
		t.Errorf("%d codes left after a failed attempt on three", CountRecoveryCodes(remaining))
	}
}

// TestCodesAreMatchedHowAPersonWouldTypeThem covers the realistic ways a code
// comes back from a printout: lower case, dash left out, spaces from a copy.
// Refusing those turns a working recovery code into a lost account.
func TestCodesAreMatchedHowAPersonWouldTypeThem(t *testing.T) {
	plain := codes(t, 1)
	stored := HashRecoveryCodes(plain)

	variants := map[string]string{
		"lower case":  strings.ToLower(plain[0]),
		"no dash":     strings.ReplaceAll(plain[0], "-", ""),
		"spaced out":  strings.ReplaceAll(plain[0], "-", " "),
		"with blanks": " " + plain[0] + " ",
	}

	for name, variant := range variants {
		if _, ok := ConsumeRecoveryCode(stored, variant); !ok {
			t.Errorf("%s: %q was rejected", name, variant)
		}
	}
}

// TestTheStoredValueIsNotTheCode is what "stored hashed" means. Anyone reading
// the users table must not come away with a set of working credentials.
func TestTheStoredValueIsNotTheCode(t *testing.T) {
	plain := codes(t, 4)
	stored := HashRecoveryCodes(plain)

	for _, code := range plain {
		if strings.Contains(stored, code) {
			t.Errorf("the code %q appears verbatim in the stored value", code)
		}
		if strings.Contains(stored, normalizeRecoveryCode(code)) {
			t.Errorf("the code %q appears in the stored value once normalised", code)
		}
	}
}

func TestGeneratedCodesAreDistinctAndReadable(t *testing.T) {
	plain := codes(t, 64)

	seen := map[string]bool{}
	for _, code := range plain {
		if seen[code] {
			t.Fatalf("the same recovery code was issued twice: %q", code)
		}
		seen[code] = true

		if len(code) != recoveryCodeLength+1 {
			t.Errorf("code %q is %d characters, want %d with its separator", code, len(code), recoveryCodeLength+1)
		}
		// No I, L, O, U, so nothing can be misread as a one or a zero.
		if strings.ContainsAny(code, "ILOU") {
			t.Errorf("code %q contains an easily misread character", code)
		}
	}
}

func TestEmptyInputIsNotACode(t *testing.T) {
	stored := HashRecoveryCodes(codes(t, 2))

	for name, code := range map[string]string{"empty": "", "punctuation only": "---"} {
		if _, ok := ConsumeRecoveryCode(stored, code); ok {
			t.Errorf("%s input was accepted as a recovery code", name)
		}
	}
	if _, ok := ConsumeRecoveryCode("", "ABCDE-FGHJK"); ok {
		t.Error("a code was accepted against an account with none stored")
	}
}
