// Test-only re-exports keep production identifiers unexported while still
// letting llm_test exercise them.

package llm

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"testing"
)

// TracingEnabled exposes [tracingEnabled] to the external test package.
var TracingEnabled = tracingEnabled

// CountConfigFileReads counts llm.yaml reads until the test ends.
func CountConfigFileReads(t interface{ Cleanup(func()) }) *int {
	n := new(int)
	prev := readConfigFile
	readConfigFile = func(name string) ([]byte, error) {
		*n++
		return prev(name)
	}
	t.Cleanup(func() { readConfigFile = prev })
	return n
}

// IsPublicMediaAddr exposes [isPublicAddr].
var IsPublicMediaAddr = isPublicAddr

// MediaDialControl exposes [controlPublic].
var MediaDialControl = controlPublic

// AllowLoopbackMedia lets URLSource reach loopback test servers for the
// rest of t; every other check stays.
func AllowLoopbackMedia(t testing.TB) {
	t.Helper()
	prev := mediaAddrAllowed
	mediaAddrAllowed = func(a netip.Addr) bool { return a.Unmap().IsLoopback() || prev(a) }
	t.Cleanup(func() { mediaAddrAllowed = prev })
}

// FakeMediaLookup resolves the names in m (and only those) for the rest
// of t.
func FakeMediaLookup(t testing.TB, m map[string][]string) {
	t.Helper()
	prev := mediaLookup
	mediaLookup = func(_ context.Context, host string) ([]netip.Addr, error) {
		raw, ok := m[host]
		if !ok {
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
		out := make([]netip.Addr, 0, len(raw))
		for _, r := range raw {
			out = append(out, netip.MustParseAddr(r))
		}
		return out, nil
	}
	t.Cleanup(func() { mediaLookup = prev })
}

// FakeMediaLookupSeq answers successive lookups of host with answers in
// turn (the last one repeats) and returns the lookup count so far.
func FakeMediaLookupSeq(t testing.TB, host string, answers [][]string) func() int {
	t.Helper()
	var mu sync.Mutex
	n := 0
	prev := mediaLookup
	mediaLookup = func(_ context.Context, h string) ([]netip.Addr, error) {
		if h != host {
			return nil, &net.DNSError{Err: "no such host", Name: h, IsNotFound: true}
		}
		mu.Lock()
		i := min(n, len(answers)-1)
		n++
		mu.Unlock()
		out := make([]netip.Addr, 0, len(answers[i]))
		for _, r := range answers[i] {
			out = append(out, netip.MustParseAddr(r))
		}
		return out, nil
	}
	t.Cleanup(func() { mediaLookup = prev })
	return func() int { mu.Lock(); defer mu.Unlock(); return n }
}
