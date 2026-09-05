package emailsink_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"hop.top/kit/go/core/netpolicy"
	emailsink "hop.top/kit/go/runtime/notify/sinks/email"
)

// sendTimeout bounds every Send below. A regression that fails to block
// must fail an assertion, never hang the battery.
const sendTimeout = 5 * time.Second

// smtpListener starts a TCP listener that accepts and immediately closes.
// Reaching it proves the dial was NOT blocked.
func smtpListener(t *testing.T) (host string, port int, reached func() bool) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	got := make(chan struct{}, 8)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			select {
			case got <- struct{}{}:
			default:
			}
			_ = c.Close()
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port, func() bool {
		select {
		case <-got:
			return true
		case <-time.After(200 * time.Millisecond):
			return false
		}
	}
}

func offlineSendCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(netpolicy.WithOffline(t.Context(), true), sendTimeout)
	t.Cleanup(cancel)
	return ctx
}

// The mailer dials lazily on every Send, so the refusal must happen on
// Send against a live listener — not merely at construction.
func TestSMTPMailer_OfflineRefusesRemoteSend(t *testing.T) {
	_, port, reached := smtpListener(t)

	// A remote name on the live listener's port: the policy must refuse
	// on the address alone, so a blocked dial cannot be mistaken for a
	// dial that merely failed to resolve.
	m := emailsink.NewSMTPMailer("kit-offline-probe.invalid", port,
		emailsink.WithSMTPFrom("ops@example.com"),
		emailsink.WithSMTPDialer(&net.Dialer{Timeout: sendTimeout}),
	)

	err := m.Send(offlineSendCtx(t), emailsink.Message{
		To:      []string{"c@d.io"},
		Subject: "x",
		Body:    "y",
	})
	if !errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("offline Send not refused: %v", err)
	}
	// The package's own sentinel must survive alongside the policy one.
	if !errors.Is(err, emailsink.ErrSMTPDial) {
		t.Errorf("offline refusal lost ErrSMTPDial: %v", err)
	}
	if reached() {
		t.Fatal("SMTP Send reached a listener despite offline context")
	}
}

// Loopback stays reachable while offline: --offline means "no network",
// not "cannot talk to myself". A local relay must keep working.
func TestSMTPMailer_OfflineAllowsLoopback(t *testing.T) {
	host, port, reached := smtpListener(t)

	m := emailsink.NewSMTPMailer(host, port,
		emailsink.WithSMTPFrom("ops@example.com"),
		emailsink.WithSMTPDialer(&net.Dialer{Timeout: sendTimeout}),
	)

	// The listener closes immediately, so Send fails at the protocol
	// stage. What matters is that it got past the dial: the error must
	// NOT be the offline refusal.
	err := m.Send(offlineSendCtx(t), emailsink.Message{
		To:      []string{"c@d.io"},
		Subject: "x",
		Body:    "y",
	})
	if errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("loopback Send refused while offline: %v", err)
	}
	if !reached() {
		t.Fatal("loopback Send never reached the listener")
	}
}

// An untagged context must be entirely unaffected by the guard.
func TestSMTPMailer_OnlineDialsRemoteNormally(t *testing.T) {
	_, port, reached := smtpListener(t)

	m := emailsink.NewSMTPMailer("localhost", port,
		emailsink.WithSMTPFrom("ops@example.com"),
		emailsink.WithSMTPDialer(&net.Dialer{Timeout: sendTimeout}),
	)

	ctx, cancel := context.WithTimeout(t.Context(), sendTimeout)
	defer cancel()
	err := m.Send(ctx, emailsink.Message{
		To:      []string{"c@d.io"},
		Subject: "x",
		Body:    "y",
	})
	if errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("untagged context was refused: %v", err)
	}
	if !reached() {
		t.Fatal("online Send never reached the listener")
	}
}
