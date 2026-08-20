package twofactor

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// Errors returned when a sealed secret cannot be used.
var (
	// ErrNoKey is returned when there is no application key to seal with.
	// Falling back to a fixed key would mean every deployment shared one, and
	// the encryption would be decoration.
	ErrNoKey = errors.New("twofactor: the application key is empty, so secrets cannot be sealed")

	// ErrSealed covers every way a stored value can fail to open. They are
	// deliberately indistinguishable: a caller can do nothing useful with the
	// difference, and an attacker probing a decryption oracle can.
	ErrSealed = errors.New("twofactor: the stored secret is malformed, or was sealed with a different application key")
)

// sealPrefix versions the stored format.
//
// Without it, changing the scheme later produces garbage that looks like a
// wrong key rather than an obviously different format, and there is no way to
// migrate the old rows without guessing which is which.
const sealPrefix = "v1."

// Vault seals TOTP secrets for storage.
//
// A password is hashed because nothing ever needs it back. A TOTP secret is not
// like that: the server recomputes the codes from it on every sign-in, so it
// has to be recoverable. Stored in plaintext it is worth exactly as much to
// whoever steals the database as it is to the user — which would leave
// two-factor authentication defending against everybody except the attacker it
// was added for, the one who already has the password table.
//
// So it is encrypted with AES-256-GCM under a key derived from the application
// key, which lives in the environment rather than the database. A dump of the
// users table is then inert by itself.
//
// This is not protection from an attacker who has the environment too. Nothing
// that has to decrypt at runtime can be. It raises the cost of a leaked backup
// or a SQL injection from "every second factor in the system" to "nothing".
type Vault struct {
	aead cipher.AEAD
}

// NewVault derives a sealing key from the application key.
//
// The key is derived rather than used directly, so the value that signs cookies
// and the value that encrypts secrets are different bytes. Reusing one key for
// two primitives is the kind of shortcut that is fine until the day it is not.
func NewVault(appKey string) (*Vault, error) {
	if strings.TrimSpace(appKey) == "" {
		return nil, ErrNoKey
	}

	key, err := hkdf.Key(sha256.New, []byte(appKey), nil, "ryla twofactor secret v1", 32)
	if err != nil {
		return nil, fmt.Errorf("twofactor: deriving the sealing key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("twofactor: building the cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("twofactor: building the cipher: %w", err)
	}

	return &Vault{aead: aead}, nil
}

// Seal encrypts a secret for storage. The result is printable, so the column
// holding it can be an ordinary text column.
func (v *Vault) Seal(secret string) (string, error) {
	if secret == "" {
		return "", nil
	}

	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("twofactor: sealing a secret: %w", err)
	}

	// The nonce is prepended rather than stored separately: it is not secret,
	// only unique, and a column that can be moved without its nonce is a
	// column that will eventually be moved without its nonce.
	sealed := v.aead.Seal(nonce, nonce, []byte(secret), nil)
	return sealPrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

// Open decrypts a stored secret.
//
// An error here usually means the application key was rotated. That locks every
// enrolled user out of their second factor, which is why rotating it is a
// migration and not a config change — read the old rows with the old key and
// re-seal them with the new one before the old key goes.
func (v *Vault) Open(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}

	body, ok := strings.CutPrefix(sealed, sealPrefix)
	if !ok {
		return "", ErrSealed
	}

	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return "", ErrSealed
	}
	if len(raw) < v.aead.NonceSize() {
		return "", ErrSealed
	}

	nonce, ciphertext := raw[:v.aead.NonceSize()], raw[v.aead.NonceSize():]
	plain, err := v.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", ErrSealed
	}
	return string(plain), nil
}
