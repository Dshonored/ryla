// Package mail composes and sends email.
//
// Messages are built as MIME in memory and handed to a Mailer. Three drivers
// ship: SMTP for real delivery, a log driver that prints instead of sending —
// the right default in development, where a real send is at best a nuisance —
// and an in-memory driver for tests.
package mail

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"time"
)

// Address is a recipient or sender.
type Address struct {
	Name  string
	Email string
}

// String renders the address for a header, encoding the display name when it
// contains anything outside ASCII.
func (a Address) String() string {
	if a.Name == "" {
		return a.Email
	}
	return (&mail.Address{Name: a.Name, Address: a.Email}).String()
}

// To builds an address list from plain email strings.
func To(emails ...string) []Address {
	out := make([]Address, 0, len(emails))
	for _, e := range emails {
		out = append(out, Address{Email: e})
	}
	return out
}

// Message is an email waiting to be sent.
//
// Set Text, HTML, or both. Sending both produces a multipart/alternative
// message, which is what lets a mail client show the HTML while a plain-text
// reader still gets something legible — and what keeps spam filters calmer than
// an HTML-only message.
type Message struct {
	From    Address
	To      []Address
	Cc      []Address
	Bcc     []Address
	ReplyTo *Address

	Subject string
	Text    string
	HTML    string

	// Headers are added verbatim. Use them for things like List-Unsubscribe.
	Headers map[string]string
}

// Recipients returns every address the message must be delivered to, including
// Bcc — which appears in the envelope but never in the headers.
func (m Message) Recipients() []string {
	var out []string
	for _, group := range [][]Address{m.To, m.Cc, m.Bcc} {
		for _, a := range group {
			out = append(out, a.Email)
		}
	}
	return out
}

// Validate reports whether the message can be sent.
func (m Message) Validate() error {
	if m.From.Email == "" {
		return errors.New("mail: message has no From address")
	}
	if len(m.Recipients()) == 0 {
		return errors.New("mail: message has no recipients")
	}
	if m.Text == "" && m.HTML == "" {
		return errors.New("mail: message has no body")
	}

	// Addresses are rejected rather than sanitised. A value carrying a newline
	// is not a real address that needs cleaning up — it is an attempt to append
	// headers, and the caller should hear about it rather than have a mangled
	// version sent on their behalf.
	for _, a := range append([]Address{m.From}, m.allAddresses()...) {
		if err := validAddress(a); err != nil {
			return err
		}
	}
	return nil
}

// allAddresses returns every recipient plus Reply-To.
func (m Message) allAddresses() []Address {
	out := make([]Address, 0, len(m.To)+len(m.Cc)+len(m.Bcc)+1)
	out = append(out, m.To...)
	out = append(out, m.Cc...)
	out = append(out, m.Bcc...)
	if m.ReplyTo != nil {
		out = append(out, *m.ReplyTo)
	}
	return out
}

func validAddress(a Address) error {
	const lineBreaks = "\r\n"

	if strings.ContainsAny(a.Email, lineBreaks) || strings.ContainsAny(a.Name, lineBreaks) {
		return fmt.Errorf("mail: address %q contains a line break", a.Email)
	}
	if !strings.Contains(a.Email, "@") {
		return fmt.Errorf("mail: %q is not an email address", a.Email)
	}
	return nil
}

// Build renders the message as RFC 5322 bytes.
func (m Message) Build() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}

	var b bytes.Buffer

	header(&b, "Date", time.Now().Format(time.RFC1123Z))
	header(&b, "From", m.From.String())
	header(&b, "To", joinAddresses(m.To))

	if len(m.Cc) > 0 {
		header(&b, "Cc", joinAddresses(m.Cc))
	}
	// Bcc is deliberately absent: it goes in the envelope, and writing it here
	// would show every hidden recipient to everyone else.
	if m.ReplyTo != nil {
		header(&b, "Reply-To", m.ReplyTo.String())
	}

	// RFC 2047 encoding, or a subject with an accent in it arrives as mojibake.
	header(&b, "Subject", mime.QEncoding.Encode("UTF-8", m.Subject))
	header(&b, "Message-ID", messageID(m.From.Email))
	header(&b, "MIME-Version", "1.0")

	for k, v := range m.Headers {
		header(&b, k, v)
	}

	switch {
	case m.Text != "" && m.HTML != "":
		boundary, err := newBoundary()
		if err != nil {
			return nil, err
		}
		header(&b, "Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
		b.WriteString("\r\n")

		// Plain text first: a client picks the last part it understands, so the
		// richer alternative has to come second.
		if err := part(&b, boundary, "text/plain; charset=UTF-8", m.Text); err != nil {
			return nil, err
		}
		if err := part(&b, boundary, "text/html; charset=UTF-8", m.HTML); err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "--%s--\r\n", boundary)

	case m.HTML != "":
		header(&b, "Content-Type", "text/html; charset=UTF-8")
		header(&b, "Content-Transfer-Encoding", "quoted-printable")
		b.WriteString("\r\n")
		if err := writeQP(&b, m.HTML); err != nil {
			return nil, err
		}

	default:
		header(&b, "Content-Type", "text/plain; charset=UTF-8")
		header(&b, "Content-Transfer-Encoding", "quoted-printable")
		b.WriteString("\r\n")
		if err := writeQP(&b, m.Text); err != nil {
			return nil, err
		}
	}

	return b.Bytes(), nil
}

func header(b *bytes.Buffer, key, value string) {
	// Strip CR and LF from values: a newline in a header lets a caller inject
	// arbitrary headers, which is how a contact form becomes an open relay.
	value = strings.NewReplacer("\r", "", "\n", "").Replace(value)
	fmt.Fprintf(b, "%s: %s\r\n", key, value)
}

func part(b *bytes.Buffer, boundary, contentType, body string) error {
	fmt.Fprintf(b, "--%s\r\n", boundary)
	fmt.Fprintf(b, "Content-Type: %s\r\n", contentType)
	fmt.Fprintf(b, "Content-Transfer-Encoding: quoted-printable\r\n\r\n")

	if err := writeQP(b, body); err != nil {
		return err
	}
	b.WriteString("\r\n")
	return nil
}

// writeQP encodes a body as quoted-printable, which keeps the message readable
// in a raw form while staying inside SMTP's line-length limits.
func writeQP(b *bytes.Buffer, body string) error {
	w := quotedprintable.NewWriter(b)
	if _, err := w.Write([]byte(normalizeNewlines(body))); err != nil {
		return err
	}
	return w.Close()
}

// normalizeNewlines converts to CRLF, which SMTP requires.
func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

func joinAddresses(addrs []Address) string {
	parts := make([]string, 0, len(addrs))
	for _, a := range addrs {
		parts = append(parts, a.String())
	}
	return strings.Join(parts, ", ")
}

func newBoundary() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("mail: boundary: %w", err)
	}
	return "ryla-" + base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func messageID(from string) string {
	var b [12]byte
	_, _ = rand.Read(b[:])

	domain := "localhost"
	if _, host, found := strings.Cut(from, "@"); found && host != "" {
		domain = host
	}
	return "<" + base64.RawURLEncoding.EncodeToString(b[:]) + "@" + domain + ">"
}
