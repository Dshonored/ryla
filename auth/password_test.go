package auth

import (
	"strings"
	"testing"
)

// fastParams keeps the tests quick. Real hashing is deliberately slow, and
// running the production cost dozens of times would make this suite crawl.
var fastParams = Params{Memory: 64, Time: 1, Threads: 1, SaltLength: 8, KeyLength: 16}

func TestHashAndVerify(t *testing.T) {
	hash, err := HashWith("correct horse battery staple", fastParams)
	if err != nil {
		t.Fatalf("HashWith: %v", err)
	}

	ok, err := Verify(hash, "correct horse battery staple")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Error("the correct password did not verify")
	}

	ok, err = Verify(hash, "wrong password")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if ok {
		t.Error("a wrong password verified")
	}
}

// TestSaltMakesHashesUnique is why two accounts with the same password do not
// look identical in the table, and why a precomputed rainbow table is useless.
func TestSaltMakesHashesUnique(t *testing.T) {
	a, err := HashWith("same", fastParams)
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashWith("same", fastParams)
	if err != nil {
		t.Fatal(err)
	}

	if a == b {
		t.Error("hashing the same password twice produced identical output")
	}

	// Both must still verify.
	for _, h := range []string{a, b} {
		if ok, _ := Verify(h, "same"); !ok {
			t.Error("a salted hash failed to verify")
		}
	}
}

func TestHashFormatCarriesItsParameters(t *testing.T) {
	hash, err := HashWith("x", fastParams)
	if err != nil {
		t.Fatal(err)
	}

	// The PHC format lets the cost be raised later without invalidating hashes
	// already stored.
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Errorf("unexpected hash format: %s", hash)
	}
	if n := strings.Count(hash, "$"); n != 5 {
		t.Errorf("hash has %d separators, want 5: %s", n, hash)
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	cases := map[string]string{
		"empty":          "",
		"not a hash":     "plaintext",
		"wrong算法":        "$bcrypt$v=19$m=64,t=1,p=1$c2FsdA$aGFzaA",
		"missing fields": "$argon2id$v=19$m=64,t=1,p=1",
		"bad base64":     "$argon2id$v=19$m=64,t=1,p=1$!!!$???",
		"bad params":     "$argon2id$v=19$m=x,t=1,p=1$c2FsdA$aGFzaA",
	}

	for name, hash := range cases {
		t.Run(name, func(t *testing.T) {
			ok, err := Verify(hash, "anything")
			if ok {
				t.Error("a malformed hash reported a match")
			}
			if err == nil {
				t.Error("a malformed hash produced no error")
			}
		})
	}
}

func TestVerifyRejectsUnknownVersion(t *testing.T) {
	_, err := Verify("$argon2id$v=16$m=64,t=1,p=1$c2FsdHNhbHQ$aGFzaGhhc2g", "x")
	if err == nil {
		t.Error("an incompatible argon2 version was accepted")
	}
}

func TestNeedsRehash(t *testing.T) {
	weak, err := HashWith("x", fastParams)
	if err != nil {
		t.Fatal(err)
	}
	if !NeedsRehash(weak) {
		t.Error("a hash below the current cost was not flagged for rehashing")
	}

	current, err := Hash("x")
	if err != nil {
		t.Fatal(err)
	}
	if NeedsRehash(current) {
		t.Error("a hash at the current cost was flagged for rehashing")
	}

	if !NeedsRehash("garbage") {
		t.Error("an unreadable hash should be replaced")
	}
}

func TestEmptyPasswordStillHashes(t *testing.T) {
	// Rejecting an empty password is validation's job, not the hasher's; it
	// must not silently produce something that verifies against anything.
	hash, err := HashWith("", fastParams)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := Verify(hash, ""); !ok {
		t.Error("an empty password did not verify against its own hash")
	}
	if ok, _ := Verify(hash, "x"); ok {
		t.Error("a non-empty password verified against the empty hash")
	}
}

func TestZeroThreadsDoesNotPanic(t *testing.T) {
	// argon2 panics on zero threads; a config typo should not take the process
	// down.
	if _, err := HashWith("x", Params{Memory: 64, Time: 1, Threads: 0, SaltLength: 8, KeyLength: 16}); err != nil {
		t.Fatalf("HashWith with zero threads: %v", err)
	}
}

func TestDummyVerifyDoesNotPanic(t *testing.T) {
	// Called on the "no such user" path, where there is no hash to compare
	// against, so that a missing account does not answer faster than a wrong
	// password.
	DummyVerify("anything")
}
