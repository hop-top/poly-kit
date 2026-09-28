package api_test

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// seen is what a handler behind ClientAddress observed.
type seen struct {
	remote, peer string
	https        bool
}

func serveClientAddr(t *testing.T, trusted []string, peer string, hdr map[string][]string) seen {
	t.Helper()
	prefixes, err := api.ParseTrustedProxies(trusted)
	require.NoError(t, err)
	var got seen
	h := api.ClientAddress(api.ClientAddressConfig{TrustedProxies: prefixes})(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			got = seen{remote: r.RemoteAddr, peer: api.PeerAddrFromContext(r.Context()), https: api.IsHTTPS(r)}
			m := api.RequestMetaFrom(r)
			assert.Equal(t, r.RemoteAddr, m.RemoteAddr)
			assert.Equal(t, got.peer, m.PeerAddr)
		}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = peer
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func TestClientAddress(t *testing.T) {
	const proxy = "10.0.0.2:4321"
	lan := []string{"10.0.0.0/8"}
	tests := []struct {
		name    string
		trusted []string
		peer    string
		hdr     map[string][]string
		want    seen
	}{
		{
			name: "no trusted proxies: spoofed headers ignored",
			peer: "203.0.113.9:5555",
			hdr: map[string][]string{
				"X-Forwarded-For": {"1.2.3.4"}, "Forwarded": {"for=1.2.3.4;proto=https"},
				"X-Real-IP": {"1.2.3.4"}, "X-Forwarded-Proto": {"https"},
			},
			want: seen{remote: "203.0.113.9:5555"},
		},
		{
			name:    "untrusted peer: spoofed headers ignored",
			trusted: lan,
			peer:    "203.0.113.9:5555",
			hdr: map[string][]string{
				"X-Forwarded-For": {"1.2.3.4"}, "Forwarded": {"for=1.2.3.4;proto=https"},
				"X-Real-IP": {"1.2.3.4"}, "X-Forwarded-Proto": {"https"},
			},
			want: seen{remote: "203.0.113.9:5555"},
		},
		{
			name: "trusted peer, X-Forwarded-For", trusted: lan, peer: proxy,
			hdr:  map[string][]string{"X-Forwarded-For": {"198.51.100.7"}, "X-Forwarded-Proto": {"https"}},
			want: seen{remote: "198.51.100.7", peer: proxy, https: true},
		},
		{
			name: "chain of trusted proxies stops at the first untrusted address", trusted: lan, peer: proxy,
			hdr:  map[string][]string{"X-Forwarded-For": {"6.6.6.6, 198.51.100.7, 10.1.1.1", "10.0.0.9"}},
			want: seen{remote: "198.51.100.7", peer: proxy},
		},
		{
			name: "every hop trusted: the leftmost", trusted: lan, peer: proxy,
			hdr:  map[string][]string{"X-Forwarded-For": {"10.9.9.9, 10.1.1.1"}},
			want: seen{remote: "10.9.9.9", peer: proxy},
		},
		{
			name: "client-supplied garbage left of the client is never read", trusted: lan, peer: proxy,
			hdr:  map[string][]string{"X-Forwarded-For": {"not-an-ip, 198.51.100.7"}},
			want: seen{remote: "198.51.100.7", peer: proxy},
		},
		{
			name: "Forwarded, chain, quoted IPv6 with port", trusted: []string{"10.0.0.0/8", "2001:db8:ffff::/48"},
			peer: proxy,
			hdr: map[string][]string{"Forwarded": {
				`for=192.0.2.60;proto=http, For="[2001:db8:cafe::17]:4711";Proto=HTTPS;by=10.0.0.1`,
				`for="[2001:db8:ffff::1]"`,
			}},
			want: seen{remote: "2001:db8:cafe::17", peer: proxy, https: true},
		},
		{
			name: "IPv6 peer and X-Forwarded-For", trusted: []string{"2001:db8::/32"}, peer: "[2001:db8::1]:443",
			hdr:  map[string][]string{"X-Forwarded-For": {"2001:db8:1::5, 2a00:1450::1"}},
			want: seen{remote: "2a00:1450::1", peer: "[2001:db8::1]:443"},
		},
		{
			name: "IPv4-mapped peer matches an IPv4 network", trusted: lan, peer: "[::ffff:10.0.0.2]:80",
			hdr:  map[string][]string{"X-Forwarded-For": {"198.51.100.7:6000"}},
			want: seen{remote: "198.51.100.7", peer: "[::ffff:10.0.0.2]:80"},
		},
		{
			name: "single trusted address", trusted: []string{"10.0.0.2"}, peer: proxy,
			hdr:  map[string][]string{"X-Real-IP": {"198.51.100.7"}, "X-Forwarded-Proto": {"https"}},
			want: seen{remote: "198.51.100.7", peer: proxy, https: true},
		},
		{
			name: "X-Real-IP loses to X-Forwarded-For", trusted: lan, peer: proxy,
			hdr:  map[string][]string{"X-Real-IP": {"6.6.6.6"}, "X-Forwarded-For": {"198.51.100.7"}},
			want: seen{remote: "198.51.100.7", peer: proxy},
		},
		{
			name: "Forwarded and X-Forwarded-For disagree: neither believed", trusted: lan, peer: proxy,
			hdr:  map[string][]string{"Forwarded": {"for=6.6.6.6;proto=https"}, "X-Forwarded-For": {"198.51.100.7"}},
			want: seen{remote: proxy},
		},
		{
			name: "Forwarded and X-Forwarded-For agree", trusted: lan, peer: proxy,
			hdr:  map[string][]string{"Forwarded": {"for=198.51.100.7"}, "X-Forwarded-For": {"198.51.100.7"}, "X-Forwarded-Proto": {"https"}},
			want: seen{remote: "198.51.100.7", peer: proxy, https: true},
		},
		{
			name: "X-Forwarded-Proto aligned with the client's hop", trusted: lan, peer: proxy,
			hdr:  map[string][]string{"X-Forwarded-For": {"198.51.100.7, 10.1.1.1"}, "X-Forwarded-Proto": {"https, http"}},
			want: seen{remote: "198.51.100.7", peer: proxy, https: true},
		},
		{
			name: "X-Forwarded-Proto that cannot be aligned is ignored", trusted: lan, peer: proxy,
			hdr:  map[string][]string{"X-Forwarded-For": {"198.51.100.7, 10.1.1.1, 10.1.1.2"}, "X-Forwarded-Proto": {"https, http"}},
			want: seen{remote: "198.51.100.7", peer: proxy},
		},
		{
			name: "lone X-Forwarded-Proto from a trusted peer", trusted: lan, peer: proxy,
			hdr:  map[string][]string{"X-Forwarded-Proto": {"HTTPS"}},
			want: seen{remote: proxy, https: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, serveClientAddr(t, tt.trusted, tt.peer, tt.hdr))
		})
	}
}

