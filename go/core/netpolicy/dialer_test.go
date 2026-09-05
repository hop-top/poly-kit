package netpolicy_test

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"net"
	"net/smtp"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"hop.top/kit/go/core/netpolicy"
)

// dialTimeout bounds every dial in this file. A regression that fails to
// block must fail an assertion, never hang the battery.
const dialTimeout = 5 * time.Second

// listener starts a TCP listener that accepts and immediately closes, and
// returns its address. Reaching it proves the dial was NOT blocked.
func listener(t *testing.T) (addr string, reached func() bool) {
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
	return ln.Addr().String(), func() bool {
		select {
		case <-got:
			return true
		case <-time.After(200 * time.Millisecond):
			return false
		}
	}
}

// offlineCtx returns a bounded, offline-marked context.
func offlineCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(netpolicy.WithOffline(t.Context(), true), dialTimeout)
	t.Cleanup(cancel)
	return ctx
}

// rewriteLoopbackPort turns 127.0.0.1:N into a non-loopback address that
// still names a real port, so a *blocked* dial and a *failed* dial cannot
// be confused: the guard must refuse before any resolution is attempted.
func hostPort(t *testing.T, addr string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split %q: %v", addr, err)
	}
	return net.JoinHostPort("kit-offline-probe.invalid", port)
}

// A raw dial to a remote host must be refused, and must not touch the
// network: the live listener stands in for "the wire".
func TestGuardDial_BlocksRawDialWhenOffline(t *testing.T) {
	addr, reached := listener(t)
	dial := netpolicy.GuardDial((&net.Dialer{}).DialContext)

	// Same port, remote name: the policy must refuse on the address
	// alone, without resolving it.
	_, err := dial(offlineCtx(t), "tcp", hostPort(t, addr))
	if !errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("raw dial not blocked: %v", err)
	}
	if reached() {
		t.Fatal("raw dial reached a listener despite offline context")
	}
}

// The guard must not break normal operation: an untagged context dials
// through, and loopback stays reachable even when offline.
func TestGuardDial_AllowsWhenOnlineAndLoopback(t *testing.T) {
	addr, reached := listener(t)
	dial := netpolicy.GuardDial((&net.Dialer{}).DialContext)

	ctx, cancel := context.WithTimeout(t.Context(), dialTimeout)
	defer cancel()
	conn, err := dial(ctx, "tcp", addr)
	if err != nil {
		t.Fatalf("online dial refused: %v", err)
	}
	_ = conn.Close()
	if !reached() {
		t.Fatal("online dial never reached the listener")
	}

	conn2, err := dial(offlineCtx(t), "tcp", addr)
	if err != nil {
		t.Fatalf("loopback dial refused while offline: %v", err)
	}
	_ = conn2.Close()
	if !reached() {
		t.Fatal("loopback dial was blocked while offline")
	}
}

