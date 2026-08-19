package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"time"
)

// Mailer sends messages.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// Encryption selects how the SMTP connection is protected.
type Encryption string

const (
	// EncryptionStartTLS connects in the clear and upgrades. This is what
	// ports 587 and 25 expect.
	EncryptionStartTLS Encryption = "starttls"
	// EncryptionTLS connects over TLS from the first byte, as port 465 expects.
	EncryptionTLS Encryption = "tls"
	// EncryptionNone sends in the clear. Only ever appropriate for a local
	// capture server such as Mailpit.
	EncryptionNone Encryption = "none"
)

// ParseEncryption maps a configuration string, defaulting to STARTTLS.
func ParseEncryption(s string) Encryption {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "tls", "ssl":
		return EncryptionTLS
	case "none", "insecure", "":
		if s == "" {
			return EncryptionStartTLS
		}
		return EncryptionNone
	default:
		return EncryptionStartTLS
	}
}

// SMTPConfig describes a mail server.
type SMTPConfig struct {
	Host       string
	Port       int
	Username   string
	Password   string
	Encryption Encryption
	// Timeout bounds the whole exchange. Without one, an unresponsive server
	// holds a queue worker forever.
	Timeout time.Duration
	// InsecureSkipVerify disables certificate checking. Only for a local
	// capture server with a self-signed certificate; anywhere else it removes
	// the point of using TLS.
	InsecureSkipVerify bool
}

// SMTPMailer delivers over SMTP.
type SMTPMailer struct {
	cfg SMTPConfig
}

// NewSMTP builds an SMTP mailer.
func NewSMTP(cfg SMTPConfig) *SMTPMailer {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.Encryption == "" {
		cfg.Encryption = EncryptionStartTLS
	}
	return &SMTPMailer{cfg: cfg}
}

// Send implements Mailer.
func (m *SMTPMailer) Send(ctx context.Context, msg Message) error {
	body, err := msg.Build()
	if err != nil {
		return err
	}

	addr := net.JoinHostPort(m.cfg.Host, fmt.Sprint(m.cfg.Port))
	dialer := &net.Dialer{Timeout: m.cfg.Timeout}

	var conn net.Conn
	if m.cfg.Encryption == EncryptionTLS {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, m.tlsConfig())
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("mail: connect to %s: %w", addr, err)
	}
	defer conn.Close()

	// A deadline on the connection, so a server that accepts the socket and
	// then goes quiet cannot hold the worker indefinitely.
	_ = conn.SetDeadline(time.Now().Add(m.cfg.Timeout))

	client, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		return fmt.Errorf("mail: smtp handshake: %w", err)
	}
	defer client.Close()

	if m.cfg.Encryption == EncryptionStartTLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(m.tlsConfig()); err != nil {
				return fmt.Errorf("mail: starttls: %w", err)
			}
		}
	}

	if m.cfg.Username != "" {
		// PlainAuth refuses to send credentials over an unencrypted connection
		// unless the server is localhost, which is exactly the check we want.
		auth := smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("mail: authenticate: %w", err)
		}
	}

	if err := client.Mail(msg.From.Email); err != nil {
		return fmt.Errorf("mail: sender rejected: %w", err)
	}
	for _, to := range msg.Recipients() {
		if err := client.Rcpt(to); err != nil {
			return fmt.Errorf("mail: recipient %s rejected: %w", to, err)
		}
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("mail: data: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("mail: write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: finish body: %w", err)
	}

	return client.Quit()
}

func (m *SMTPMailer) tlsConfig() *tls.Config {
	return &tls.Config{
		ServerName:         m.cfg.Host,
		InsecureSkipVerify: m.cfg.InsecureSkipVerify, //nolint:gosec // opt-in, documented
		MinVersion:         tls.VersionTLS12,
	}
}

// LogMailer prints messages instead of sending them.
//
// This is the right default in development: a real send during local work is at
// best a nuisance and at worst an email to a customer from someone's laptop.
type LogMailer struct {
	Log *slog.Logger
	// Full includes the entire rendered MIME message. Off by default, since a
	// message with an HTML part is a wall of markup.
	Full bool
}

// NewLog builds a log mailer.
func NewLog(log *slog.Logger) *LogMailer { return &LogMailer{Log: log} }

// Send implements Mailer.
func (m *LogMailer) Send(_ context.Context, msg Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}

	log := m.Log
	if log == nil {
		log = slog.Default()
	}

	attrs := []any{
		"to", strings.Join(msg.Recipients(), ", "),
		"from", msg.From.Email,
		"subject", msg.Subject,
	}

	// The plain-text body is always logged, and that is deliberate: a
	// confirmation or password-reset link only exists inside the message, so
	// without this the log driver makes those flows impossible to finish
	// locally. Text rather than HTML keeps it readable.
	if body := strings.TrimSpace(msg.Text); body != "" {
		attrs = append(attrs, "text", body)
	}
	if m.Full {
		full, err := msg.Build()
		if err != nil {
			return err
		}
		attrs = append(attrs, "raw", string(full))
	}

	log.Info("mail not sent (log driver)", attrs...)
	return nil
}

// MemoryMailer keeps messages so tests can assert on them.
type MemoryMailer struct {
	mu   sync.Mutex
	sent []Message
}

// NewMemory builds an in-memory mailer.
func NewMemory() *MemoryMailer { return &MemoryMailer{} }

// Send implements Mailer.
func (m *MemoryMailer) Send(_ context.Context, msg Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

// Sent returns every message sent so far.
func (m *MemoryMailer) Sent() []Message {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Message, len(m.sent))
	copy(out, m.sent)
	return out
}

// Last returns the most recent message, and whether there was one.
func (m *MemoryMailer) Last() (Message, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.sent) == 0 {
		return Message{}, false
	}
	return m.sent[len(m.sent)-1], true
}

// Reset discards everything sent.
func (m *MemoryMailer) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = nil
}
