package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Forwarding headers [ClientAddress] reads from a trusted proxy.
const (
	// HeaderForwarded is the RFC 7239 Forwarded header.
	HeaderForwarded = "Forwarded"
	// HeaderXForwardedFor is the de-facto X-Forwarded-For header: a
	// comma-separated list of addresses, each appended by one proxy.
	HeaderXForwardedFor = "X-Forwarded-For"
	// HeaderXForwardedProto is the de-facto X-Forwarded-Proto header:
	// the scheme the client used to reach the proxy.
	HeaderXForwardedProto = "X-Forwarded-Proto"
	// HeaderXRealIP is the single-address X-Real-IP header.
	HeaderXRealIP = "X-Real-IP"
)

// ClientAddressConfig configures [ClientAddress].
type ClientAddressConfig struct {
	// TrustedProxies are the networks whose forwarding headers are
	// believed: a request whose immediate peer lies in none of them
	// is served with the peer as its client, whatever it claims.
	// Empty trusts no proxy. [ParseTrustedProxies] builds it.
	TrustedProxies []netip.Prefix
}

// ParseTrustedProxies parses a trusted-proxy list: each entry a CIDR
// ("10.0.0.0/8", "2001:db8::/32") or a single address ("10.0.0.7",
// "::1"). Blank entries are skipped. The first entry that is neither
// is an error naming it.
func ParseTrustedProxies(entries []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if strings.Contains(e, "/") {
			p, err := netip.ParsePrefix(e)
			if err != nil {
				return nil, fmt.Errorf("trusted proxy %q: not a CIDR or an IP address", e)
			}
			if p.Addr().Is4In6() && p.Bits() >= 96 {
				p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(e)
		if err != nil || a.Zone() != "" {
			return nil, fmt.Errorf("trusted proxy %q: not a CIDR or an IP address", e)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

type peerAddrKey struct{}

type forwardedProtoKey struct{}

// ClientAddress returns the middleware of HTTP-plane slot 2: it
// resolves the address of the client behind any trusted proxies and
// makes it the request's RemoteAddr, so everything after it — the
// access log, the audit record, the rate limiter, a Connect peer —
// reads one address.
//
// Forwarding headers are read only when the immediate peer lies in
// cfg.TrustedProxies; with none configured, or from any other peer,
// they are ignored and the peer is the client. From a trusted peer,
// the header is walked right to left — each entry was appended by the
// hop to its right — past every trusted address, and the first
// untrusted one is the client. When every entry is trusted, the
// leftmost is. The headers, in order:
//
//   - Forwarded (RFC 7239), its for= parameters;
//   - X-Forwarded-For;
//   - X-Real-IP, only when neither of the others is present.
//
// A header is believed whole or not at all: an entry it has to walk
// that is not an address ("unknown", an obfuscated node, garbage), or
// an element without for=, leaves the peer as the client. So does a
// request carrying both Forwarded and X-Forwarded-For that name
// different clients: a proxy that writes one and passes the other
// through from the client would otherwise let the client pick its
// own address.
//
// With a forwarded client, RemoteAddr becomes its bare IP (no port)
// and the peer's own address is kept for [PeerAddrFromContext]. The
// scheme the client used, from the proto= of the element naming it
// or X-Forwarded-Proto, is kept for [IsHTTPS] — likewise only from a
// trusted peer. Nothing else about the request changes; the headers
// themselves are left in place.
func ClientAddress(cfg ClientAddressConfig) Middleware {
	trusted := append([]netip.Prefix(nil), cfg.TrustedProxies...)
	return func(next http.Handler) http.Handler {
		if len(trusted) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			peer, ok := addrOf(r.RemoteAddr)
			if !ok || !trustedAddr(trusted, peer) {
				next.ServeHTTP(w, r)
				return
			}
			client, proto, found := forwardedClient(r.Header, trusted)
			if !found && proto == "" {
				next.ServeHTTP(w, r)
				return
			}
			ctx := r.Context()
			if proto != "" {
				ctx = context.WithValue(ctx, forwardedProtoKey{}, proto)
			}
			if found {
				ctx = context.WithValue(ctx, peerAddrKey{}, r.RemoteAddr)
			}
			r = r.WithContext(ctx)
			if found {
				r.RemoteAddr = client.String()
			}
			next.ServeHTTP(w, r)
		})
	}
}

// PeerAddrFromContext returns the immediate peer's address when
// [ClientAddress] replaced RemoteAddr with a client a trusted proxy
// forwarded, else "". It is the proxy the request arrived through.
func PeerAddrFromContext(ctx context.Context) string {
	v, _ := ctx.Value(peerAddrKey{}).(string)
	return v
}

// IsHTTPS reports whether the client reached the service over TLS:
// the request arrived over TLS on this server, or a trusted proxy
// (see [ClientAddress]) forwarded https or wss as the scheme the
// client used. A forwarded scheme from any other peer is ignored.
func IsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	p, _ := r.Context().Value(forwardedProtoKey{}).(string)
	return p == "https" || p == "wss"
}

// forwardedClient resolves the client a trusted peer forwarded, and
// the scheme it used. found is false when no header names a client
// that can be believed; proto may still be set from a lone
// X-Forwarded-Proto.
func forwardedClient(h http.Header, trusted []netip.Prefix) (client netip.Addr, proto string, found bool) {
	fwd, hasFwd := h[HeaderForwarded]
	xff, hasXFF := h[HeaderXForwardedFor]
	xfp := splitList(h.Values(HeaderXForwardedProto))

	switch {
	case hasFwd || hasXFF:
		var fc, xc netip.Addr
		var fp, xp string
		var fok, xok bool
		if hasFwd {
			elems, ok := parseForwarded(fwd)
			if ok {
				fors := make([]string, len(elems))
				for i, e := range elems {
					fors[i] = e.forNode
				}
				var i int
				if fc, i, fok = walkHops(fors, trusted); fok {
					fp = elems[i].proto
				}
			}
		}
		if hasXFF {
			hops := splitList(xff)
			var i int
			if xc, i, xok = walkHops(hops, trusted); xok {
				switch len(xfp) {
				case 1:
					xp = xfp[0]
				case len(hops):
					xp = xfp[i]
				}
			}
		}
		switch {
		case hasFwd && hasXFF:
			if !fok || !xok || fc != xc {
				return netip.Addr{}, "", false
			}
			if fp == "" {
				fp = xp
			}
			return fc, validProto(fp), true
		case hasFwd:
			return fc, validProto(fp), fok
		default:
			return xc, validProto(xp), xok
		}
	case len(h.Values(HeaderXRealIP)) > 0:
		vals := splitList(h.Values(HeaderXRealIP))
		if len(vals) != 1 {
			return netip.Addr{}, "", false
		}
		c, _, ok := walkHops(vals, trusted)
		if !ok {
			return netip.Addr{}, "", false
		}
		return c, loneProto(xfp), true
	default:
		return netip.Addr{}, loneProto(xfp), false
	}
}

// loneProto is X-Forwarded-Proto when it holds exactly one scheme.
func loneProto(xfp []string) string {
	if len(xfp) != 1 {
		return ""
	}
	return validProto(xfp[0])
}

// walkHops walks hops right to left past trusted addresses. It
// returns the first untrusted address and its index, or the leftmost
// when all are trusted. ok is false for an empty list or an entry,
// reached before the walk stops, that is not an address.
func walkHops(hops []string, trusted []netip.Prefix) (netip.Addr, int, bool) {
	if len(hops) == 0 {
		return netip.Addr{}, 0, false
	}
	var last netip.Addr
	for i := len(hops) - 1; i >= 0; i-- {
		a, ok := nodeAddr(hops[i])
		if !ok {
			return netip.Addr{}, 0, false
		}
		if !trustedAddr(trusted, a) {
			return a, i, true
		}
		last = a
	}
	return last, 0, true
}

// trustedAddr reports whether a lies in one of the trusted networks.
func trustedAddr(trusted []netip.Prefix, a netip.Addr) bool {
	for _, p := range trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// addrOf parses a RemoteAddr — "ip:port", or a bare IP — into an
// unmapped address without zone.
func addrOf(remote string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	return parseIP(host)
}

// nodeAddr parses one forwarded node: an IPv4 address, a bracketed
// IPv6 address, either with a port, or a bare IPv6 address.
func nodeAddr(node string) (netip.Addr, bool) {
	node = strings.TrimSpace(node)
	if host, _, err := net.SplitHostPort(node); err == nil {
		return parseIP(host)
	}
	if strings.HasPrefix(node, "[") && strings.HasSuffix(node, "]") {
		return parseIP(node[1 : len(node)-1])
	}
	return parseIP(node)
}

func parseIP(s string) (netip.Addr, bool) {
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap().WithZone(""), true
}

// splitList splits comma-separated header values into trimmed
// entries, across every line of the header.
func splitList(values []string) []string {
	var out []string
	for _, v := range values {
		for _, e := range strings.Split(v, ",") {
			out = append(out, strings.TrimSpace(e))
		}
	}
	return out
}

// validProto lower-cases a forwarded scheme and returns it when it
// is a URI scheme (RFC 3986 §3.1), else "".
func validProto(p string) string {
	p = strings.ToLower(strings.TrimSpace(p))
	if p == "" || p[0] < 'a' || p[0] > 'z' {
		return ""
	}
	for _, c := range p {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '+' && c != '-' && c != '.' {
			return ""
		}
	}
	return p
}

// forwardedElement is one element of a Forwarded header: the node
// that connected to the proxy that wrote it, and the scheme it used.
type forwardedElement struct {
	forNode string
	proto   string
}

// parseForwarded parses Forwarded header lines (RFC 7239 §4): a
// comma-separated list of elements, each semicolon-separated
// token=value pairs, a value a token or a quoted string. ok is false
// for anything that does not parse, an element without for=, or a
// parameter repeated within an element.
func parseForwarded(lines []string) ([]forwardedElement, bool) {
	var out []forwardedElement
	for _, line := range lines {
		s := line
		for {
			e, rest, ok := parseForwardedElement(s)
			if !ok {
				return nil, false
			}
			out = append(out, e)
			rest = strings.TrimLeft(rest, " \t")
			if rest == "" {
				break
			}
			if rest[0] != ',' {
				return nil, false
			}
			s = rest[1:]
		}
	}
	return out, len(out) > 0
}

// parseForwardedElement parses one element off the front of s,
// returning it and what follows it (a comma or nothing).
func parseForwardedElement(s string) (forwardedElement, string, bool) {
	var e forwardedElement
	seen := map[string]bool{}
	for {
		s = strings.TrimLeft(s, " \t")
		name, rest := cutToken(s)
		if name == "" || !strings.HasPrefix(rest, "=") {
			return e, "", false
		}
		value, rest, ok := cutValue(rest[1:])
		if !ok {
			return e, "", false
		}
		name = strings.ToLower(name)
		if seen[name] {
			return e, "", false
		}
		seen[name] = true
		switch name {
		case "for":
			e.forNode = value
		case "proto":
			e.proto = value
		}
		rest = strings.TrimLeft(rest, " \t")
		if strings.HasPrefix(rest, ";") {
			s = rest[1:]
			continue
		}
		if rest != "" && rest[0] != ',' {
			return e, "", false
		}
		if !seen["for"] {
			return e, "", false
		}
		return e, rest, true
	}
}

// cutToken splits an RFC 7230 token off the front of s.
func cutToken(s string) (tok, rest string) {
	i := 0
	for i < len(s) && isTokenChar(s[i]) {
		i++
	}
	return s[:i], s[i:]
}

// cutValue splits a token or a quoted string off the front of s,
// unescaping the quoted string.
func cutValue(s string) (value, rest string, ok bool) {
	if !strings.HasPrefix(s, `"`) {
		v, r := cutToken(s)
		return v, r, v != ""
	}
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		switch c := s[i]; c {
		case '"':
			return b.String(), s[i+1:], true
		case '\\':
			if i+1 == len(s) {
				return "", "", false
			}
			i++
			b.WriteByte(s[i])
		default:
			b.WriteByte(c)
		}
	}
	return "", "", false
}

func isTokenChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
}
