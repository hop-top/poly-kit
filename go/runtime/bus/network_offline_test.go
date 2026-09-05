package bus

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"hop.top/kit/go/core/netpolicy"
)

// connectTimeout bounds every Connect below. A regression that fails to
// block must fail an assertion, never hang the battery.
const connectTimeout = 5 * time.Second

// busListener starts a TCP listener that accepts and immediately closes.
// Reaching it proves the handshake was NOT blocked.
func busListener(t *testing.T) (port int, reached func() bool) {
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

func offlineConnectCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(netpolicy.WithOffline(t.Context(), true), connectTimeout)
	t.Cleanup(cancel)
	return ctx
}

// A remote peer on the live listener's port must be refused on the
// address alone, without touching the wire.
func TestNetworkAdapter_OfflineRefusesRemotePeer(t *testing.T) {
	port, reached := busListener(t)
	n := NewNetworkAdapter(New())
	t.Cleanup(func() { _ = n.Close() })

	addr := fmt.Sprintf("ws://kit-offline-probe.invalid:%d/bus", port)
	if err := n.Connect(offlineConnectCtx(t), addr); !errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("offline Connect not refused: %v", err)
	}
	if reached() {
		t.Fatal("Connect reached a listener despite offline context")
	}
}

// Enforcement must not ride on netpolicy.Install having run.
func TestNetworkAdapter_RefusalIndependentOfDefaultTransport(t *testing.T) {
	orig := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = orig })
	http.DefaultTransport = &http.Transport{} // explicitly UNguarded

	port, reached := busListener(t)
	n := NewNetworkAdapter(New())
	t.Cleanup(func() { _ = n.Close() })

	addr := fmt.Sprintf("ws://kit-offline-probe.invalid:%d/bus", port)
	if err := n.Connect(offlineConnectCtx(t), addr); !errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("Connect relied on a guarded DefaultTransport: %v", err)
	}
	if reached() {
		t.Fatal("Connect reached a listener with DefaultTransport unguarded")
	}
}

// Loopback peers stay reachable while offline.
func TestNetworkAdapter_OfflineAllowsLoopbackPeer(t *testing.T) {
	port, reached := busListener(t)
	n := NewNetworkAdapter(New())
	t.Cleanup(func() { _ = n.Close() })

	addr := fmt.Sprintf("ws://127.0.0.1:%d/bus", port)
	if err := n.Connect(offlineConnectCtx(t), addr); errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("loopback peer refused while offline: %v", err)
	}
	if !reached() {
		t.Fatal("loopback Connect never reached the listener")
	}
}

// The reconnect loop outlives the caller's context and starts from its
// own root. That root must still carry the offline marker, or a dropped
// peer would be silently re-dialed on a run that asked for no network.
func TestNetworkAdapter_ReconnectInheritsOfflinePolicy(t *testing.T) {
	port, reached := busListener(t)
	n := NewNetworkAdapter(New())
	t.Cleanup(func() { _ = n.Close() })

	addr := fmt.Sprintf("ws://kit-offline-probe.invalid:%d/bus", port)
	// One offline Connect is what the adapter learns the policy from.
	if err := n.Connect(offlineConnectCtx(t), addr); !errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("offline Connect not refused: %v", err)
	}

	// The reconnect root must be tagged, so a re-dial refuses too.
	if !netpolicy.IsOffline(n.baseCtx()) {
		t.Fatal("reconnect root dropped the offline marker")
	}
	if err := n.Connect(n.baseCtx(), addr); !errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("reconnect-context Connect not refused: %v", err)
	}
	if reached() {
		t.Fatal("reconnect reached a listener despite offline policy")
	}
}

// An adapter never used offline must not tag its reconnect root: the
// policy is inherited, never invented.
func TestNetworkAdapter_OnlineReconnectRootStaysClean(t *testing.T) {
	n := NewNetworkAdapter(New())
	t.Cleanup(func() { _ = n.Close() })

	if netpolicy.IsOffline(n.baseCtx()) {
		t.Fatal("reconnect root tagged offline without an offline Connect")
	}
}
