package mail

import (
	"context"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
)

func basic() Message {
	return Message{
		From:    Address{Name: "Shop", Email: "hello@shop.test"},
		To:      To("alice@example.com"),
		Subject: "Welcome",
		Text:    "Hello Alice",
	}
}

// parse reads a built message back with the standard library, which is the only
// honest way to check it is well formed.
func parse(t *testing.T, msg Message) *mail.Message {
	t.Helper()

	raw, err := msg.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	parsed, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("the built message does not parse: %v\n%s", err, raw)
	}
	return parsed
}

func TestBuildsAParsableMessage(t *testing.T) {
	parsed := parse(t, basic())

	for header, want := range map[string]string{
		"From":         `"Shop" <hello@shop.test>`,
		"To":           "alice@example.com",
		"MIME-Version": "1.0",
	} {
		if got := parsed.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if parsed.Header.Get("Message-ID") == "" {
		t.Error("no Message-ID was set")
	}
	if parsed.Header.Get("Date") == "" {
		t.Error("no Date was set")
	}
}

// TestSubjectIsEncoded guards against a subject with an accent in it arriving
// as mojibake.
func TestSubjectIsEncoded(t *testing.T) {
	msg := basic()
	msg.Subject = "Café — your receipt"

	parsed := parse(t, msg)
	raw := parsed.Header.Get("Subject")

	if strings.Contains(raw, "Café") {
		t.Errorf("Subject was written as raw UTF-8: %q", raw)
	}

	decoded, err := new(mime.WordDecoder).DecodeHeader(raw)
	if err != nil {
		t.Fatalf("decode subject: %v", err)
	}
	if decoded != msg.Subject {
		t.Errorf("Subject decoded to %q, want %q", decoded, msg.Subject)
	}
}

// TestHeaderInjectionIsRejected is the one that matters for anything built from
// user input: a newline in an address would let a caller append headers of
// their own, which is how a contact form becomes an open relay.
//
// The address is rejected rather than sanitised. Quietly mangling it into
// something almost right and sending anyway is worse than telling the caller.
func TestHeaderInjectionIsRejected(t *testing.T) {
	injections := []Address{
		{Email: "attacker@evil.test\r\nBcc: victim@example.com"},
		{Email: "attacker@evil.test\nBcc: victim@example.com"},
		{Name: "Evil\r\nBcc: victim@example.com", Email: "a@b.test"},
	}

	for _, from := range injections {
		msg := basic()
		msg.From = from

		if _, err := msg.Build(); err == nil {
			t.Errorf("an address containing a line break was accepted: %q", from.Email+from.Name)
		}
	}
}

// TestNoInjectedHeaderIsParsable is the defence in depth behind the check
// above: even if a bad value reaches the writer, it must not become a header.
func TestNoInjectedHeaderIsParsable(t *testing.T) {
	msg := basic()
	msg.Headers = map[string]string{"X-Thing": "ok\r\nBcc: victim@example.com"}

	parsed := parse(t, msg)
	if got := parsed.Header.Get("Bcc"); got != "" {
		t.Errorf("an injected Bcc header was parsed: %q", got)
	}
}

// TestBccIsNotInTheHeaders is the whole point of Bcc: the addresses go in the
// envelope, and writing them here would show every hidden recipient to
// everybody else.
func TestBccIsNotInTheHeaders(t *testing.T) {
	msg := basic()
	msg.Bcc = To("secret@example.com")

	raw, err := msg.Build()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret@example.com") {
		t.Error("a Bcc address appeared in the message headers")
	}

	// It still has to be delivered there.
	found := false
	for _, r := range msg.Recipients() {
		if r == "secret@example.com" {
			found = true
		}
	}
	if !found {
		t.Error("the Bcc address is missing from the envelope recipients")
	}
}

func TestMultipartWhenBothBodiesAreSet(t *testing.T) {
	msg := basic()
	msg.HTML = "<p>Hello <b>Alice</b></p>"

	parsed := parse(t, msg)

	mediaType, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse Content-Type: %v", err)
	}
	if mediaType != "multipart/alternative" {
		t.Fatalf("Content-Type = %q, want multipart/alternative", mediaType)
	}

	reader := multipart.NewReader(parsed.Body, params["boundary"])
	var types []string
	var bodies []string

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read part: %v", err)
		}

		mt, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		types = append(types, mt)

		decoded, err := io.ReadAll(quotedprintable.NewReader(part))
		if err != nil {
			t.Fatalf("decode part: %v", err)
		}
		bodies = append(bodies, string(decoded))
	}

	// Plain text first: a client picks the last part it understands, so the
	// richer alternative has to come second or nobody sees the HTML.
	if len(types) != 2 || types[0] != "text/plain" || types[1] != "text/html" {
		t.Errorf("parts = %v, want [text/plain text/html] in that order", types)
	}
	if !strings.Contains(bodies[0], "Hello Alice") {
		t.Errorf("text part = %q", bodies[0])
	}
	if !strings.Contains(bodies[1], "<b>Alice</b>") {
		t.Errorf("html part = %q", bodies[1])
	}
}

