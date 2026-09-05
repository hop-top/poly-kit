package kv_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"hop.top/kit/go/core/netpolicy"
	"hop.top/kit/go/storage/kv"

	_ "hop.top/kit/go/storage/kv/badger"
	_ "hop.top/kit/go/storage/kv/etcd"
	_ "hop.top/kit/go/storage/kv/sqlite"
	_ "hop.top/kit/go/storage/kv/tidb"
)

// openTimeout bounds every open below. A regression that fails to refuse
// must fail an assertion, never hang the battery.
const openTimeout = 5 * time.Second

// remoteHost is a name that must never resolve. Pairing it with a live
// local port means a refusal cannot be confused with a dial that merely
// failed to find a host: if the policy lets go, the listener is reached.
const remoteHost = "kit-offline-probe.invalid"

// probeListener starts a TCP listener that accepts and immediately closes.
// Reaching it proves the connect was NOT blocked.
func probeListener(t *testing.T) (port int, reached func() bool) {
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

func offlineCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(netpolicy.WithOffline(t.Context(), true), openTimeout)
	t.Cleanup(cancel)
	return ctx
}

func onlineCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), openTimeout)
	t.Cleanup(cancel)
	return ctx
}

// The point of the whole change: the INITIAL connect, made through the
// registry rather than through a driver constructor, is refused on an
// offline context. Before ContextOpener existed this could not be
// expressed — kv.Open had no context to refuse on.
func TestOpenContext_OfflineRefusesInitialConnect(t *testing.T) {
	t.Run("tidb", func(t *testing.T) {
		port, reached := probeListener(t)
		cfg := kv.Config{
			Backend: "tidb",
			DSN:     fmt.Sprintf("kit:pw@tcp(%s:%d)/kit", remoteHost, port),
		}
		_, err := kv.OpenContext(offlineCtx(t), cfg)
		if !errors.Is(err, netpolicy.ErrOffline) {
			t.Fatalf("initial connect not refused: %v", err)
		}
		if reached() {
			t.Fatal("open reached a listener despite offline context")
		}
	})

	t.Run("etcd", func(t *testing.T) {
		port, reached := probeListener(t)
		cfg := kv.Config{
			Backend:   "etcd",
			Endpoints: []string{fmt.Sprintf("%s:%d", remoteHost, port)},
		}
		_, err := kv.OpenContext(offlineCtx(t), cfg)
		if !errors.Is(err, netpolicy.ErrOffline) {
			t.Fatalf("initial connect not refused: %v", err)
		}
		if reached() {
			t.Fatal("open reached a listener despite offline context")
		}
	})
}

// etcd endpoints carry schemes. Each accepted form must be reduced to the
// address the dial would use, or a URL-shaped endpoint slips the check.
func TestOpenContext_OfflineRefusesSchemedEtcdEndpoints(t *testing.T) {
	port, _ := probeListener(t)
	for _, scheme := range []string{"http://", "https://"} {
		t.Run(scheme, func(t *testing.T) {
			cfg := kv.Config{
				Backend:   "etcd",
				Endpoints: []string{fmt.Sprintf("%s%s:%d", scheme, remoteHost, port)},
			}
			if _, err := kv.OpenContext(offlineCtx(t), cfg); !errors.Is(err, netpolicy.ErrOffline) {
				t.Fatalf("scheme %q slipped the policy check: %v", scheme, err)
			}
		})
	}
}

// One remote endpoint among several must still refuse: a cluster is only
// as offline-safe as its most remote member.
func TestOpenContext_OfflineRefusesAnyRemoteEndpoint(t *testing.T) {
	port, _ := probeListener(t)
	cfg := kv.Config{
		Backend: "etcd",
		Endpoints: []string{
			fmt.Sprintf("127.0.0.1:%d", port),
			fmt.Sprintf("%s:%d", remoteHost, port),
		},
	}
	if _, err := kv.OpenContext(offlineCtx(t), cfg); !errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("remote endpoint beside a loopback one was not refused: %v", err)
	}
}

