package emailsink_test

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"syscall"
	"testing"
	"time"

	emailsink "hop.top/kit/go/runtime/notify/sinks/email"
)

func TestNewSMTPMailer_OptionsThreadThrough(t *testing.T) {
	t.Parallel()

	// Construction with all options should succeed and return a
	// non-nil Mailer regardless of internal shape. We exercise the
	// observable behavior (Send) in subsequent tests; here we just
	// confirm that the constructor accepts every option.
	cfg := &tls.Config{ServerName: "example.com"}
	m := emailsink.NewSMTPMailer("smtp.example.com", 587,
		emailsink.WithSMTPAuth("user", "pass"),
		emailsink.WithSMTPTLS(true),
		emailsink.WithSMTPFrom("noreply@example.com"),
		emailsink.WithSMTPTLSConfig(cfg),
		emailsink.WithSMTPDialer(&net.Dialer{Timeout: time.Second}),
	)
	if m == nil {
		t.Fatal("NewSMTPMailer returned nil")
	}
}

// TestSMTPMailer_DialFailure_ReturnsError fails the mailer's dial. Send
// should return an error wrapped around ErrSMTPDial within a reasonable
// timeout. The dialer refuses before connecting: a port freed for the
// test could be taken by another listener before Send dials it.
func TestSMTPMailer_DialFailure_ReturnsError(t *testing.T) {
	t.Parallel()

	refused := errors.New("refused by the test dialer")
	m := emailsink.NewSMTPMailer("127.0.0.1", 25,
		emailsink.WithSMTPFrom("noreply@example.com"),
		emailsink.WithSMTPDialer(&net.Dialer{
			Timeout: 500 * time.Millisecond,
			Control: func(string, string, syscall.RawConn) error { return refused },
		}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := m.Send(ctx, emailsink.Message{
		From:    "a@b.io",
		To:      []string{"c@d.io"},
		Subject: "x",
		Body:    "y",
	})
	if err == nil {
		t.Fatal("expected dial error")
	}
	if !errors.Is(err, emailsink.ErrSMTPDial) {
		t.Errorf("err = %v; want errors.Is(ErrSMTPDial) true", err)
	}
	if !errors.Is(err, refused) {
		t.Errorf("err = %v; want the dial's own error kept", err)
	}
}

func TestSMTPMailer_EmptyFrom_Errors(t *testing.T) {
	t.Parallel()

	m := emailsink.NewSMTPMailer("127.0.0.1", 25)
	err := m.Send(context.Background(), emailsink.Message{
		To:      []string{"c@d.io"},
		Subject: "x",
		Body:    "y",
	})
	if err == nil {
		t.Fatal("expected error on empty From")
	}
}

func TestSMTPMailer_EmptyTo_Errors(t *testing.T) {
	t.Parallel()

	m := emailsink.NewSMTPMailer("127.0.0.1", 25,
		emailsink.WithSMTPFrom("ops@example.com"),
	)
	err := m.Send(context.Background(), emailsink.Message{
		Subject: "x",
		Body:    "y",
	})
	if err == nil {
		t.Fatal("expected error on empty To")
	}
}
