package telemetry

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"hop.top/kit/go/core/netpolicy"
	"hop.top/kit/go/runtime/bus"
)

// stubTransport records requests and answers 200 without touching the
// network. It stands in for the real base transport so the test can use
// a NON-LOOPBACK URL: loopback is exempt from the offline guard, so a
// httptest server (127.0.0.1) would pass whether or not the telemetry
// carve-out exists, and could never fail.
type stubTransport struct{ hits int64 }

func (s *stubTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	atomic.AddInt64(&s.hits, 1)
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     make(http.Header),
		Request:    r,
	}, nil
}

// installGuardedStub makes http.DefaultTransport a guarded stub, exactly
// the shape netpolicy.Install produces in a real process.
func installGuardedStub(t *testing.T) *stubTransport {
	t.Helper()
	orig := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = orig })
	stub := &stubTransport{}
	http.DefaultTransport = netpolicy.Guard(stub)
	return stub
}

// Telemetry is logging-class egress: --offline stops traffic the user
// asked for, it is not a second consent gate on diagnostics. Consent and
// mode already decide whether anything is emitted at all.
//
// The destination is deliberately remote (not loopback) so the offline
// guard would refuse it if the sink had not opted out.
func TestHTTPSSinkEmitsToRemoteWhileOffline(t *testing.T) {
	stub := installGuardedStub(t)

	// No WithHTTPClient: exercise the default construction path, the one
	// that carries the observability carve-out.
	s, err := NewHTTPSSink("https://telemetry.example.com/v1/events",
		WithSpoolDir(t.TempDir()),
		WithBatchSize(1000),
		WithFlushInterval(0),
		WithMaxRetries(1),
	)
	if err != nil {
		t.Fatalf("NewHTTPSSink: %v", err)
	}

	ctx := netpolicy.WithOffline(context.Background(), true)
	if err := s.Drain(ctx, sampleEvent(1)); err != nil {
		t.Fatalf("drain: %v", err)
	}
	// CloseCtx ships the remaining ring under the caller's context.
	if err := s.CloseCtx(ctx); err != nil {
		t.Fatalf("close while offline: %v", err)
	}

	if n := atomic.LoadInt64(&stub.hits); n == 0 {
		t.Fatal("telemetry to a remote endpoint was suppressed by --offline: " +
			"logging-class egress must stay exempt")
	}
}

// The carve-out must stay narrow: a client built the ordinary way still
// refuses remote traffic, so exempting telemetry does not quietly exempt
// anything the user asked for.
func TestOrdinaryClientStillRefusedWhileOffline(t *testing.T) {
	installGuardedStub(t)

	c := &http.Client{} // nil Transport -> guarded DefaultTransport
	req, err := http.NewRequestWithContext(
		netpolicy.WithOffline(context.Background(), true),
		http.MethodGet, "https://telemetry.example.com/v1/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if _, err := c.Do(req); err == nil {
		t.Fatal("user-initiated remote request was allowed while offline")
	}
}

var _ = bus.Event{}
