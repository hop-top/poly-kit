package client_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"hop.top/kit/go/core/netpolicy"
	"hop.top/kit/go/transport/api/client"
)

// wsDialTimeout bounds every dial below. A regression that fails to
// block must fail an assertion, never hang the battery.
const wsDialTimeout = 5 * time.Second

// wsListener starts a TCP listener that accepts and immediately closes.
// Reaching it proves the handshake was NOT blocked.
func wsListener(t *testing.T) (port int, reached func() bool) {
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

func offlineDialCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(netpolicy.WithOffline(t.Context(), true), wsDialTimeout)
	t.Cleanup(cancel)
	return ctx
}

// A remote host on the live listener's port: the policy must refuse on
// the address alone, so a blocked handshake cannot be confused with one
// that merely failed to resolve.
func TestDialWS_OfflineRefusesRemote(t *testing.T) {
	port, reached := wsListener(t)
	url := "ws://kit-offline-probe.invalid:" + itoa(port) + "/ws"

	if _, err := client.DialWS(offlineDialCtx(t), url); !errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("offline DialWS not refused: %v", err)
	}
	if reached() {
		t.Fatal("DialWS reached a listener despite offline context")
	}
}

// Enforcement must not depend on netpolicy.Install having run, nor on
// http.DefaultTransport still being the guarded one. Swap the global to
// an unguarded transport and the refusal must stand.
func TestDialWS_RefusalIndependentOfDefaultTransport(t *testing.T) {
	orig := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = orig })
	http.DefaultTransport = &http.Transport{} // explicitly UNguarded

	port, reached := wsListener(t)
	url := "ws://kit-offline-probe.invalid:" + itoa(port) + "/ws"

	if _, err := client.DialWS(offlineDialCtx(t), url); !errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("DialWS relied on a guarded DefaultTransport: %v", err)
	}
	if reached() {
		t.Fatal("DialWS reached a listener with DefaultTransport unguarded")
	}
}

// Loopback stays reachable while offline: a local kit serve peer must
// keep working. The listener hangs up, so the handshake fails — what
// matters is that the error is NOT the offline refusal.
func TestDialWS_OfflineAllowsLoopback(t *testing.T) {
	port, reached := wsListener(t)
	url := "ws://127.0.0.1:" + itoa(port) + "/ws"

	if _, err := client.DialWS(offlineDialCtx(t), url); errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("loopback DialWS refused while offline: %v", err)
	}
	if !reached() {
		t.Fatal("loopback DialWS never reached the listener")
	}
}

// An untagged context must be entirely unaffected.
func TestDialWS_OnlineDialsRemoteNormally(t *testing.T) {
	port, reached := wsListener(t)
	url := "ws://localhost:" + itoa(port) + "/ws"

	ctx, cancel := context.WithTimeout(t.Context(), wsDialTimeout)
	defer cancel()
	if _, err := client.DialWS(ctx, url); errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("untagged context was refused: %v", err)
	}
	if !reached() {
		t.Fatal("online DialWS never reached the listener")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
