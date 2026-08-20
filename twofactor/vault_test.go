package twofactor

import (
	"errors"
	"strings"
	"testing"
)

func vault(t *testing.T, key string) *Vault {
	t.Helper()

	v, err := NewVault(key)
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}
	return v
}

func TestSealedSecretsRoundTrip(t *testing.T) {
	v := vault(t, "an application key")
	secret := newSecret(t)

	sealed, err := v.Seal(secret.Base32)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	opened, err := v.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if opened != secret.Base32 {
		t.Errorf("opened %q, want %q", opened, secret.Base32)
	}
}

// TestTheSealedValueDoesNotContainTheSecret is the point of the whole type. If
// the secret survived into the stored string in any recoverable form, a leaked
// database would still hand the attacker a working second factor for every
// enrolled user — which is precisely the attacker two-factor authentication was
// added to stop.
func TestTheSealedValueDoesNotContainTheSecret(t *testing.T) {
	v := vault(t, "an application key")
	secret := newSecret(t)

	sealed, err := v.Seal(secret.Base32)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if strings.Contains(sealed, secret.Base32) {
		t.Fatal("the sealed value contains the secret verbatim")
	}
	// Base32 is case-insensitive in this alphabet, so check both.
	if strings.Contains(strings.ToUpper(sealed), strings.ToUpper(secret.Base32)) {
		t.Fatal("the sealed value contains the secret ignoring case")
	}
}

// TestSealingTheSameSecretTwiceDiffers protects the nonce. A deterministic
// ciphertext would let anyone with the table see which users share a secret,
// and would tell an attacker who could trigger a re-enrolment whether the
// secret had actually changed.
func TestSealingTheSameSecretTwiceDiffers(t *testing.T) {
	v := vault(t, "an application key")
	secret := newSecret(t)

	first, err := v.Seal(secret.Base32)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	second, err := v.Seal(secret.Base32)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if first == second {
		t.Fatal("sealing the same secret twice produced the same value")
	}
}

// TestAnotherKeyCannotOpenIt is what makes a stolen database inert: the key
// lives in the environment, not in the table.
func TestAnotherKeyCannotOpenIt(t *testing.T) {
	sealed, err := vault(t, "the real application key").Seal("JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if _, err := vault(t, "some other key").Open(sealed); !errors.Is(err, ErrSealed) {
		t.Fatalf("Open with the wrong key returned %v, want ErrSealed", err)
	}
}

// TestTamperedValuesAreRefused is why this is GCM and not a bare stream
// cipher: a secret an attacker can edit in place is a secret they can replace
// with one they know the codes for.
func TestTamperedValuesAreRefused(t *testing.T) {
	v := vault(t, "an application key")

	sealed, err := v.Seal("JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// Somewhere in the middle, well past the version prefix and away from the
	// final character, whose spare bits base64 does not carry.
	flipped := []byte(sealed)
	mid := len(flipped) / 2
	if flipped[mid] == 'A' {
		flipped[mid] = 'B'
	} else {
		flipped[mid] = 'A'
	}

	cases := map[string]string{
		"one character changed": string(flipped),
		"truncated":             sealed[:len(sealed)-4],
		"no version prefix":     strings.TrimPrefix(sealed, sealPrefix),
		"not base64":            sealPrefix + "not base64 !!",
		"nothing but a prefix":  sealPrefix,
	}

	for name, bad := range cases {
		if _, err := v.Open(bad); !errors.Is(err, ErrSealed) {
			t.Errorf("%s: Open returned %v, want ErrSealed", name, err)
		}
	}
}

// TestAnEmptyApplicationKeyIsRefused stops the failure mode where encryption
// silently becomes decoration because every deployment shares one key.
func TestAnEmptyApplicationKeyIsRefused(t *testing.T) {
	for _, key := range []string{"", "   "} {
		if _, err := NewVault(key); !errors.Is(err, ErrNoKey) {
			t.Errorf("NewVault(%q) returned %v, want ErrNoKey", key, err)
		}
	}
}

// TestNoSecretSealsToNothing keeps the empty column honest: a user who has
// never enrolled has no secret, and that must not become a sealed empty string
// that looks like one.
func TestNoSecretSealsToNothing(t *testing.T) {
	v := vault(t, "an application key")

	sealed, err := v.Seal("")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if sealed != "" {
		t.Errorf("sealing nothing produced %q", sealed)
	}

	opened, err := v.Open("")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if opened != "" {
		t.Errorf("opening nothing produced %q", opened)
	}
}