// unix sockets are filesystem objects, not network: --offline must not
// sever a local IPC channel.
func TestGuardDial_AllowsUnixSocketWhenOffline(t *testing.T) {
	// Not t.TempDir(): its path routinely exceeds the ~104-byte sockaddr
	// limit, which would skip this test rather than run it.
	dir, err := os.MkdirTemp("", "np")
	if err != nil {
		t.Fatalf("tempdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	sock := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("unix listen: %v", err)
	}
	defer ln.Close()
	go func() {
		if c, err := ln.Accept(); err == nil {
			_ = c.Close()
		}
	}()

	dial := netpolicy.GuardDial((&net.Dialer{}).DialContext)
	conn, err := dial(offlineCtx(t), "unix", sock)
	if err != nil {
		t.Fatalf("unix socket blocked while offline: %v", err)
	}
	_ = conn.Close()
}

// SMTP is kit's one raw-dial egress path. Routing its dialer through
// GuardDial must refuse before net/smtp ever sees a conn.
func TestGuardDial_BlocksSMTPWhenOffline(t *testing.T) {
	addr, reached := listener(t)
	dial := netpolicy.GuardDial((&net.Dialer{}).DialContext)

	conn, err := dial(offlineCtx(t), "tcp", hostPort(t, addr))
	if !errors.Is(err, netpolicy.ErrOffline) {
		if conn != nil {
			c, _ := smtp.NewClient(conn, "mail.invalid")
			if c != nil {
				_ = c.Close()
			}
			_ = conn.Close()
		}
		t.Fatalf("SMTP dial not blocked: %v", err)
	}
	if reached() {
		t.Fatal("SMTP dial reached a listener despite offline context")
	}
}

// Raw TLS: the dial beneath tls.Client must be refused, so no handshake
// with a remote peer can begin.
func TestGuardDial_BlocksTLSWhenOffline(t *testing.T) {
	addr, reached := listener(t)
	dial := netpolicy.GuardDial(nil) // nil base -> zero net.Dialer

	conn, err := dial(offlineCtx(t), "tcp", hostPort(t, addr))
	if !errors.Is(err, netpolicy.ErrOffline) {
		if conn != nil {
			_ = tls.Client(conn, &tls.Config{InsecureSkipVerify: true}).Close() //nolint:gosec // never reached
		}
		t.Fatalf("TLS dial not blocked: %v", err)
	}
	if reached() {
		t.Fatal("TLS dial reached a listener despite offline context")
	}
}

// gRPC and every other library taking a dial function: the guarded
// DialFunc satisfies that signature directly, so the refusal is the
// library's dial error.
func TestGuardDial_BlocksInjectedDialerSignature(t *testing.T) {
	addr, reached := listener(t)

	// Exactly the shape grpc.WithContextDialer / DialOptions want.
	var injected func(context.Context, string) (net.Conn, error)
	guarded := netpolicy.GuardDial(nil)
	injected = func(ctx context.Context, a string) (net.Conn, error) {
		return guarded(ctx, "tcp", a)
	}

	if _, err := injected(offlineCtx(t), hostPort(t, addr)); !errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("injected dialer not blocked: %v", err)
	}
	if reached() {
		t.Fatal("injected dialer reached a listener despite offline context")
	}
}

// database/sql through the driver's own dial hook: the marker must
// survive sql.Open's laziness and refuse at first use.
func TestGuardDial_BlocksSQLOpenWhenOffline(t *testing.T) {
	addr, reached := listener(t)

	cfg := mysql.NewConfig()
	cfg.User = "kit"
	cfg.Net = "tcp"
	cfg.Addr = hostPort(t, addr)
	cfg.DBName = "kit"
	cfg.DialFunc = netpolicy.GuardDial(nil)

	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		t.Fatalf("connector: %v", err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()

	// sql.Open/OpenDB is lazy; the dial happens here.
	if err := db.PingContext(offlineCtx(t)); !errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("SQL dial not blocked: %v", err)
	}
	if reached() {
		t.Fatal("SQL driver reached a listener despite offline context")
	}
}

// CheckDial is the decision without a conn, for hooks that are not
// dialers. It must agree with GuardDial on every exemption.
func TestCheckDial_MatchesGuardExemptions(t *testing.T) {
	off := offlineCtx(t)
	blocked := []struct{ network, addr string }{
		{"tcp", "db.example.com:3306"},
		{"tcp", "10.0.0.5:443"},
	}
	allowed := []struct{ network, addr string }{
		{"tcp", "127.0.0.1:5432"},
		{"tcp", "localhost:25"},
		{"tcp", "[::1]:9000"},
		{"unix", "/tmp/kit.sock"},
	}
	for _, c := range blocked {
		if err := netpolicy.CheckDial(off, c.network, c.addr); !errors.Is(err, netpolicy.ErrOffline) {
			t.Errorf("CheckDial(%s,%s) = %v, want ErrOffline", c.network, c.addr, err)
		}
	}
	for _, c := range allowed {
		if err := netpolicy.CheckDial(off, c.network, c.addr); err != nil {
			t.Errorf("CheckDial(%s,%s) = %v, want nil", c.network, c.addr, err)
		}
	}
	// An untagged context permits everything.
	for _, c := range blocked {
		if err := netpolicy.CheckDial(t.Context(), c.network, c.addr); err != nil {
			t.Errorf("online CheckDial(%s,%s) = %v, want nil", c.network, c.addr, err)
		}
	}
}
