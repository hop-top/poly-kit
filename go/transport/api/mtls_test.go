package api_test

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/internal/testpki"
)

var (
	oidOU     = asn1.ObjectIdentifier{2, 5, 4, 11}
	oidTenant = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 55555, 1}
)

func parsed(t *testing.T, i testpki.Issued) *x509.Certificate {
	t.Helper()
	c, err := x509.ParseCertificate(i.TLS.Certificate[0])
	require.NoError(t, err)
	return c
}

func TestClientCertClaims_Principal(t *testing.T) {
	ca := testpki.NewCA(t, "ca")
	full := parsed(t, ca.Issue(t, testpki.Leaf{
		CN:     "alice-cn",
		URIs:   []string{"spiffe://example.org/svc/alice"},
		DNS:    []string{"alice.example.org"},
		Emails: []string{"alice@example.org"},
		Client: true,
	}))
	dnsOnly := parsed(t, ca.Issue(t, testpki.Leaf{CN: "bob-cn", DNS: []string{"bob.example.org"}, Client: true}))
	cnOnly := parsed(t, ca.Issue(t, testpki.Leaf{CN: "carol", Client: true}))

	cases := []struct {
		name, source string
		cert         *x509.Certificate
		want         string
		wantErr      string
	}{
		{"default prefers the URI SAN", "", full, "spiffe://example.org/svc/alice", ""},
		{"san falls back to DNS", api.PrincipalSAN, dnsOnly, "bob.example.org", ""},
		{"san_dns", api.PrincipalSANDNS, full, "alice.example.org", ""},
		{"san_email", api.PrincipalSANEmail, full, "alice@example.org", ""},
		{"san_uri", api.PrincipalSANURI, full, "spiffe://example.org/svc/alice", ""},
		{"cn", api.PrincipalCN, full, "alice-cn", ""},
		{"no SAN of the kind asked for", api.PrincipalSAN, cnOnly, "", "no principal (san)"},
		{"no URI SAN", api.PrincipalSANURI, dnsOnly, "", "no principal (san_uri)"},
		{"unknown source", "serial", full, "", "unknown principal source"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := api.ClientCertClaims(tc.cert, api.ClientCertConfig{Principal: tc.source})
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, c.Subject)
			p, _ := api.IdentityOf(c)
			assert.Equal(t, tc.want, p, "the claims attribute the call")
		})
	}
}

func TestClientCertClaims_Tenant(t *testing.T) {
	ca := testpki.NewCA(t, "ca")
	ext, err := asn1.Marshal("globex")
	require.NoError(t, err)
	cert := parsed(t, ca.Issue(t, testpki.Leaf{
		CN:         "alice",
		URIs:       []string{"spiffe://example.org/tenant/acme/svc/alice"},
		Names:      []pkix.AttributeTypeAndValue{{Type: oidOU, Value: "initech"}},
		Extensions: []pkix.Extension{{Id: oidTenant, Value: ext}},
		Client:     true,
	}))

	cases := []struct {
		name string
		cfg  api.ClientCertConfig
		want string
	}{
		{"none configured", api.ClientCertConfig{}, ""},
		{"subject attribute", api.ClientCertConfig{TenantOID: oidOU}, "initech"},
		{"extension", api.ClientCertConfig{TenantOID: oidTenant}, "globex"},
		{"absent OID", api.ClientCertConfig{TenantOID: asn1.ObjectIdentifier{2, 5, 4, 10}}, ""},
		{"SAN pattern capture", api.ClientCertConfig{TenantSAN: regexp.MustCompile(`^spiffe://example\.org/tenant/([^/]+)/`)}, "acme"},
		{"SAN pattern whole match", api.ClientCertConfig{TenantSAN: regexp.MustCompile(`acme`)}, "acme"},
		{"SAN pattern no match", api.ClientCertConfig{TenantSAN: regexp.MustCompile(`^nomatch`)}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := api.ClientCertClaims(cert, tc.cfg)
			require.NoError(t, err)
			assert.Equal(t, tc.want, c.Tenant)
		})
	}
}

func TestClientCertAuth_RefusesWithoutAVerifiedCertificate(t *testing.T) {
	auth := api.ClientCertAuth(api.ClientCertConfig{})

	_, err := auth(httptest.NewRequest(http.MethodGet, "/", nil))
	require.ErrorContains(t, err, "did not arrive over TLS")

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.TLS = &tls.ConnectionState{HandshakeComplete: true}
	_, err = auth(r)
	require.ErrorContains(t, err, "client certificate required")
}

// TestClientCertAuth_OverTLS runs the verifier on a TLS server that
// asks for client certificates, including the path an RPC interceptor
// takes: a synthetic request carrying only the request context, whose
// TLS state comes from the connection TLSConnContext recorded.
func TestClientCertAuth_OverTLS(t *testing.T) {
	ca := testpki.NewCA(t, "ca")
	auth := api.ClientCertAuth(api.ClientCertConfig{Principal: api.PrincipalCN})

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		synthetic := (&http.Request{Header: r.Header.Clone()}).WithContext(r.Context())
		for _, req := range []*http.Request{r, synthetic} {
			claims, err := auth(req)
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}
			p, _ := api.IdentityOf(claims)
			_, _ = io.WriteString(w, p+";")
		}
	}))
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{ca.Localhost(t).TLS},
		ClientCAs:    ca.Pool(),
		ClientAuth:   tls.VerifyClientCertIfGiven,
	}
	srv.Config.ConnContext = api.TLSConnContext
	srv.StartTLS()
	t.Cleanup(srv.Close)

	get := func(certs ...tls.Certificate) (int, string) {
		t.Helper()
		hc := &http.Client{Transport: &http.Transport{TLSClientConfig: ca.ClientTLS(certs...)}}
		resp, err := hc.Get(srv.URL)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	code, body := get(ca.Issue(t, testpki.Leaf{CN: "alice", Client: true}).TLS)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "alice;alice;", body, "the request and the synthetic request see the same certificate")

	code, body = get()
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Contains(t, body, "client certificate required")
}