// Loopback stays reachable while offline: --offline means "do not talk to
// the network", not "do not talk to myself". A local etcd or TiDB must
// keep working, so the refusal must not fire on 127.0.0.1.
func TestOpenContext_OfflineAllowsLoopback(t *testing.T) {
	t.Run("tidb", func(t *testing.T) {
		port, reached := probeListener(t)
		cfg := kv.Config{
			Backend: "tidb",
			DSN:     fmt.Sprintf("kit:pw@tcp(127.0.0.1:%d)/kit", port),
		}
		// The listener hangs up, so the open fails on the protocol. What
		// matters is that it is not the policy that stopped it.
		_, err := kv.OpenContext(offlineCtx(t), cfg)
		if errors.Is(err, netpolicy.ErrOffline) {
			t.Fatalf("loopback open refused while offline: %v", err)
		}
		if !reached() {
			t.Fatal("loopback open never reached the listener")
		}
	})

	t.Run("etcd", func(t *testing.T) {
		port, _ := probeListener(t)
		cfg := kv.Config{
			Backend:   "etcd",
			Endpoints: []string{fmt.Sprintf("127.0.0.1:%d", port)},
		}
		store, err := kv.OpenContext(offlineCtx(t), cfg)
		if errors.Is(err, netpolicy.ErrOffline) {
			t.Fatalf("loopback open refused while offline: %v", err)
		}
		if err != nil {
			t.Fatalf("loopback open: %v", err)
		}
		_ = store.Close()
	})
}

// The local drivers have no dial, so an offline context must not restrict
// them at all. A guard that refused these would break every offline run.
func TestOpenContext_OfflineAllowsLocalBackends(t *testing.T) {
	for _, tc := range []struct {
		backend string
		path    func(dir string) string
	}{
		{"sqlite", func(dir string) string { return filepath.Join(dir, "kv.db") }},
		{"badger", func(dir string) string { return filepath.Join(dir, "badger") }},
	} {
		t.Run(tc.backend, func(t *testing.T) {
			cfg := kv.Config{Backend: tc.backend, Path: tc.path(t.TempDir())}
			store, err := kv.OpenContext(offlineCtx(t), cfg)
			if err != nil {
				t.Fatalf("offline open of a local backend failed: %v", err)
			}
			defer store.Close()
			if err := store.Put(offlineCtx(t), "k", []byte("v")); err != nil {
				t.Fatalf("offline put: %v", err)
			}
		})
	}
}

// An untagged context must be entirely unaffected: the guard bites on the
// marker, not on the address.
func TestOpenContext_OnlineConnectsNormally(t *testing.T) {
	t.Run("tidb", func(t *testing.T) {
		port, reached := probeListener(t)
		cfg := kv.Config{
			Backend: "tidb",
			DSN:     fmt.Sprintf("kit:pw@tcp(localhost:%d)/kit", port),
		}
		if _, err := kv.OpenContext(onlineCtx(t), cfg); errors.Is(err, netpolicy.ErrOffline) {
			t.Fatalf("untagged context was refused: %v", err)
		}
		if !reached() {
			t.Fatal("online open never reached the listener")
		}
	})

	t.Run("etcd", func(t *testing.T) {
		port, _ := probeListener(t)
		cfg := kv.Config{
			Backend:   "etcd",
			Endpoints: []string{fmt.Sprintf("%s:%d", remoteHost, port)},
		}
		store, err := kv.OpenContext(onlineCtx(t), cfg)
		if errors.Is(err, netpolicy.ErrOffline) {
			t.Fatalf("untagged context was refused: %v", err)
		}
		if err != nil {
			t.Fatalf("online open: %v", err)
		}
		_ = store.Close()
	})
}

// kv.Open keeps its signature and its behavior: no context, so no policy.
// It must not start reporting refusals it cannot know about.
func TestOpen_ContextFreeStillWorks(t *testing.T) {
	cfg := kv.Config{Backend: "sqlite", Path: filepath.Join(t.TempDir(), "kv.db")}
	store, err := kv.Open(cfg)
	if err != nil {
		t.Fatalf("kv.Open: %v", err)
	}
	defer store.Close()
	if err := store.Put(t.Context(), "k", []byte("v")); err != nil {
		t.Fatalf("put: %v", err)
	}
}
