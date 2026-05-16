package emailsink_test

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
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

// TestSMTPMailer_DialFailure_ReturnsError points the mailer at a port
// nothing's listening on. Send should return an error wrapped around
// ErrSMTPDial within a reasonable timeout.
func TestSMTPMailer_DialFailure_ReturnsError(t *testing.T) {
	t.Parallel()

	port := unusedPort(t)

	m := emailsink.NewSMTPMailer("127.0.0.1", port,
		emailsink.WithSMTPFrom("noreply@example.com"),
		emailsink.WithSMTPDialer(&net.Dialer{Timeout: 500 * time.Millisecond}),
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

// unusedPort returns a TCP port currently bound by the kernel but
// immediately released, giving the test a port that is highly likely
// to be unbound by the time Send dials.
func unusedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return port
}
