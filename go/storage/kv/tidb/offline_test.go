package tidb_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"hop.top/kit/go/core/netpolicy"
	"hop.top/kit/go/storage/kv/tidb"
)

// openTimeout bounds every open below. A regression that fails to block
// must fail an assertion, never hang the battery.
const openTimeout = 5 * time.Second

// tidbListener starts a TCP listener that accepts and immediately closes.
// Reaching it proves the dial was NOT blocked. A real MySQL handshake
// never completes against it, which is fine: these tests assert on which
// error comes back, not on a working session.
func tidbListener(t *testing.T) (port int, reached func() bool) {
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
	return ln.Addr().(*net.TCPAddr).Port, func() bool {
		select {
		case <-got:
			return true
		case <-time.After(200 * time.Millisecond):
			return false
		}
	}
}

func offlineOpenCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(netpolicy.WithOffline(t.Context(), true), openTimeout)
	t.Cleanup(cancel)
	return ctx
}

// A remote DSN on the live listener's port: the policy must refuse on the
// address alone, so a blocked dial cannot be mistaken for one that merely
// failed to resolve.
func TestNewContext_OfflineRefusesRemoteDSN(t *testing.T) {
	port, reached := tidbListener(t)
	dsn := fmt.Sprintf("kit:pw@tcp(kit-offline-probe.invalid:%d)/kit", port)

	_, err := tidb.NewContext(offlineOpenCtx(t), dsn, "kv")
	if !errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("offline open not refused: %v", err)
	}
	if reached() {
		t.Fatal("tidb open reached a listener despite offline context")
	}
}

// sql.Open is lazy, so the refusal must survive to first use rather than
// being decided (and lost) at open time. NewContext pings, which is that
// first use — assert the marker actually traveled down the driver's
// dial hook rather than being checked eagerly by our own code.
func TestNewContext_RefusalComesFromDriverDialHook(t *testing.T) {
	port, _ := tidbListener(t)
	dsn := fmt.Sprintf("kit:pw@tcp(kit-offline-probe.invalid:%d)/kit", port)

	_, err := tidb.NewContext(offlineOpenCtx(t), dsn, "kv")
	if !errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("want ErrOffline, got %v", err)
	}
	// The driver reports its dial errors through the ping; the policy
	// error must have propagated through that path intact.
	if got := err.Error(); !contains(got, "ping") {
		t.Errorf("refusal did not surface through the lazy ping path: %q", got)
	}
}

// Loopback stays reachable while offline: a local TiDB must keep working.
// The listener closes immediately so no handshake completes; what matters
// is that the error is NOT the offline refusal.
func TestNewContext_OfflineAllowsLoopback(t *testing.T) {
	port, reached := tidbListener(t)
	dsn := fmt.Sprintf("kit:pw@tcp(127.0.0.1:%d)/kit", port)

	_, err := tidb.NewContext(offlineOpenCtx(t), dsn, "kv")
	if errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("loopback open refused while offline: %v", err)
	}
	if !reached() {
		t.Fatal("loopback open never reached the listener")
	}
}

// An untagged context must be entirely unaffected by the guard.
func TestNewContext_OnlineDialsRemoteNormally(t *testing.T) {
	port, reached := tidbListener(t)
	dsn := fmt.Sprintf("kit:pw@tcp(localhost:%d)/kit", port)

	ctx, cancel := context.WithTimeout(t.Context(), openTimeout)
	defer cancel()
	_, err := tidb.NewContext(ctx, dsn, "kv")
	if errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("untagged context was refused: %v", err)
	}
	if !reached() {
		t.Fatal("online open never reached the listener")
	}
}

// A malformed DSN must be reported as such, not mistaken for a policy
// refusal: NewContext now parses the DSN where sql.Open used to.
func TestNewContext_RejectsMalformedDSN(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), openTimeout)
	defer cancel()
	if _, err := tidb.NewContext(ctx, "@@@not-a-dsn@@@", "kv"); err == nil {
		t.Fatal("malformed DSN accepted")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// The context-free New cannot police its own open-time ping: it has no
// context to read the marker from. The guarded dial hook is installed
// regardless, so a Store reached through that path still refuses on a
// later query with an offline context.
//
// kv.Open no longer reaches this path with a caller's policy — the driver
// registers a context-carrying opener, so kv.OpenContext refuses the
// connect outright — but New stays for callers holding no context, and
// must not report a refusal it cannot know about.
func TestNew_ContextFreeOpenStillGuardsLaterQueries(t *testing.T) {
	port, reached := tidbListener(t)
	// Loopback so the open-time ping is permitted to proceed; it fails
	// on the protocol (the listener hangs up), which is enough to hand
	// back a Store-shaped error path. Use the DSN directly instead.
	dsn := fmt.Sprintf("kit:pw@tcp(kit-offline-probe.invalid:%d)/kit", port)

	// New with a background context: the open-time ping is unguarded and
	// fails on DNS, exactly as documented.
	if _, err := tidb.New(dsn, "kv"); errors.Is(err, netpolicy.ErrOffline) {
		t.Fatal("context-free New must not report a policy refusal it cannot know about")
	}
	if reached() {
		t.Fatal("open reached the listener")
	}
}
