package bearer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// DefaultTouchInterval is how stale a token's last-used timestamp may get.
const DefaultTouchInterval = time.Minute

// Store keeps personal access tokens in the database.
//
// The database rather than a cache: revoking a token has to be immediate and
// permanent, and a credential that outlives its revocation because an entry was
// still warm somewhere is not revoked at all.
type Store struct {
	DB *gorm.DB

	// TouchInterval bounds how often Verify writes last_used_at. Zero uses
	// DefaultTouchInterval.
	TouchInterval time.Duration
}

// New builds a token store.
func New(db *gorm.DB) *Store {
	return &Store{DB: db, TouchInterval: DefaultTouchInterval}
}

// Create issues a token for a user and returns its one and only plaintext.
//
// name is the owner-facing label; opts may bound the token's life and scopes.
func (s *Store) Create(ctx context.Context, userID, name string, opts Options) (Created, error) {
	if userID == "" {
		return Created{}, errors.New("bearer: a token needs a user id")
	}

	scopes, err := joinScopes(opts.Scopes)
	if err != nil {
		return Created{}, err
	}

	plaintext, err := generate()
	if err != nil {
		return Created{}, err
	}

	tok := &Token{
		UserID:    userID,
		Name:      name,
		Digest:    digest(plaintext),
		Scopes:    scopes,
		CreatedAt: time.Now(),
	}
	if opts.ExpiresIn > 0 {
		at := tok.CreatedAt.Add(opts.ExpiresIn)
		tok.ExpiresAt = &at
	}

	if err := s.DB.WithContext(ctx).Create(tok).Error; err != nil {
		return Created{}, fmt.Errorf("bearer: create token: %w", err)
	}

	// The plaintext travels back to the caller and nowhere else. It is
	// deliberately not logged, not returned by List, and not recoverable from
	// the row that was just written.
	return Created{Token: tok, Plaintext: plaintext}, nil
}

// List returns a user's tokens, newest first.
//
// It cannot return the plaintexts, which is the honest thing for a settings
// page to show: a name, when it was made, when it was last used, and a revoke
// button.
func (s *Store) List(ctx context.Context, userID string) ([]Token, error) {
	var tokens []Token
	err := s.DB.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("created_at DESC, id DESC").
		Find(&tokens).Error
	if err != nil {
		return nil, fmt.Errorf("bearer: list tokens: %w", err)
	}
	return tokens, nil
}

// Revoke deletes one of a user's tokens.
//
// The delete is scoped to the owner as well as the id. Without that, anyone
// could revoke anybody's token by counting upwards through the primary key —
// the same insecure-direct-object-reference hole as any other id taken from a
// request.
func (s *Store) Revoke(ctx context.Context, userID string, id uint) error {
	res := s.DB.WithContext(ctx).Delete(&Token{}, "id = ? AND user_id = ?", id, userID)
	if res.Error != nil {
		return fmt.Errorf("bearer: revoke token: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeAll deletes every token belonging to a user and returns how many went.
//
// This is the API half of "log out everywhere". A password change that only
// destroys sessions leaves every stolen token working, so call this alongside
// session.DBStore.DestroyForUser rather than instead of it.
func (s *Store) RevokeAll(ctx context.Context, userID string) (int64, error) {
	res := s.DB.WithContext(ctx).Delete(&Token{}, "user_id = ?", userID)
	if res.Error != nil {
		return 0, fmt.Errorf("bearer: revoke tokens: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// Verify resolves a plaintext token to its row.
//
// It returns ErrInvalidToken for anything unknown or malformed and ErrExpired
// for a real token past its expiry.
func (s *Store) Verify(ctx context.Context, plaintext string) (*Token, error) {
	// Rejected before touching the database, so a scanner spraying rubbish at
	// the API costs a string comparison rather than a query each time.
	if !strings.HasPrefix(plaintext, Prefix) {
		return nil, ErrInvalidToken
	}

	var tok Token
	err := s.DB.WithContext(ctx).First(&tok, "digest = ?", digest(plaintext)).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, fmt.Errorf("bearer: look up token: %w", err)
	}

	if tok.Expired() {
		// Left in place rather than deleted as it is found. An expired token
		// the owner can still see in their list explains itself; one that
		// vanished on first use turns "my integration broke" into a mystery.
		// Prune clears them on a schedule instead.
		return nil, ErrExpired
	}

	s.touch(ctx, &tok)
	return &tok, nil
}

// touch records that a token was used, at most once per TouchInterval.
//
// The timestamp is what lets an owner spot a token they had forgotten about, or
// one being used from somewhere they are not. Writing it on every request would
// put a database write in front of every API call, so a value that can be a
// minute stale is the trade — the same one Session.Touch makes.
func (s *Store) touch(ctx context.Context, tok *Token) {
	interval := s.TouchInterval
	if interval <= 0 {
		interval = DefaultTouchInterval
	}

	now := time.Now()
	if tok.LastUsedAt != nil && now.Sub(*tok.LastUsedAt) < interval {
		return
	}

	// A failed write here must not fail an otherwise valid request: the token
	// is good, and bookkeeping is not worth a 500. The in-memory copy is left
	// alone so the next request tries again.
	if err := s.DB.WithContext(ctx).Model(&Token{}).
		Where("id = ?", tok.ID).
		UpdateColumn("last_used_at", now).Error; err != nil {
		return
	}
	tok.LastUsedAt = &now
}

// Prune deletes expired tokens and returns how many went.
//
// Verify leaves expired rows alone on purpose, so this is the only thing that
// clears them. A token with no expiry is never pruned, which is the whole
// meaning of not setting one.
func (s *Store) Prune(ctx context.Context) (int64, error) {
	res := s.DB.WithContext(ctx).
		Where("expires_at IS NOT NULL AND expires_at < ?", time.Now()).
		Delete(&Token{})
	if res.Error != nil {
		return 0, fmt.Errorf("bearer: prune tokens: %w", res.Error)
	}
	return res.RowsAffected, nil
}
