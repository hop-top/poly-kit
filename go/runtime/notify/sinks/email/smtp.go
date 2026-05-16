package emailsink

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// ErrSMTPDial is returned when the underlying network dial fails.
var ErrSMTPDial = errors.New("emailsink: smtp dial failed")

// smtpMailer is a Mailer backed by net/smtp. It dials lazily on every
// Send — no connection pooling, by design (keep the reference impl
// simple per the kit-notify scope).
type smtpMailer struct {
	host   string
	port   int
	auth   smtp.Auth
	useTLS bool   // STARTTLS opportunistic upgrade
	from   string // default From when Message.From is empty
	tlsCfg *tls.Config
	dialer *net.Dialer // overridable for tests; nil → default with timeout
}

// smtpOpts is the option bag mutated by SMTPOption closures.
type smtpOpts struct {
	auth   smtp.Auth
	useTLS bool
	from   string
	tlsCfg *tls.Config
	dialer *net.Dialer
}

// SMTPOption configures NewSMTPMailer.
type SMTPOption func(*smtpOpts)

// WithSMTPAuth configures PLAIN authentication with the given
// credentials. The host argument used at Send time is also passed to
// smtp.PlainAuth as the identity host.
func WithSMTPAuth(username, password string) SMTPOption {
	return func(o *smtpOpts) {
		o.auth = plainAuthHolder{username: username, password: password}
	}
}

// WithSMTPTLS enables opportunistic STARTTLS. When enabled, Send
// performs the standard SMTP STARTTLS upgrade against the server and
// errors if the server does not advertise the extension. Default
// false.
func WithSMTPTLS(enabled bool) SMTPOption {
	return func(o *smtpOpts) { o.useTLS = enabled }
}

// WithSMTPFrom sets a default From address used when Message.From is
// empty.
func WithSMTPFrom(addr string) SMTPOption {
	return func(o *smtpOpts) { o.from = addr }
}

// WithSMTPTLSConfig overrides the TLS config used by STARTTLS.
// Defaults to &tls.Config{ServerName: host}.
func WithSMTPTLSConfig(cfg *tls.Config) SMTPOption {
	return func(o *smtpOpts) { o.tlsCfg = cfg }
}

// WithSMTPDialer overrides the net.Dialer used for the underlying
// TCP connection. Tests use this to inject deadlines.
func WithSMTPDialer(d *net.Dialer) SMTPOption {
	return func(o *smtpOpts) { o.dialer = d }
}

// NewSMTPMailer returns a Mailer that dials host:port lazily on every
// Send. No connection pooling. When WithSMTPTLS is set, the mailer
// runs the standard STARTTLS handshake before authenticating.
func NewSMTPMailer(host string, port int, options ...SMTPOption) Mailer {
	o := smtpOpts{}
	for _, opt := range options {
		opt(&o)
	}
	m := &smtpMailer{
		host:   host,
		port:   port,
		auth:   o.auth,
		useTLS: o.useTLS,
		from:   o.from,
		tlsCfg: o.tlsCfg,
		dialer: o.dialer,
	}
	// Resolve PLAIN-auth holder lazily so we can stamp the host.
	if h, ok := m.auth.(plainAuthHolder); ok {
		m.auth = smtp.PlainAuth("", h.username, h.password, host)
	}
	return m
}

// plainAuthHolder defers building the smtp.Auth so we know the host.
type plainAuthHolder struct {
	username, password string
}

// Start / Next satisfy smtp.Auth so the holder can be safely stored
// even if the lazy resolution path is bypassed; it never authenticates
// successfully (the protocol negotiation is handled by smtp.PlainAuth
// once the host is known). NewSMTPMailer always rewrites the holder
// before returning.
func (plainAuthHolder) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	return "", nil, errors.New("emailsink: plain auth not finalized")
}

func (plainAuthHolder) Next(_ []byte, _ bool) ([]byte, error) {
	return nil, errors.New("emailsink: plain auth not finalized")
}

// Send dials the configured host:port, optionally negotiates
// STARTTLS, authenticates, and submits the rendered Message. Honors
// ctx cancellation/deadline through the dialer.
func (m *smtpMailer) Send(ctx context.Context, msg Message) error {
	from := msg.From
	if from == "" {
		from = m.from
	}
	if from == "" {
		return errors.New("emailsink: empty From address")
	}
	if len(msg.To) == 0 {
		return errors.New("emailsink: empty To list")
	}

	addr := net.JoinHostPort(m.host, strconv.Itoa(m.port))

	dialer := m.dialer
	if dialer == nil {
		dialer = &net.Dialer{Timeout: 30 * time.Second}
	}

	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSMTPDial, err)
	}

	c, err := smtp.NewClient(conn, m.host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("emailsink: smtp client: %w", err)
	}
	defer func() { _ = c.Close() }()

	if m.useTLS {
		ok, _ := c.Extension("STARTTLS")
		if !ok {
			return errors.New("emailsink: server does not advertise STARTTLS")
		}
		cfg := m.tlsCfg
		if cfg == nil {
			cfg = &tls.Config{ServerName: m.host}
		}
		if err := c.StartTLS(cfg); err != nil {
			return fmt.Errorf("emailsink: starttls: %w", err)
		}
	}

	if m.auth != nil {
		if ok, _ := c.Extension("AUTH"); ok {
			if err := c.Auth(m.auth); err != nil {
				return fmt.Errorf("emailsink: auth: %w", err)
			}
		}
	}

	if err := c.Mail(from); err != nil {
		return fmt.Errorf("emailsink: MAIL FROM: %w", err)
	}
	for _, to := range msg.To {
		if err := c.Rcpt(to); err != nil {
			return fmt.Errorf("emailsink: RCPT TO %q: %w", to, err)
		}
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("emailsink: DATA: %w", err)
	}
	if _, err := w.Write(buildRFC822(from, msg)); err != nil {
		_ = w.Close()
		return fmt.Errorf("emailsink: write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("emailsink: close body: %w", err)
	}

	return c.Quit()
}

// buildRFC822 assembles a minimal RFC 822 message: From, To, Subject,
// Content-Type, blank line, body. Headers are kept ASCII; callers
// needing internationalised subjects should pre-encode them with the
// MIME encoded-word form.
func buildRFC822(from string, msg Message) []byte {
	ct := msg.ContentType
	if ct == "" {
		ct = DefaultContentType
	}
	var b strings.Builder
	b.WriteString("From: ")
	b.WriteString(from)
	b.WriteString("\r\n")
	b.WriteString("To: ")
	b.WriteString(strings.Join(msg.To, ", "))
	b.WriteString("\r\n")
	b.WriteString("Subject: ")
	b.WriteString(msg.Subject)
	b.WriteString("\r\n")
	b.WriteString("Content-Type: ")
	b.WriteString(ct)
	b.WriteString("\r\n")
	b.WriteString("\r\n")
	b.WriteString(msg.Body)
	return []byte(b.String())
}
