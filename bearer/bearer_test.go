package bearer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var dbCounter atomic.Int64

func uniqueDSN(t *testing.T) string {
	return fmt.Sprintf("file:%s-%d?mode=memory&cache=shared", t.Name(), dbCounter.Add(1))
}

func store(t *testing.T) *Store {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(uniqueDSN(t)), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := db.AutoMigrate(&Token{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return New(db)
}

func create(t *testing.T, s *Store, userID string, opts Options) Created {
	t.Helper()

	made, err := s.Create(context.Background(), userID, "test token", opts)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	return made
}

// rows returns every stored token as raw column values, which is the only way
// to check what actually landed in the table rather than what the model chose
// to expose.
func rows(t *testing.T, s *Store) []map[string]any {
	t.Helper()

	var out []map[string]any
	if err := s.DB.Table("personal_access_tokens").Find(&out).Error; err != nil {
		t.Fatalf("read raw rows: %v", err)
	}
	return out
}

// TestThePlaintextIsNeverStored is the property the whole package exists for. A
// tokens table that leaks must leak digests, exactly as a users table leaks
// password hashes; if the plaintext were anywhere in the row, stealing the
// table would be stealing working credentials.
func TestThePlaintextIsNeverStored(t *testing.T) {
	s := store(t)
	made := create(t, s, "42", Options{})

	secret := strings.TrimPrefix(made.Plaintext, Prefix)
	if secret == "" {
		t.Fatal("the issued token is nothing but its prefix")
	}

	for _, row := range rows(t, s) {
		for column, value := range row {
			got := fmt.Sprint(value)
			if strings.Contains(got, made.Plaintext) || strings.Contains(got, secret) {
				t.Errorf("column %q holds the plaintext token: %q", column, got)
			}
		}
	}

	if made.Token.Digest == made.Plaintext {
		t.Error("the digest is the plaintext")
	}
}

// TestAStolenRowIsNotAWorkingCredential is the other half of the same property:
// not only is the plaintext absent, nothing else in the row can be presented in
// its place. An attacker holding the table must have nothing to send.
func TestAStolenRowIsNotAWorkingCredential(t *testing.T) {
	s := store(t)
	made := create(t, s, "42", Options{})

	stolen := []string{
		made.Token.Digest,
		Prefix + made.Token.Digest,
		fmt.Sprint(made.Token.ID),
	}

	for _, candidate := range stolen {
		if _, err := s.Verify(context.Background(), candidate); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("a stolen row value %q was accepted, err = %v", candidate, err)
		}
	}
}

func TestVerifyAcceptsAFreshToken(t *testing.T) {
	s := store(t)
	made := create(t, s, "42", Options{})

	tok, err := s.Verify(context.Background(), made.Plaintext)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if tok.UserID != "42" {
		t.Errorf("UserID = %q, want 42", tok.UserID)
	}
	if tok.ID != made.Token.ID {
		t.Errorf("resolved token %d, want %d", tok.ID, made.Token.ID)
	}
}

// TestWrongTokensAreRejected covers the shapes an attacker actually sends:
// nothing, rubbish, something with the right prefix, and a real token with a
// character changed.
func TestWrongTokensAreRejected(t *testing.T) {
	s := store(t)
	made := create(t, s, "42", Options{})

	tampered := []byte(made.Plaintext)
	// Flipping the final character keeps the length and the prefix, so only the
	// digest lookup can catch it.
	if tampered[len(tampered)-1] == 'A' {
		tampered[len(tampered)-1] = 'B'
	} else {
		tampered[len(tampered)-1] = 'A'
	}

	wrong := map[string]string{
		"empty":              "",
		"no prefix":          "not-a-token",
		"prefix only":        Prefix,
		"prefix and rubbish": Prefix + "definitely-not-issued",
		"tampered":           string(tampered),
		"uppercased":         strings.ToUpper(made.Plaintext),
	}

	for name, candidate := range wrong {
		t.Run(name, func(t *testing.T) {
			if _, err := s.Verify(context.Background(), candidate); !errors.Is(err, ErrInvalidToken) {
				t.Errorf("err = %v, want ErrInvalidToken", err)
			}
		})
	}
}

// TestExpiredTokensAreRejected protects the only guarantee an expiry gives: a
// token handed to a contractor for a fortnight stops working after it, whether
// or not anybody remembers to revoke it.
func TestExpiredTokensAreRejected(t *testing.T) {
	s := store(t)
	made := create(t, s, "42", Options{ExpiresIn: time.Hour})

	// Wound back rather than slept through, so the test costs nothing.
	past := time.Now().Add(-time.Minute)
	if err := s.DB.Model(&Token{}).Where("id = ?", made.Token.ID).
		UpdateColumn("expires_at", past).Error; err != nil {
		t.Fatalf("expire the token: %v", err)
	}

	if _, err := s.Verify(context.Background(), made.Plaintext); !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v, want ErrExpired", err)
	}

	// Still listed, because an owner who cannot see the expired token cannot
	// work out why their integration stopped.
	tokens, err := s.List(context.Background(), "42")
	if err != nil || len(tokens) != 1 {
		t.Fatalf("List = %d tokens, %v", len(tokens), err)
	}
}