func TestSingleBodyIsNotMultipart(t *testing.T) {
	parsed := parse(t, basic())

	mt, _, _ := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if mt != "text/plain" {
		t.Errorf("Content-Type = %q, want text/plain", mt)
	}

	body, err := io.ReadAll(quotedprintable.NewReader(parsed.Body))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Hello Alice") {
		t.Errorf("body = %q", body)
	}
}

func TestValidation(t *testing.T) {
	tests := map[string]func(*Message){
		"no from":       func(m *Message) { m.From = Address{} },
		"no recipients": func(m *Message) { m.To = nil },
		"no body":       func(m *Message) { m.Text = ""; m.HTML = "" },
	}

	for name, break_ := range tests {
		t.Run(name, func(t *testing.T) {
			msg := basic()
			break_(&msg)
			if err := msg.Validate(); err == nil {
				t.Error("an invalid message passed validation")
			}
			if _, err := msg.Build(); err == nil {
				t.Error("an invalid message was built anyway")
			}
		})
	}
}

func TestRecipientsIncludesEveryGroup(t *testing.T) {
	msg := basic()
	msg.Cc = To("cc@example.com")
	msg.Bcc = To("bcc@example.com")

	got := strings.Join(msg.Recipients(), ",")
	want := "alice@example.com,cc@example.com,bcc@example.com"
	if got != want {
		t.Errorf("Recipients = %q, want %q", got, want)
	}
}

func TestMemoryMailerRecordsMessages(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()

	if err := m.Send(ctx, basic()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	sent := m.Sent()
	if len(sent) != 1 || sent[0].Subject != "Welcome" {
		t.Fatalf("Sent() = %+v", sent)
	}

	last, ok := m.Last()
	if !ok || last.Subject != "Welcome" {
		t.Errorf("Last() = %+v, %v", last, ok)
	}

	m.Reset()
	if len(m.Sent()) != 0 {
		t.Error("Reset did not clear the messages")
	}
}

func TestMemoryMailerRejectsInvalidMessages(t *testing.T) {
	m := NewMemory()

	// A test that silently records an unsendable message would pass while the
	// real mailer failed.
	if err := m.Send(context.Background(), Message{}); err == nil {
		t.Error("an invalid message was accepted")
	}
}

func TestLogMailerDoesNotSend(t *testing.T) {
	var buf strings.Builder
	m := NewLog(slog.New(slog.NewTextHandler(&buf, nil)))

	if err := m.Send(context.Background(), basic()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	out := buf.String()
	// The text body is always logged: a confirmation or reset link exists only
	// inside the message, so without it the log driver makes those flows
	// impossible to finish locally.
	for _, want := range []string{"alice@example.com", "Welcome", "Hello Alice"} {
		if !strings.Contains(out, want) {
			t.Errorf("log is missing %q: %s", want, out)
		}
	}
}

func TestParseEncryption(t *testing.T) {
	tests := map[string]Encryption{
		"":         EncryptionStartTLS,
		"starttls": EncryptionStartTLS,
		"TLS":      EncryptionTLS,
		"ssl":      EncryptionTLS,
		"none":     EncryptionNone,
		"nonsense": EncryptionStartTLS,
	}
	for in, want := range tests {
		if got := ParseEncryption(in); got != want {
			t.Errorf("ParseEncryption(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAddressFormatting(t *testing.T) {
	if got := (Address{Email: "a@b.test"}).String(); got != "a@b.test" {
		t.Errorf("bare address = %q", got)
	}
	// A display name with a comma has to be quoted, or it splits the header
	// into two addresses.
	got := (Address{Name: "Shop, Inc", Email: "a@b.test"}).String()
	if !strings.HasPrefix(got, `"Shop, Inc"`) {
		t.Errorf("name with a comma was not quoted: %q", got)
	}
}

func TestQueuedMailerValidatesBeforeQueueing(t *testing.T) {
	// A malformed address discovered five retries later, in a log nobody is
	// reading, is much worse than an error returned to the code that built it.
	q := &Queued{}
	if err := q.Send(context.Background(), Message{}); err == nil {
		t.Error("an invalid message was queued")
	}
}

func TestSendJobPolicy(t *testing.T) {
	var j SendJob

	if j.Name() != JobName {
		t.Errorf("Name = %q", j.Name())
	}
	// Its own queue, so a slow mail server cannot hold up everything else.
	if j.Queue() != "mail" {
		t.Errorf("Queue = %q, want mail", j.Queue())
	}
	// Mail servers fail temporarily far more often than permanently.
	if j.MaxAttempts() <= 3 {
		t.Errorf("MaxAttempts = %d, want more than the default 3", j.MaxAttempts())
	}
	if a, b := j.Backoff(1), j.Backoff(3); b <= a {
		t.Errorf("backoff did not grow: %v then %v", a, b)
	}
}

func TestSendJobWithoutAMailerFails(t *testing.T) {
	sender = nil
	j := &SendJob{Message: basic()}

	if err := j.Run(context.Background()); err == nil {
		t.Error("the job ran with no mailer registered")
	}
}
