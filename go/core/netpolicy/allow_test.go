package netpolicy_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"hop.top/kit/go/core/netpolicy"
)

func TestWithAllowHosts_EmptyLeavesContextClean(t *testing.T) {
	ctx := t.Context()
	if got := netpolicy.WithAllowHosts(ctx, nil); got != ctx {
		t.Error("nil list changed the context")
	}
	if got := netpolicy.WithAllowHosts(ctx, []string{}); got != ctx {
		t.Error("empty list changed the context")
	}
	if netpolicy.AllowHosts(ctx) != nil {
		t.Error("untagged context reports a list")
	}
}

func TestAllowHosts_ReturnsCopy(t *testing.T) {
	in := []string{"a.example.com"}
	ctx := netpolicy.WithAllowHosts(t.Context(), in)
	in[0] = "evil.example.com"
	got := netpolicy.AllowHosts(ctx)
	got[0] = "evil.example.com"
	if netpolicy.AllowHosts(ctx)[0] != "a.example.com" {
		t.Error("list aliased caller storage")
	}
}

func TestValidateAllowHosts(t *testing.T) {
	if err := netpolicy.ValidateAllowHosts([]string{"a.example.com", "*.b.example.com", "203.0.113.5", "[2001:db8::1]"}); err != nil {
		t.Errorf("valid list: %v", err)
	}
	for _, bad := range []string{"", "*", "*.", "a*.example.com", "*.*.example.com", "example.com/x", "example.com:443", "*.203.0.113.5", " "} {
		if err := netpolicy.ValidateAllowHosts([]string{"ok.example.com", bad}); err == nil {
			t.Errorf("entry %q accepted", bad)
		}
	}
}

// Offline wins over the allowlist: an allowed host is still refused
// offline, with the offline error.
func TestAllowHosts_OfflineStillWins(t *testing.T) {
	ctx := netpolicy.WithOffline(netpolicy.WithAllowHosts(t.Context(), []string{"a.example.com"}), true)
	err := netpolicy.CheckDial(ctx, "tcp", "a.example.com:443")
	if !errors.Is(err, netpolicy.ErrOffline) || errors.Is(err, netpolicy.ErrHostNotAllowed) {
		t.Fatalf("err = %v, want ErrOffline only", err)
	}
	if err := netpolicy.CheckDial(ctx, "tcp", "127.0.0.1:1"); err != nil {
		t.Errorf("loopback offline: %v", err)
	}
}

func TestAllowHosts_DoesNotAffectUnlistedContexts(t *testing.T) {
	if err := netpolicy.CheckDial(t.Context(), "tcp", "evil.example.com:1"); err != nil {
		t.Errorf("no list: %v", err)
	}
}

type auditLog struct {
	mu  sync.Mutex
	got []netpolicy.Refusal
}

func (a *auditLog) add(r netpolicy.Refusal) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.got = append(a.got, r)
}

func TestAudit_RecordsEveryRefusal(t *testing.T) {
	log := &auditLog{}
	ctx := netpolicy.WithAudit(netpolicy.WithAllowHosts(t.Context(), []string{"ok.example.com"}), log.add)

	_ = netpolicy.CheckDial(ctx, "tcp", "evil.example.com:443")
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://u:p@evil.example.com/v1/x?key=k", nil)
	_, _ = netpolicy.Guard(&tripRecorder{}).RoundTrip(req)
	_ = netpolicy.CheckDial(ctx, "tcp", "ok.example.com:443") // permitted: no record
	off := netpolicy.WithOffline(ctx, true)
	_ = netpolicy.CheckDial(off, "tcp", "ok.example.com:443")

	want := []netpolicy.Refusal{
		{Reason: netpolicy.ErrHostNotAllowed, Op: "dial tcp", Dest: "evil.example.com:443"},
		{Reason: netpolicy.ErrHostNotAllowed, Op: "POST", Dest: "https://evil.example.com/v1/x"},
		{Reason: netpolicy.ErrOffline, Op: "dial tcp", Dest: "ok.example.com:443"},
	}
	if len(log.got) != len(want) {
		t.Fatalf("records = %+v, want %+v", log.got, want)
	}
	for i := range want {
		if log.got[i] != want[i] {
			t.Errorf("record %d = %+v, want %+v", i, log.got[i], want[i])
		}
	}
}

func TestAudit_NilContextAndNoSinkAreSafe(t *testing.T) {
	var ctx context.Context
	if netpolicy.HostAllowed(ctx, "x.example.com") != true {
		t.Error("nil ctx should not restrict")
	}
	ctx = netpolicy.WithAllowHosts(t.Context(), []string{"a.example.com"})
	if err := netpolicy.CheckDial(ctx, "tcp", "b.example.com:1"); !errors.Is(err, netpolicy.ErrHostNotAllowed) {
		t.Errorf("err = %v", err)
	}
}