func TestATokenWithNoExpiryKeepsWorking(t *testing.T) {
	s := store(t)
	made := create(t, s, "42", Options{})

	if made.Token.ExpiresAt != nil {
		t.Fatalf("ExpiresAt = %v, want nil", made.Token.ExpiresAt)
	}
	if _, err := s.Verify(context.Background(), made.Plaintext); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

// TestRevokeEndsTheTokenImmediately is what a user pressing "revoke" after
// pasting a token into the wrong window is relying on.
func TestRevokeEndsTheTokenImmediately(t *testing.T) {
	s := store(t)
	made := create(t, s, "42", Options{})

	if err := s.Revoke(context.Background(), "42", made.Token.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := s.Verify(context.Background(), made.Plaintext); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("a revoked token still verifies, err = %v", err)
	}
}

// TestRevokeIsScopedToTheOwner closes the direct-object-reference hole: token
// ids are sequential, so a revoke that trusted the id alone would let anyone
// disable everybody else's integrations by counting.
func TestRevokeIsScopedToTheOwner(t *testing.T) {
	s := store(t)
	victim := create(t, s, "42", Options{})

	if err := s.Revoke(context.Background(), "99", victim.Token.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := s.Verify(context.Background(), victim.Plaintext); err != nil {
		t.Errorf("the owner's token was revoked by somebody else: %v", err)
	}
}

// TestRevokeAllEndsEveryToken is the API half of a password change: sessions
// alone are not enough, because a stolen token outlives them.
func TestRevokeAllEndsEveryToken(t *testing.T) {
	s := store(t)
	first := create(t, s, "42", Options{})
	second := create(t, s, "42", Options{})
	other := create(t, s, "99", Options{})

	n, err := s.RevokeAll(context.Background(), "42")
	if err != nil {
		t.Fatalf("RevokeAll: %v", err)
	}
	if n != 2 {
		t.Errorf("revoked %d tokens, want 2", n)
	}

	for _, made := range []Created{first, second} {
		if _, err := s.Verify(context.Background(), made.Plaintext); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("a revoked token still verifies, err = %v", err)
		}
	}
	if _, err := s.Verify(context.Background(), other.Plaintext); err != nil {
		t.Errorf("another user's token was caught up in the revoke: %v", err)
	}
}

// TestLastUsedAtIsRecordedButNotOnEveryRequest protects both halves of the
// trade: an owner can see when a token was last used, and the API does not pay
// for a database write per request to tell them.
func TestLastUsedAtIsRecordedButNotOnEveryRequest(t *testing.T) {
	s := store(t)
	s.TouchInterval = time.Hour
	made := create(t, s, "42", Options{})

	if made.Token.LastUsedAt != nil {
		t.Fatalf("a token that has never been used has LastUsedAt = %v", made.Token.LastUsedAt)
	}

	first, err := s.Verify(context.Background(), made.Plaintext)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if first.LastUsedAt == nil {
		t.Fatal("the first use was not recorded")
	}

	second, err := s.Verify(context.Background(), made.Plaintext)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !second.LastUsedAt.Equal(*first.LastUsedAt) {
		t.Errorf("a second use inside the touch interval rewrote the timestamp: %v then %v",
			first.LastUsedAt, second.LastUsedAt)
	}
}

// TestScopesRestrictWhatATokenMayDo keeps the narrowing meaningful: a token
// issued for one thing must not answer yes to another.
func TestScopesRestrictWhatATokenMayDo(t *testing.T) {
	s := store(t)
	made := create(t, s, "42", Options{Scopes: []string{"posts:read", "posts:write"}})

	tok, err := s.Verify(context.Background(), made.Plaintext)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if !tok.Can("posts:read") || !tok.Can("posts:write") {
		t.Errorf("a granted scope was refused: %v", tok.ScopeList())
	}
	if tok.Can("billing:write") {
		t.Error("a scope that was never granted was allowed")
	}
}

// TestAnUnscopedTokenIsUnrestricted pins the rule the other way round, because
// the opposite one would silently disable every token already issued the day an
// application started checking scopes.
func TestAnUnscopedTokenIsUnrestricted(t *testing.T) {
	s := store(t)
	made := create(t, s, "42", Options{})

	if !made.Token.Can("anything") {
		t.Error("a token created with no scopes refused a scope check")
	}
}

// TestAScopeContainingTheSeparatorIsRefused stops a scope smuggling a comma in
// and becoming two scopes, which would grant something nobody asked for.
func TestAScopeContainingTheSeparatorIsRefused(t *testing.T) {
	s := store(t)

	_, err := s.Create(context.Background(), "42", "sneaky", Options{
		Scopes: []string{"posts:read,billing:write"},
	})
	if err == nil {
		t.Fatal("a scope containing a comma was accepted")
	}
}

// TestPruneRemovesOnlyExpiredTokens guards the sweep that Verify deliberately
// leaves undone: a token with no expiry must survive it.
func TestPruneRemovesOnlyExpiredTokens(t *testing.T) {
	s := store(t)
	permanent := create(t, s, "42", Options{})
	expiring := create(t, s, "42", Options{ExpiresIn: time.Hour})

	past := time.Now().Add(-time.Minute)
	if err := s.DB.Model(&Token{}).Where("id = ?", expiring.Token.ID).
		UpdateColumn("expires_at", past).Error; err != nil {
		t.Fatalf("expire the token: %v", err)
	}

	n, err := s.Prune(context.Background())
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 1 {
		t.Errorf("pruned %d tokens, want 1", n)
	}
	if _, err := s.Verify(context.Background(), permanent.Plaintext); err != nil {
		t.Errorf("a token with no expiry was pruned: %v", err)
	}
}

// TestEveryTokenIsDifferent would catch the worst possible bug here — a token
// generator that repeats — before it hands two users the same credential.
func TestEveryTokenIsDifferent(t *testing.T) {
	s := store(t)

	seen := map[string]bool{}
	for range 50 {
		made := create(t, s, "42", Options{})

		if !strings.HasPrefix(made.Plaintext, Prefix) {
			t.Fatalf("token %q has no scanner-visible prefix", made.Plaintext)
		}
		if seen[made.Plaintext] {
			t.Fatalf("token %q was issued twice", made.Plaintext)
		}
		seen[made.Plaintext] = true
	}
}

func TestListReturnsOnlyTheOwnersTokens(t *testing.T) {
	s := store(t)
	create(t, s, "42", Options{})
	create(t, s, "42", Options{})
	create(t, s, "99", Options{})

	tokens, err := s.List(context.Background(), "42")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tokens) != 2 {
		t.Fatalf("List returned %d tokens, want 2", len(tokens))
	}
	for _, tok := range tokens {
		if tok.UserID != "42" {
			t.Errorf("List leaked user %q's token", tok.UserID)
		}
	}
}
