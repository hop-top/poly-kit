package bus

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hop.top/kit/go/core/netpolicy"
)

// A handshake answered with a redirect to another origin must be
// refused: the handshake carries Sec-WebSocket-* headers, which may not
// leave their origin. Nothing reaches the second origin.
func TestNetworkAdapter_RefusesCrossOriginRedirect(t *testing.T) {
	var hits atomic.Int32
	b := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer b.Close()
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, b.URL, http.StatusFound)
	}))
	defer a.Close()

	ctx, cancel := context.WithTimeout(t.Context(), connectTimeout)
	defer cancel()
	n := NewNetworkAdapter(New())
	t.Cleanup(func() { _ = n.Close() })
	err := n.Connect(ctx, "ws"+strings.TrimPrefix(a.URL, "http"))
	if !errors.Is(err, netpolicy.ErrCrossOriginHeaders) {
		t.Fatalf("err = %v, want ErrCrossOriginHeaders", err)
	}
	time.Sleep(100 * time.Millisecond)
	if n := hits.Load(); n != 0 {
		t.Errorf("second origin received %d requests, want 0", n)
	}
}
