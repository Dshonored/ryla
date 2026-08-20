// Package twofactor adds TOTP two-factor authentication.
//
// Three things an application needs are here: enrolment, which produces a
// shared secret and the otpauth:// URI an authenticator app scans; verification
// of the codes that secret generates; and recovery codes, because a second
// factor that lives on one phone turns a lost phone into a lost account.
//
// Two decisions are worth knowing before reading further.
//
// A TOTP secret cannot be hashed the way a password is — the server has to
// recompute the codes, so it needs the secret back. Vault seals it with the
// application key instead, so a stolen users table is inert on its own.
//
// And six digits is a million guesses, which is nothing. Verify is the
// unthrottled primitive; Verifier is the one to reach for, because it puts a
// rate limiter in front of every attempt and treats a recovery code as costing
// the same budget as a code from the phone.
package twofactor

import (
	"bytes"
	"encoding/base32"
	"fmt"
	"image/png"
	"net/url"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// Defaults for Options.
//
// Thirty seconds and six digits are not really choices: they are what every
// authenticator app assumes when the URI omits them, and an app that guesses
// differently from the server produces codes that are always wrong.
const (
	DefaultPeriod = 30 * time.Second
	DefaultDigits = 6

	// DefaultSkew accepts the period either side of now, so a phone whose
	// clock has drifted by a few seconds — or a person who started typing as
	// the code rolled over — still gets in. One step is as far as RFC 6238
	// suggests going: each extra step widens the window a guess can land in.
	DefaultSkew = 1
)

// Options control the shape of the codes and how much clock drift is tolerated.
//
// The zero value is usable except for Issuer, which has no sensible default:
// it is the name the user sees beside the account in their authenticator app.
type Options struct {
	// Issuer names the application in the authenticator app. Required by
	// NewSecret, ignored everywhere else.
	Issuer string

	// Period is how long one code lasts. Leave it at zero unless you have a
	// reason; see DefaultPeriod.
	Period time.Duration

	// Digits per code. Six or eight; anything else falls back to six.
	Digits int

	// Skew is how many periods either side of now are also accepted. Zero
	// means DefaultSkew — pass a negative number for no tolerance at all.
	Skew int
}

// withDefaults fills the unset fields, so every entry point behaves the same
// whether it was handed a configured Options or the zero value.
func (o Options) withDefaults() Options {
	if o.Period <= 0 {
		o.Period = DefaultPeriod
	}
	if o.Digits != 8 {
		o.Digits = DefaultDigits
	}
	if o.Skew == 0 {
		o.Skew = DefaultSkew
	}
	if o.Skew < 0 {
		o.Skew = 0
	}
	return o
}

func (o Options) digits() otp.Digits {
	if o.Digits == 8 {
		return otp.DigitsEight
	}
	return otp.DigitsSix
}

// periodSeconds rounds to whole seconds because that is the only unit an
// otpauth:// URI can express.
func (o Options) periodSeconds() uint {
	secs := uint(o.Period / time.Second)
	if secs == 0 {
		secs = uint(DefaultPeriod / time.Second)
	}
	return secs
}

func (o Options) validateOpts() totp.ValidateOpts {
	return totp.ValidateOpts{
		Period:    o.periodSeconds(),
		Skew:      uint(o.Skew),
		Digits:    o.digits(),
		Algorithm: otp.AlgorithmSHA1,
	}
}

// Secret is a freshly generated shared secret, in the two forms enrolment
// needs.
type Secret struct {
	// Base32 is the secret itself, in the RFC 4648 base32 an authenticator app
	// expects when it is typed in by hand rather than scanned. This is the
	// value to seal and store — never store the URI, which contains it.
	Base32 string

	// URI is the otpauth:// URL to render as a QR code.
	URI string
}

// NewSecret generates a secret for account and builds its otpauth:// URI.
//
// account is what the user will see in their authenticator app, so it should be
// something they recognise — an email address rather than a numeric id, since a
// phone holding six of these is otherwise a list of anonymous six-digit codes.
func NewSecret(account string, opts Options) (Secret, error) {
	opts = opts.withDefaults()

	if strings.TrimSpace(opts.Issuer) == "" {
		return Secret{}, fmt.Errorf("twofactor: generating a secret: Options.Issuer is required")
	}
	if strings.TrimSpace(account) == "" {
		return Secret{}, fmt.Errorf("twofactor: generating a secret: account is required")
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      opts.Issuer,
		AccountName: account,
		Period:      opts.periodSeconds(),
		Digits:      opts.digits(),
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return Secret{}, fmt.Errorf("twofactor: generating a secret: %w", err)
	}

	return Secret{Base32: key.Secret(), URI: key.URL()}, nil
}

// URI rebuilds the otpauth:// URI for a secret that already exists.
//
// The URI is never stored — it contains the secret, so keeping it would mean
// keeping a second, unsealed copy. It is rebuilt on demand instead, which is
// what lets a QR code be served from its own endpoint rather than embedded in
// the page that asked for it.
func URI(account, secret string, opts Options) (string, error) {
	opts = opts.withDefaults()

	if strings.TrimSpace(opts.Issuer) == "" {
		return "", fmt.Errorf("twofactor: building the otpauth URI: Options.Issuer is required")
	}

	raw, err := base32NoPadding.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", fmt.Errorf("twofactor: building the otpauth URI: %w", err)
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      opts.Issuer,
		AccountName: account,
		Period:      opts.periodSeconds(),
		Digits:      opts.digits(),
		Algorithm:   otp.AlgorithmSHA1,
		Secret:      raw,
	})
	if err != nil {
		return "", fmt.Errorf("twofactor: building the otpauth URI: %w", err)
	}
	return key.URL(), nil
}