func TestClientAddress_MalformedHeadersLeaveThePeer(t *testing.T) {
	const proxy = "10.0.0.2:4321"
	for name, hdr := range map[string]map[string][]string{
		"garbage X-Forwarded-For":        {"X-Forwarded-For": {"198.51.100.7, bogus"}},
		"garbage past a trusted hop":     {"X-Forwarded-For": {"bogus, 10.1.1.1"}},
		"unknown past a trusted hop":     {"Forwarded": {"for=unknown, for=10.1.1.1"}},
		"empty X-Forwarded-For entry":    {"X-Forwarded-For": {"198.51.100.7,"}},
		"X-Forwarded-For hostname":       {"X-Forwarded-For": {"evil.example"}},
		"Forwarded for=unknown":          {"Forwarded": {"for=unknown"}},
		"Forwarded obfuscated node":      {"Forwarded": {"for=_hidden"}},
		"Forwarded without for":          {"Forwarded": {"proto=https;by=10.0.0.1"}},
		"Forwarded repeated parameter":   {"Forwarded": {"for=198.51.100.7;for=6.6.6.6"}},
		"Forwarded unterminated quote":   {"Forwarded": {`for="[2001:db8::1]`}},
		"Forwarded bare IPv6 unquoted":   {"Forwarded": {"for=2001:db8::1"}},
		"Forwarded trailing junk":        {"Forwarded": {"for=198.51.100.7 junk"}},
		"two X-Real-IP values":           {"X-Real-IP": {"198.51.100.7", "6.6.6.6"}},
		"X-Real-IP garbage":              {"X-Real-IP": {"???"}},
		"malformed Forwarded beside XFF": {"Forwarded": {"for=bogus"}, "X-Forwarded-For": {"198.51.100.7"}},
	} {
		t.Run(name, func(t *testing.T) {
			got := serveClientAddr(t, []string{"10.0.0.0/8"}, proxy, hdr)
			assert.Equal(t, seen{remote: proxy}, got)
		})
	}
}

func TestParseTrustedProxies(t *testing.T) {
	got, err := api.ParseTrustedProxies([]string{" 10.1.2.3/8 ", "", "192.0.2.1", "2001:db8::/32", "::1", "::ffff:172.16.0.0/108"})
	require.NoError(t, err)
	var s []string
	for _, p := range got {
		s = append(s, p.String())
	}
	assert.Equal(t, []string{"10.0.0.0/8", "192.0.2.1/32", "2001:db8::/32", "::1/128", "172.16.0.0/12"}, s)

	for _, bad := range []string{"10.0.0.0/33", "proxy.internal", "10.0.0", "fe80::1%eth0"} {
		_, err := api.ParseTrustedProxies([]string{bad})
		assert.ErrorContains(t, err, bad, bad)
	}
}

func TestSecurityHeaders_HSTSFollowsTrustedForwardedProto(t *testing.T) {
	prefixes, err := api.ParseTrustedProxies([]string{"10.0.0.0/8"})
	require.NoError(t, err)
	h := api.Chain(
		api.ClientAddress(api.ClientAddressConfig{TrustedProxies: prefixes}),
		api.SecurityHeaders(api.SecurityHeadersConfig{}),
	)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	hsts := func(peer string, tlsConn bool, proto string) string {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = peer
		if tlsConn {
			req.TLS = &tls.ConnectionState{}
		}
		if proto != "" {
			req.Header.Set("X-Forwarded-Proto", proto)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Header().Get("Strict-Transport-Security")
	}
	assert.NotEmpty(t, hsts("10.0.0.2:1", false, "https"), "trusted proxy forwarded https")
	assert.Empty(t, hsts("203.0.113.9:1", false, "https"), "untrusted peer's forwarded proto")
	assert.Empty(t, hsts("10.0.0.2:1", false, "http"), "trusted proxy forwarded http")
	assert.Empty(t, hsts("10.0.0.2:1", false, ""), "trusted proxy, no proto")
	assert.NotEmpty(t, hsts("203.0.113.9:1", true, ""), "TLS on this server")
}
