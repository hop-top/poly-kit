package netpolicy_test

import (
	"errors"
	"net/http"
	"testing"

	"hop.top/kit/go/core/netpolicy"
)

// Install must make a plain http.Client (no explicit Transport) enforce
// the policy, and must be idempotent.
func TestInstall_GuardsDefaultTransport(t *testing.T) {
	orig := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = orig })

	netpolicy.Install()
	netpolicy.Install() // idempotent

	req, _ := http.NewRequestWithContext(
		netpolicy.WithOffline(t.Context(), true), http.MethodGet,
		"https://example.invalid/x", nil)

	if _, err := (&http.Client{}).Do(req); !errors.Is(err, netpolicy.ErrOffline) {
		t.Fatalf("plain client not guarded after Install: %v", err)
	}
}
