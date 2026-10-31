package netpolicy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"golang.org/x/net/idna"
)

// ErrHostNotAllowed is returned when a host allowlist is in force and the
// destination is neither loopback nor on it. Callers match it with
// errors.Is, as with ErrOffline.
var ErrHostNotAllowed = errors.New("host not allowed by netpolicy.allow_hosts")

// Refusal describes one refused operation, handed to the audit sink.
type Refusal struct {
	// Reason is ErrOffline or ErrHostNotAllowed.
	Reason error
	// Op is the HTTP method of a refused request, or "dial <network>"
	// for a refused dial.
	Op string
	// Dest is where it was going. For a request: scheme, host and path
	// only, never query, fragment or userinfo. For a dial: the address
	// as given.
	Dest string
}

type (
	allowCtxKey struct{}
	auditCtxKey struct{}
)

// WithAllowHosts returns a context carrying the host allowlist
// (config key netpolicy.allow_hosts). Entries are an exact host
// ("graph.microsoft.com") or a "*.suffix" that matches one or more
// labels below the suffix. While the list is in force, a non-loopback
// destination that matches no entry is refused with ErrHostNotAllowed.
//
// An empty list returns ctx unchanged: no list means no restriction, and
// an untagged context stays clean. Entries that are not valid (see
// ValidateAllowHosts) never match, so a malformed list fails closed;
// validate configuration up front to fail loud instead.
func WithAllowHosts(ctx context.Context, patterns []string) context.Context {
	if len(patterns) == 0 {
		return ctx
	}
	return context.WithValue(ctx, allowCtxKey{}, compileAllow(patterns))
}

// AllowHosts returns a copy of the entries WithAllowHosts stored, or nil
// when ctx carries no allowlist. nil-safe.
func AllowHosts(ctx context.Context) []string {
	l := allowFrom(ctx)
	if l == nil {
		return nil
	}
	return append([]string(nil), l.raw...)
}

// HostAllowed reports whether a destination host (with or without a
// port) passes the allowlist in ctx. It is true when ctx carries no
// list, for loopback, and for a match. It ignores the offline marker.
func HostAllowed(ctx context.Context, host string) bool {
	l := allowFrom(ctx)
	return l == nil || isLoopback(host) || l.matches(host)
}

// ValidateAllowHosts reports the first entry that can never match: empty,
// a bare "*", a wildcard anywhere but a leading "*." label, a port, a
// path, or a "*." suffix over an IP literal.
func ValidateAllowHosts(patterns []string) error {
	for _, p := range patterns {
		if _, ok := parseEntry(p); !ok {
			return fmt.Errorf("netpolicy.allow_hosts: invalid entry %q: want a host or *.suffix", p)
		}
	}
	return nil
}

// WithAudit returns a context whose policy refusals are reported to fn
// (nil returns ctx unchanged). fn runs synchronously on the refusing
// goroutine and must not block; it receives scheme, host and path only,
// never credentials. Wire it to the tool's log or bus.
func WithAudit(ctx context.Context, fn func(Refusal)) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, auditCtxKey{}, fn)
}

func audit(ctx context.Context, reason error, op, dest string) {
	if ctx == nil {
		return
	}
	if fn, _ := ctx.Value(auditCtxKey{}).(func(Refusal)); fn != nil {
		fn(Refusal{Reason: reason, Op: op, Dest: dest})
	}
}

// refuse reports the policy decision for host: nil to proceed, otherwise
// ErrOffline or ErrHostNotAllowed. Offline wins: it is the stricter rule
// and its loopback exemption is the allowlist's.
func refuse(ctx context.Context, host string) error {
	if isLoopback(host) {
		return nil
	}
	if IsOffline(ctx) {
		return ErrOffline
	}
	if l := allowFrom(ctx); l != nil && !l.matches(host) {
		return ErrHostNotAllowed
	}
	return nil
}

func allowFrom(ctx context.Context) *allowList {
	if ctx == nil {
		return nil
	}
	l, _ := ctx.Value(allowCtxKey{}).(*allowList)
	return l
}

type entry struct {
	host   string
	suffix bool
}

type allowList struct {
	raw     []string
	entries []entry
}

func compileAllow(patterns []string) *allowList {
	l := &allowList{raw: append([]string(nil), patterns...)}
	for _, p := range patterns {
		if e, ok := parseEntry(p); ok {
			l.entries = append(l.entries, e)
		}
	}
	return l
}

func (l *allowList) matches(hostport string) bool {
	h := normalizeHost(hostport)
	if h == "" {
		return false
	}
	_, ipErr := netip.ParseAddr(h)
	isIP := ipErr == nil
	for _, e := range l.entries {
		switch {
		case !e.suffix && h == e.host:
			return true
		case e.suffix && !isIP && strings.HasSuffix(h, "."+e.host):
			return true
		}
	}
	return false
}

// parseEntry validates and normalizes one allowlist entry.
func parseEntry(p string) (entry, bool) {
	p = strings.TrimSpace(p)
	suffix := false
	if rest, ok := strings.CutPrefix(p, "*."); ok {
		p, suffix = rest, true
	}
	if strings.ContainsAny(p, "*/ \t%") {
		return entry{}, false
	}
	// A port or any other colon is only legal inside an IPv6 literal.
	if strings.Contains(p, ":") && !strings.HasPrefix(p, "[") && net.ParseIP(p) == nil {
		return entry{}, false
	}
	if strings.HasPrefix(p, "[") && !strings.HasSuffix(p, "]") {
		return entry{}, false
	}
	h := normalizeHost(p)
	if h == "" {
		return entry{}, false
	}
	if _, err := netip.ParseAddr(h); err == nil && suffix {
		return entry{}, false
	}
	return entry{host: h, suffix: suffix}, true
}

// normalizeHost reduces host (optionally with a port, optionally a
// bracketed IPv6 literal) to its comparison form: lower case, no
// trailing dot, IDNA names in ASCII, IP literals canonical. It returns
// "" for anything that cannot be a host.
func normalizeHost(hostport string) string {
	h, _, err := net.SplitHostPort(hostport)
	if err != nil {
		h = unbracketIPv6(hostport)
	}
	if h = strings.TrimPrefix(strings.TrimSuffix(h, "]"), "["); h == "" {
		return ""
	}
	if addr, err := netip.ParseAddr(h); err == nil {
		if strings.Contains(h, "%") {
			return ""
		}
		return addr.Unmap().String()
	}
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	if h == "" || strings.HasPrefix(h, ".") || strings.Contains(h, "..") {
		return ""
	}
	ascii, err := idna.Lookup.ToASCII(h)
	if err != nil || ascii == "" {
		return ""
	}
	return ascii
}
