package etcd_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
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

// The context-free New goes through the same check.
func TestNew_RejectsSchemelessUserinfo(t *testing.T) {
	store, err := etcd.New([]string{"kit-user:kit-secret@127.0.0.1:2379"}, "")
	if err == nil {
		_ = store.Close()
		t.Fatal("schemeless endpoint with userinfo was accepted")
	}
	if strings.Contains(err.Error(), "kit-secret") {
		t.Fatalf("error leaks the password: %v", err)
	}
}

// Userinfo in any endpoint is rejected at open, on any context, before
// the client exists: the client dials url.Host and never sends it, so
// credentials there would be silently dropped. The error names the host
// alone and points at the supported credential fields.
func TestNewContext_RejectsEndpointUserinfo(t *testing.T) {
	port, reached := etcdListener(t)
	for _, tc := range []struct{ ep, host string }{
		{fmt.Sprintf("kit-user:kit-secret@kit-offline-probe.invalid:%d", port), fmt.Sprintf("kit-offline-probe.invalid:%d", port)},
		{fmt.Sprintf("kit-user@127.0.0.1:%d", port), fmt.Sprintf("127.0.0.1:%d", port)},
		{fmt.Sprintf("http://kit-user:kit-secret@kit-offline-probe.invalid:%d", port), fmt.Sprintf("kit-offline-probe.invalid:%d", port)},
		{fmt.Sprintf("https://kit-user@127.0.0.1:%d/v3?x=1", port), fmt.Sprintf("127.0.0.1:%d", port)},
		{fmt.Sprintf("HTTP://kit-user:kit-secret@[::1]:%d", port), fmt.Sprintf("[::1]:%d", port)},
	} {
		for name, ctx := range map[string]context.Context{
			"online":  t.Context(),
			"offline": offlineOpenCtx(t),
		} {
			t.Run(name+"/"+tc.ep, func(t *testing.T) {
				store, err := etcd.NewContext(ctx, []string{tc.ep}, "")
				if err == nil {
					_ = store.Close()
					t.Fatal("endpoint with userinfo was accepted")
				}
				if errors.Is(err, netpolicy.ErrOffline) {
					t.Fatalf("rejected by the policy, not the endpoint check: %v", err)
				}
				msg := err.Error()
				for _, secret := range []string{"kit-user", "kit-secret", "@"} {
					if strings.Contains(msg, secret) {
						t.Fatalf("error leaks userinfo %q: %s", secret, msg)
					}
				}
				if !strings.Contains(msg, tc.host) {
					t.Fatalf("error does not name the endpoint host %q: %s", tc.host, msg)
				}
				if !strings.Contains(msg, "Username") {
					t.Fatalf("error does not point at the credential fields: %s", msg)
				}
			})
		}
	}
	if reached() {
		t.Fatal("a connection was attempted for a rejected endpoint")
	}
}

// An "@" past the authority is path, query or fragment, not userinfo.
func TestNewContext_AcceptsAtSignOutsideAuthority(t *testing.T) {
	port, _ := etcdListener(t)
	for _, ep := range []string{
		fmt.Sprintf("http://127.0.0.1:%d/a@b", port),
		fmt.Sprintf("http://127.0.0.1:%d?q=a@b", port),
	} {
		t.Run(ep, func(t *testing.T) {
			store, err := etcd.NewContext(offlineOpenCtx(t), []string{ep}, "")
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

// An endpoint scheme the client does not interpret is dialed verbatim as
// a TCP address, which can never connect — and the client's own logger
// would print it, userinfo included. It is rejected at open on any
// context, before the client exists, without echoing the endpoint.
func TestNewContext_RejectsUnsupportedScheme(t *testing.T) {
	port, reached := etcdListener(t)
	for _, ep := range []string{
		fmt.Sprintf("grpc://kit-user:kit-secret@kit-offline-probe.invalid:%d", port),
		fmt.Sprintf("grpc://kit-user:kit-secret@127.0.0.1:%d", port),
		fmt.Sprintf("dns:///127.0.0.1:%d", port),
	} {
		for name, ctx := range map[string]context.Context{
			"online":  t.Context(),
			"offline": offlineOpenCtx(t),
		} {
			t.Run(name+"/"+ep, func(t *testing.T) {
				store, err := etcd.NewContext(ctx, []string{ep}, "")
				if err == nil {
					_ = store.Close()
					t.Fatal("endpoint with an unsupported scheme was accepted")
				}
				if errors.Is(err, netpolicy.ErrOffline) {
					t.Fatalf("rejected by the policy, not the endpoint check: %v", err)
				}
				for _, secret := range []string{"kit-user", "kit-secret", "@"} {
					if strings.Contains(err.Error(), secret) {
						t.Fatalf("error leaks %q: %v", secret, err)
					}
				}
			})
		}
	}
	if reached() {
		t.Fatal("a connection was attempted for a rejected endpoint")
	}
}

// The client folds the scheme to lower case, so "HTTP://127.0.0.1" dials
// loopback and must stay reachable offline, while "HTTP://" userinfo must
// never reach a refusal.
func TestNewContext_UppercaseScheme(t *testing.T) {
	port, reached := etcdListener(t)
	t.Run("loopback allowed offline", func(t *testing.T) {
		store, err := etcd.NewContext(offlineOpenCtx(t), []string{fmt.Sprintf("HTTP://127.0.0.1:%d", port)}, "")
		if err != nil {
			t.Fatalf("uppercase-scheme loopback refused offline: %v", err)
		}
		_ = store.Close()
	})
	t.Run("remote refused offline", func(t *testing.T) {
		_, err := etcd.NewContext(offlineOpenCtx(t), []string{fmt.Sprintf("HTTPS://kit-offline-probe.invalid:%d/v3", port)}, "")
		if !errors.Is(err, netpolicy.ErrOffline) {
			t.Fatalf("uppercase-scheme remote endpoint slipped the policy: %v", err)
		}
		if want := fmt.Sprintf("kit-offline-probe.invalid:%d", port); !strings.Contains(err.Error(), want) ||
			strings.Contains(err.Error(), "HTTPS") {
			t.Fatalf("refusal does not name the dial target %q alone: %v", want, err)
		}
		if reached() {
			t.Fatal("etcd open reached a listener despite offline context")
		}
	})
}
