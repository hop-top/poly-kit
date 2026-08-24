package netpolicy_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"hop.top/kit/go/core/netpolicy"
)

// installGuarded guards http.DefaultTransport for the duration of the test.
func installGuarded(t *testing.T) {
	t.Helper()
	orig := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = orig })
	netpolicy.Install()
}

// Logging-class egress is exempt: --offline must not mute diagnostics.
func TestObservabilityTransportEmitsWhileOffline(t *testing.T) {
	installGuarded(t)

	var got int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got++
	}))
	t.Cleanup(srv.Close)

	c := &http.Client{Transport: netpolicy.ObservabilityTransport(nil)}
	req, err := http.NewRequestWithContext(
		netpolicy.WithOffline(context.Background(), true), http.MethodPost, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("observability egress refused while offline: %v", err)
	}
	resp.Body.Close()
	if got != 1 {
		t.Fatalf("sink never reached: got %d requests, want 1", got)
	}
}

// The carve-out is narrow: a default client stays refused, so exempting
// telemetry does not silently exempt user-initiated traffic.
func TestDefaultClientStillRefusedWhileOffline(t *testing.T) {
	installGuarded(t)

	c := &http.Client{} // nil Transport -> guarded DefaultTransport
	req, err := http.NewRequestWithContext(
		netpolicy.WithOffline(context.Background(), true), http.MethodGet, "https://example.com/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if _, err := c.Do(req); !errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("want ErrOffline for user traffic, got %v", err)
	}
}

// Stripping is idempotent and does not re-wrap.
func TestObservabilityTransportStripsGuard(t *testing.T) {
	guarded := netpolicy.Guard(http.DefaultTransport)
	if netpolicy.ObservabilityTransport(guarded) == guarded {
		t.Fatal("guard not stripped: observability client would still refuse")
	}
}