// base32NoPadding is the encoding the otpauth format uses for secrets. The
// padding is dropped because authenticator apps reject the equals signs.
var base32NoPadding = base32.StdEncoding.WithPadding(base32.NoPadding)

// QRCodePNG renders an otpauth:// URI as a PNG QR code, size pixels square.
//
// Serving this from a handler keeps the secret out of the page source, where it
// would otherwise sit in a data: URI in the browser's history and in any proxy
// cache that saw the HTML.
func QRCodePNG(uri string, size int) ([]byte, error) {
	if size <= 0 {
		size = 240
	}

	// The scheme is checked here rather than left to the library, which
	// QR-encodes whatever string it is handed. A QR code built from the wrong
	// value scans perfectly and enrols nothing, and the user only finds out
	// when the first code they type is refused.
	parsed, err := url.Parse(strings.TrimSpace(uri))
	if err != nil || parsed.Scheme != "otpauth" {
		return nil, fmt.Errorf("twofactor: rendering the QR code: %q is not an otpauth URI", uri)
	}

	key, err := otp.NewKeyFromURL(uri)
	if err != nil {
		return nil, fmt.Errorf("twofactor: reading the otpauth URI: %w", err)
	}

	img, err := key.Image(size, size)
	if err != nil {
		return nil, fmt.Errorf("twofactor: rendering the QR code: %w", err)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("twofactor: encoding the QR code: %w", err)
	}
	return buf.Bytes(), nil
}

// Verify reports whether code is valid for secret right now.
//
// This is the unthrottled primitive. Calling it straight from a handler leaves
// a six-digit code exposed to an unlimited guessing loop, which is a million
// tries at worst and rather fewer in practice, since the skew window means
// three codes are live at once. Use Verifier unless something else is already
// counting the attempts.
func Verify(secret, code string, opts Options) bool {
	return VerifyAt(secret, code, time.Now(), opts)
}

// VerifyAt is Verify at an explicit time, which is what makes drift and
// expiry testable without waiting thirty seconds for a code to go stale.
func VerifyAt(secret, code string, t time.Time, opts Options) bool {
	opts = opts.withDefaults()

	code = normalizeCode(code)
	if code == "" {
		return false
	}

	// A secret that will not parse produces no valid codes, so there is nothing
	// for a caller to do with the difference between "wrong code" and
	// "unusable secret" that it would not also do with a plain false. The one
	// case worth distinguishing — a secret that cannot be unsealed because the
	// application key changed — is reported by Vault.Open, before this is
	// reached.
	ok, err := totp.ValidateCustom(code, secret, t.UTC(), opts.validateOpts())
	if err != nil {
		return false
	}
	return ok
}

// normalizeCode strips the spaces authenticator apps put in the middle of a
// code for legibility, which come along when it is copied rather than typed.
func normalizeCode(code string) string {
	var b strings.Builder
	for _, r := range code {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
