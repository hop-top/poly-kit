package etcd_test

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"hop.top/kit/go/core/netpolicy"
	"hop.top/kit/go/storage/kv"
	"hop.top/kit/go/storage/kv/etcd"
)

// openBounded runs open on its own goroutine and fails the test if it
// outlives openTimeout: a regression must fail an assertion, never hang.
func openBounded(t *testing.T, open func() (kv.Store, error)) (kv.Store, error) {
	t.Helper()
	type result struct {
		store kv.Store
		err   error
	}
	done := make(chan result, 1)
	go func() {
		s, err := open()
		done <- result{s, err}
	}()
	select {
	case r := <-done:
		return r.store, r.err
	case <-time.After(openTimeout):
		t.Fatal("open outlived its context: construction is not bounded by ctx")
		return nil, nil
	}
}

func assertNoSecret(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), "kit-secret") {
		t.Fatalf("error leaks the password: %v", err)
	}
}

// With credentials the client authenticates before clientv3.New returns,
// a blocking RPC on the client's own context. The open must be bounded by
// the caller's ctx instead of waiting forever on a server that never
// answers — and reaching the listener at all proves the credentials made
// it into the client, which does not authenticate without them.
func TestNewContext_CredentialsAuthenticateAtOpen(t *testing.T) {
	port, reached := etcdListener(t)
	ep := fmt.Sprintf("127.0.0.1:%d", port)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	store, err := openBounded(t, func() (kv.Store, error) {
		return etcd.NewContext(ctx, []string{ep}, "", etcd.WithAuth("kit-user", "kit-secret"))
	})
	if err == nil {
		_ = store.Close()
		t.Fatal("authenticated open succeeded against a server that never answers")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("open error is not the ctx deadline: %v", err)
	}
	assertNoSecret(t, err)
	if !reached() {
		t.Fatal("no authentication attempt reached the server")
	}
}

// The same through kv.OpenContext: the Config fields reach the client.
func TestOpenContext_CredentialsReachClient(t *testing.T) {
	port, reached := etcdListener(t)
	ep := fmt.Sprintf("127.0.0.1:%d", port)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	store, err := openBounded(t, func() (kv.Store, error) {
		return kv.OpenContext(ctx, kv.Config{
			Backend:   "etcd",
			Endpoints: []string{ep},
			Username:  "kit-user",
			Password:  "kit-secret",
		})
	})
	if err == nil {
		_ = store.Close()
		t.Fatal("kv.Config credentials never reached the client: open did not authenticate")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("open error is not the ctx deadline: %v", err)
	}
	assertNoSecret(t, err)
	if !reached() {
		t.Fatal("no authentication attempt reached the server")
	}
}

// A context already done fails the open rather than handing back a
// client whose lifetime context is canceled.
func TestNewContext_CanceledContext(t *testing.T) {
	port, _ := etcdListener(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	store, err := openBounded(t, func() (kv.Store, error) {
		return etcd.NewContext(ctx, []string{fmt.Sprintf("127.0.0.1:%d", port)}, "")
	})
	if err == nil {
		_ = store.Close()
		t.Fatal("open succeeded on a canceled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("open error is not the ctx cancellation: %v", err)
	}
}

// Credentials do not change the offline decision: a remote endpoint is
// refused before any client exists, and the refusal carries no secret.
func TestNewContext_OfflineRefusesCredentialedRemote(t *testing.T) {
	port, reached := etcdListener(t)
	ep := fmt.Sprintf("kit-offline-probe.invalid:%d", port)

	_, err := etcd.NewContext(offlineOpenCtx(t), []string{ep}, "", etcd.WithAuth("kit-user", "kit-secret"))
	if !errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("offline open not refused: %v", err)
	}
	assertNoSecret(t, err)
	if strings.Contains(err.Error(), "kit-user") {
		t.Fatalf("refusal names the user: %v", err)
	}
	if reached() {
		t.Fatal("etcd open reached a listener despite offline context")
	}
}

// A lone Username or Password is ignored by the client; through
// kv.OpenContext it is rejected instead, without echoing it.
func TestOpenContext_RejectsLoneCredential(t *testing.T) {
	port, reached := etcdListener(t)
	ep := fmt.Sprintf("127.0.0.1:%d", port)
	for name, cfg := range map[string]kv.Config{
		"username": {Backend: "etcd", Endpoints: []string{ep}, Username: "kit-user"},
		"password": {Backend: "etcd", Endpoints: []string{ep}, Password: "kit-secret"},
	} {
		t.Run(name, func(t *testing.T) {
			store, err := kv.OpenContext(t.Context(), cfg)
			if err == nil {
				_ = store.Close()
				t.Fatal("lone credential accepted")
			}
			assertNoSecret(t, err)
			if strings.Contains(err.Error(), "kit-user") {
				t.Fatalf("error names the user: %v", err)
			}
		})
	}
	if reached() {
		t.Fatal("a connection was attempted for a rejected config")
	}
}

// TLS with an http:// endpoint would be dropped silently by the client,
// and mixed transports follow the first endpoint's: both are rejected.
func TestNewContext_RejectsInconsistentTransport(t *testing.T) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	for name, tc := range map[string]struct {
		eps  []string
		opts []etcd.Option
	}{
		"tls with http":   {[]string{"http://127.0.0.1:2379"}, []etcd.Option{etcd.WithTLS(tlsCfg)}},
		"http then https": {[]string{"http://127.0.0.1:2379", "https://127.0.0.1:2380"}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			store, err := etcd.NewContext(t.Context(), tc.eps, "", tc.opts...)
			if err == nil {
				_ = store.Close()
				t.Fatal("inconsistent transport accepted")
			}
		})
	}
}
