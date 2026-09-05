package etcd_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"hop.top/kit/go/core/netpolicy"
	"hop.top/kit/go/storage/kv/etcd"
)

// openTimeout bounds every open below. A regression that fails to refuse
// must fail an assertion, never hang the battery.
const openTimeout = 5 * time.Second

// etcdListener starts a TCP listener that accepts and immediately closes.
// Reaching it proves the connect was NOT blocked. No etcd handshake ever
// completes against it, which is fine: these tests assert on which error
// comes back, not on a working session.
func etcdListener(t *testing.T) (port int, reached func() bool) {
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
		case <-time.After(500 * time.Millisecond):
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

// A remote endpoint on the live listener's port: the policy must refuse on
// the address alone, so a blocked connect cannot be mistaken for one that
// merely failed to resolve.
func TestNewContext_OfflineRefusesRemoteEndpoint(t *testing.T) {
	port, reached := etcdListener(t)
	ep := fmt.Sprintf("kit-offline-probe.invalid:%d", port)

	_, err := etcd.NewContext(offlineOpenCtx(t), []string{ep}, "app/")
	if !errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("offline open not refused: %v", err)
	}
	if reached() {
		t.Fatal("etcd open reached a listener despite offline context")
	}
}

// The refusal must be decided before the client exists. clientv3.New is
// non-blocking and gRPC dials on its own background context, so a check
// deferred to the dial hook would never see the marker: this asserts the
// endpoint check happens at open time, where it can still refuse.
func TestNewContext_OfflineRefusesBeforeAnyConnection(t *testing.T) {
	port, reached := etcdListener(t)
	ep := fmt.Sprintf("127.0.0.1:%d", port)

	// A loopback endpoint is reachable, so any connection attempt shows up
	// on the listener. Pair it with a remote one: the refusal must land
	// without the client ever being built, so nothing is dialed at all.
	remote := fmt.Sprintf("kit-offline-probe.invalid:%d", port)
	_, err := etcd.NewContext(offlineOpenCtx(t), []string{remote, ep}, "app/")
	if !errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("offline open not refused: %v", err)
	}
	if reached() {
		t.Fatal("a connection was attempted despite the policy refusing the open")
	}
}

// Scheme-bearing endpoints are an accepted etcd form and must be reduced to
// the address a dial would use, or a URL-shaped endpoint slips the check.
func TestNewContext_OfflineRefusesSchemedEndpoints(t *testing.T) {
	port, _ := etcdListener(t)
	for _, ep := range []string{
		fmt.Sprintf("http://kit-offline-probe.invalid:%d", port),
		fmt.Sprintf("https://kit-offline-probe.invalid:%d", port),
		fmt.Sprintf("http://kit-offline-probe.invalid:%d/path", port),
	} {
		t.Run(ep, func(t *testing.T) {
			if _, err := etcd.NewContext(offlineOpenCtx(t), []string{ep}, ""); !errors.Is(err, netpolicy.ErrOffline) {
				t.Fatalf("endpoint slipped the policy check: %v", err)
			}
		})
	}
}

// Loopback stays reachable while offline: a local etcd must keep working.
func TestNewContext_OfflineAllowsLoopback(t *testing.T) {
	port, _ := etcdListener(t)
	ep := fmt.Sprintf("127.0.0.1:%d", port)

	store, err := etcd.NewContext(offlineOpenCtx(t), []string{ep}, "app/")
	if errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("loopback open refused while offline: %v", err)
	}
	if err != nil {
		t.Fatalf("loopback open: %v", err)
	}
	_ = store.Close()
}

// The mirror of the case above, and the one that actually needs the scheme
// reduction: a scheme-bearing LOOPBACK endpoint must still be allowed.
// netpolicy.isLoopback parses a host:port, so it reads "http://127.0.0.1:2379"
// as a DNS name and calls it remote — reducing the endpoint to its authority
// first is what keeps a local etcd reachable on an offline run.
func TestNewContext_OfflineAllowsSchemedLoopback(t *testing.T) {
	port, _ := etcdListener(t)
	for _, ep := range []string{
		fmt.Sprintf("http://127.0.0.1:%d", port),
		fmt.Sprintf("https://127.0.0.1:%d", port),
		fmt.Sprintf("http://localhost:%d", port),
		fmt.Sprintf("http://127.0.0.1:%d/path", port),
	} {
		t.Run(ep, func(t *testing.T) {
			store, err := etcd.NewContext(offlineOpenCtx(t), []string{ep}, "")
			if errors.Is(err, netpolicy.ErrOffline) {
				t.Fatalf("schemed loopback endpoint refused while offline: %v", err)
			}
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			_ = store.Close()
		})
	}
}

// Unix sockets are filesystem objects, not the network, and must stay
// reachable while offline.
func TestNewContext_OfflineAllowsUnixSocket(t *testing.T) {
	for _, ep := range []string{"unix:///tmp/kit-etcd.sock", "unixs://localhost:2379"} {
		t.Run(ep, func(t *testing.T) {
			store, err := etcd.NewContext(offlineOpenCtx(t), []string{ep}, "")
			if errors.Is(err, netpolicy.ErrOffline) {
				t.Fatalf("unix socket refused while offline: %v", err)
			}
			if err != nil {
				t.Fatalf("unix open: %v", err)
			}
			_ = store.Close()
		})
	}
}

// An untagged context must be entirely unaffected by the guard.
func TestNewContext_OnlineAcceptsRemoteEndpoint(t *testing.T) {
	port, _ := etcdListener(t)
	ep := fmt.Sprintf("kit-offline-probe.invalid:%d", port)

	ctx, cancel := context.WithTimeout(t.Context(), openTimeout)
	defer cancel()
	store, err := etcd.NewContext(ctx, []string{ep}, "app/")
	if errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("untagged context was refused: %v", err)
	}
	if err != nil {
		t.Fatalf("online open: %v", err)
	}
	_ = store.Close()
}

// The context-free New cannot police anything, and must not pretend to.
func TestNew_ContextFreeDoesNotReportPolicyRefusals(t *testing.T) {
	port, _ := etcdListener(t)
	ep := fmt.Sprintf("kit-offline-probe.invalid:%d", port)

	store, err := etcd.New([]string{ep}, "app/")
	if errors.Is(err, netpolicy.ErrOffline) {
		t.Fatal("context-free New reported a policy refusal it cannot know about")
	}
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = store.Close()
}
