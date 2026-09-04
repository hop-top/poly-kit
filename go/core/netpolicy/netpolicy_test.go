package netpolicy_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"hop.top/kit/go/core/netpolicy"
)

// tripRecorder records whether the wrapped transport was ever reached.
type tripRecorder struct{ reached bool }

func (r *tripRecorder) RoundTrip(*http.Request) (*http.Response, error) {
	r.reached = true
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
}

// A tagged context must stop the request before it reaches the wire.
// The destination is external: loopback is exempt by design, so a
// httptest server would legitimately be allowed through.
func TestRoundTripper_BlocksExternalWhenOffline(t *testing.T) {
	rec := &tripRecorder{}
	c := &http.Client{Transport: netpolicy.Guard(rec)}
	req, _ := http.NewRequestWithContext(
		netpolicy.WithOffline(t.Context(), true), http.MethodGet,
		"https://example.invalid/v1/thing", nil)

	_, err := c.Do(req)
	if err == nil {
		t.Fatal("expected offline error, got nil")
	}
	if !errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("expected ErrOffline, got %v", err)
	}
	if rec.reached {
		t.Fatal("request reached the transport despite offline context")
	}
}

// Untagged contexts must be entirely unaffected.
func TestRoundTripper_AllowsWhenNotOffline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := &http.Client{Transport: netpolicy.Guard(http.DefaultTransport)}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)

	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
}

// Loopback stays reachable when offline: --offline means "no network",
// not "cannot talk to myself".
func TestRoundTripper_AllowsLoopbackWhenOffline(t *testing.T) {
	for _, target := range []string{
		"http://127.0.0.1:8080/health",
		"http://localhost:9000/health",
		"http://[::1]:9000/health",
	} {
		rec := &tripRecorder{}
		c := &http.Client{Transport: netpolicy.Guard(rec)}
		req, _ := http.NewRequestWithContext(
			netpolicy.WithOffline(t.Context(), true), http.MethodGet, target, nil)

		resp, err := c.Do(req)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", target, err)
		}
		resp.Body.Close()
		if !rec.reached {
			t.Fatalf("%s: loopback request was blocked", target)
		}
	}
}

// A DNS name is remote even if it might resolve to loopback: resolving
// it is itself network access.
func TestRoundTripper_BlocksDNSNamesWhenOffline(t *testing.T) {
	rec := &tripRecorder{}
	c := &http.Client{Transport: netpolicy.Guard(rec)}
	req, _ := http.NewRequestWithContext(
		netpolicy.WithOffline(t.Context(), true), http.MethodGet,
		"http://my-host.internal/health", nil)

	if _, err := c.Do(req); !errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("expected ErrOffline, got %v", err)
	}
	if rec.reached {
		t.Fatal("DNS-named host was allowed through")
	}
}

// WithOffline(ctx,false) must not tag the context.
func TestWithOffline_FalseLeavesContextClean(t *testing.T) {
	if netpolicy.IsOffline(netpolicy.WithOffline(t.Context(), false)) {
		t.Fatal("WithOffline(false) tagged the context")
	}
	if netpolicy.IsOffline(nil) { //nolint:staticcheck // nil-safety is the assertion
		t.Fatal("nil context reported offline")
	}
}

// Guard must not double-wrap.
func TestGuard_Idempotent(t *testing.T) {
	once := netpolicy.Guard(http.DefaultTransport)
	if twice := netpolicy.Guard(once); twice != once {
		t.Fatal("Guard double-wrapped an already guarded transport")
	}
	if netpolicy.Guard(nil) == nil {
		t.Fatal("Guard(nil) returned nil")
	}
}
