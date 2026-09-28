package cli

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/internal/testpki"
)

// tlsFixture is a CA, a loopback server certificate it signed, and
// their files.
type tlsFixture struct {
	ca                 *testpki.CA
	dir                string
	certFile, keyFile  string
	caFile             string
	clientCert, rogue  tls.Certificate
	clientPrincipal    string
	clientTenant       string
	clientTenantRegexp string
}

func newTLSFixture(t *testing.T) *tlsFixture {
	t.Helper()
	f := &tlsFixture{ca: testpki.NewCA(t, "test CA"), dir: t.TempDir()}
	f.certFile, f.keyFile = f.ca.Localhost(t).Write(t, f.dir, "server")
	f.caFile = f.ca.WriteCA(t, f.dir, "ca")
	f.clientPrincipal = "spiffe://example.org/tenant/acme/svc/alice"
	f.clientTenant = "acme"
	f.clientTenantRegexp = `^spiffe://example\.org/tenant/([^/]+)/`
	f.clientCert = f.ca.Issue(t, testpki.Leaf{CN: "alice", URIs: []string{f.clientPrincipal}, Client: true}).TLS
	other := testpki.NewCA(t, "other CA")
	f.rogue = other.Issue(t, testpki.Leaf{CN: "mallory", URIs: []string{f.clientPrincipal}, Client: true}).TLS
	return f
}

// tlsKeys are the keys serving svc over TLS with the fixture's
// certificate.
func (f *tlsFixture) tlsKeys(svc string) map[string]any {
	return map[string]any{
		"services." + svc + ".tls.cert_file": f.certFile,
		"services." + svc + ".tls.key_file":  f.keyFile,
	}
}

// mtlsKeys add auth.mode: mtls with the fixture's CA and a SAN tenant.
func (f *tlsFixture) mtlsKeys(svc string) map[string]any {
	m := f.tlsKeys(svc)
	m["services."+svc+".auth.mode"] = "mtls"
	m["services."+svc+".auth.mtls.ca_file"] = f.caFile
	m["services."+svc+".auth.mtls.tenant_san_pattern"] = f.clientTenantRegexp
	return m
}

// client trusts the fixture's CA and presents certs.
func (f *tlsFixture) client(certs ...tls.Certificate) *http.Client {
	return &http.Client{Transport: &http.Transport{
		TLSClientConfig:   f.ca.ClientTLS(certs...),
		ForceAttemptHTTP2: true,
	}}
}

// rogueClient presents the certificate another CA signed, whatever CAs
// the server names: Go's client would otherwise send none.
func (f *tlsFixture) rogueClient() *http.Client {
	c := f.client()
	c.Transport.(*http.Transport).TLSClientConfig.GetClientCertificate =
		func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return &f.rogue, nil }
	return c
}

// loopbackTLS is the https base URL for a listener bound to addr.
func loopbackTLS(t *testing.T, base string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(base, "http://"))
	require.NoError(t, err)
	return "https://127.0.0.1:" + port
}

func setKeys(r *Root, keys map[string]any) {
	for k, v := range keys {
		r.Viper.Set(k, v)
	}
}

func tlsGet(t *testing.T, c *http.Client, url string) (*http.Response, string) {
	t.Helper()
	resp, err := c.Get(url)
	require.NoError(t, err)
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, string(b)
}

func TestAPIServesTLS(t *testing.T) {
	f := newTLSFixture(t)
	r := guardRoot(t, f.tlsKeys(APIServiceName))
	base, stop := serveAPI(t, r)
	defer stop()
	base = strings.Replace(base, "http://", "https://", 1)

	resp, body := tlsGet(t, f.client(), base+"/v1/commands/list")
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	assert.Equal(t, 2, resp.ProtoMajor, "HTTP/2 is negotiated over TLS")
	assert.Equal(t, uint16(tls.VersionTLS13), resp.TLS.Version)
	assert.Equal(t, "max-age=31536000", resp.Header.Get("Strict-Transport-Security"),
		"HSTS is sent once the request arrived over TLS")

	// Plain TLS is not authentication: nothing asked for a client
	// certificate, and the call is unattributed.
	assert.Contains(t, body, "listed")

	// The listener speaks TLS only.
	plain, err := http.Get(strings.Replace(base, "https://", "http://", 1) + "/v1/commands/list")
	require.NoError(t, err)
	_ = plain.Body.Close()
	assert.Equal(t, http.StatusBadRequest, plain.StatusCode)

	// A client held to TLS 1.2 still connects: the floor is 1.2.
	old := f.client()
	old.Transport.(*http.Transport).TLSClientConfig.MaxVersion = tls.VersionTLS12
	resp, _ = tlsGet(t, old, base+"/healthz")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestAPIWithoutTLSSendsNoHSTS(t *testing.T) {
	r := guardRoot(t, nil)
	base, stop := serveAPI(t, r)
	defer stop()
	resp, _ := get(t, base+"/v1/commands/list", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("Strict-Transport-Security"))
}

func TestAPIMinVersion13(t *testing.T) {
	f := newTLSFixture(t)
	keys := f.tlsKeys(APIServiceName)
	keys["services.api.tls.min_version"] = "1.3"
	base, stop := serveAPI(t, guardRoot(t, keys))
	defer stop()
	base = strings.Replace(base, "http://", "https://", 1)

	old := f.client()
	old.Transport.(*http.Transport).TLSClientConfig.MaxVersion = tls.VersionTLS12
	_, err := old.Get(base + "/healthz")
	require.Error(t, err, "a TLS 1.2 client is refused under min_version 1.3")
}

func TestAPIMutualTLS(t *testing.T) {
	f := newTLSFixture(t)
	rec := &auditRecorder{}
	r := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}), WithAuditSinks(rec.spec()))
	r.Cmd.AddCommand(&cobra.Command{
		Use:   "secret",
		Short: "a secret",
		RunE:  func(cmd *cobra.Command, _ []string) error { cmd.Print("unlocked"); return nil },
		Annotations: map[string]string{
			"kit/side-effect":   "read",
			"kit/auth-required": "true",
		},
	})
	setKeys(r, f.mtlsKeys(APIServiceName))
	base, stop := serveAPI(t, r)
	defer stop()
	base = strings.Replace(base, "http://", "https://", 1)

	t.Run("the certificate is an established identity: kit/auth-required admits it", func(t *testing.T) {
		resp, body := tlsGet(t, f.client(f.clientCert), base+"/v1/commands/secret")
		require.Equal(t, http.StatusOK, resp.StatusCode, body)
		assert.Contains(t, body, "unlocked")
		inv, _, err := rec.last(t)
		require.NoError(t, err)
		assert.True(t, inv.Meta.Authenticated(), "an mTLS caller is established, not claimed")
		assert.Equal(t, f.clientPrincipal, inv.Meta.Caller)
	})
	t.Run("accepted, attributed to the certificate", func(t *testing.T) {
		resp, body := tlsGet(t, f.client(f.clientCert), base+"/v1/commands/list")
		require.Equal(t, http.StatusOK, resp.StatusCode, body)
		inv, _, err := rec.last(t)
		require.NoError(t, err)
		assert.Equal(t, f.clientPrincipal, inv.Meta.Caller)
		assert.Equal(t, f.clientTenant, inv.Meta.Tenant)
	})
	t.Run("no certificate is unauthenticated, and audited", func(t *testing.T) {
		before := rec.count()
		resp, body := tlsGet(t, f.client(), base+"/v1/commands/list")
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, body)
		assert.Contains(t, body, "client certificate required")
		assert.Equal(t, before+1, rec.count())
		_, _, err := rec.last(t)
		assert.ErrorIs(t, err, cmdsurface.ErrAuthRefused)
	})
	t.Run("health probes answer without a certificate", func(t *testing.T) {
		resp, _ := tlsGet(t, f.client(), base+"/healthz")
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
	t.Run("a certificate the CA bundle does not verify fails the handshake", func(t *testing.T) {
		_, err := f.rogueClient().Get(base + "/v1/commands/list")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "tls")
	})
}

func TestAPIExposureWithTLS(t *testing.T) {
	f := newTLSFixture(t)

	t.Run("plain TLS does not authenticate", func(t *testing.T) {
		r := authRoot(t, WithAPI(APIConfig{Addr: "0.0.0.0:0", InsecureNoPolicy: true}))
		setKeys(r, f.tlsKeys(APIServiceName))
		oe := usageErr(t, runServeExpect(t, r, []string{"serve", "api"}, 2*time.Second))
		assert.Contains(t, oe.Message, "is not a loopback address and the api service has no authentication")
	})
	t.Run("mtls authenticates", func(t *testing.T) {
		r := authRoot(t, WithAPI(APIConfig{Addr: "0.0.0.0:0", InsecureNoPolicy: true}))
		setKeys(r, f.mtlsKeys(APIServiceName))
		base, stop := serveAPI(t, r)
		defer stop()
		base = loopbackTLS(t, base)
		resp, body := tlsGet(t, f.client(f.clientCert), base+"/v1/commands/list")
		assert.Equal(t, http.StatusOK, resp.StatusCode, body)
	})
}

func TestServeTLSConfigErrors(t *testing.T) {
	f := newTLSFixture(t)
	notPEM := filepath.Join(f.dir, "not.pem")
	require.NoError(t, os.WriteFile(notPEM, []byte("nope"), 0o600))

	cases := []struct {
		name string
		keys map[string]any
		want string
	}{
		{"enabled without a certificate", map[string]any{"services.api.tls.enabled": true},
			"services.api.tls.enabled: TLS is on but has no certificate"},
		{"cert without key", map[string]any{"services.api.tls.cert_file": f.certFile},
			"set both, or neither"},
		{"missing file", map[string]any{
			"services.api.tls.cert_file": filepath.Join(f.dir, "gone.crt"),
			"services.api.tls.key_file":  f.keyFile,
		}, "services.api.tls.cert_file, services.api.tls.key_file:"},
		{"two sources", map[string]any{
			"services.api.tls.cert_file":    f.certFile,
			"services.api.tls.key_file":     f.keyFile,
			"services.api.tls.acme.domains": []string{"example.org"},
		}, "use one certificate source"},
		{"bad min version", map[string]any{
			"services.api.tls.cert_file":   f.certFile,
			"services.api.tls.key_file":    f.keyFile,
			"services.api.tls.min_version": "1.1",
		}, `services.api.tls.min_version: "1.1" is not a supported version`},
		{"unknown tls key", map[string]any{"services.api.tls.cert": f.certFile},
			`unknown key "cert"`},
		{"unknown mode", map[string]any{"services.api.auth.mode": "kerberos"},
			`services.api.auth.mode: unknown mode "kerberos"`},
		{"mtls without tls", map[string]any{"services.api.auth.mode": "mtls"},
			`services.api.auth.mode: "mtls" needs TLS`},
		{"mtls without a CA", map[string]any{
			"services.api.tls.cert_file": f.certFile,
			"services.api.tls.key_file":  f.keyFile,
			"services.api.auth.mode":     "mtls",
		}, "services.api.auth.mtls.ca_file:"},
		{"CA bundle holds no certificate", withKeys(f.mtlsKeys("api"), "services.api.auth.mtls.ca_file", notPEM),
			"holds no PEM certificate"},
		{"unknown principal source", withKeys(f.mtlsKeys("api"), "services.api.auth.mtls.principal", "serial"),
			`services.api.auth.mtls.principal: unknown source "serial"`},
		{"two tenant sources", withKeys(f.mtlsKeys("api"), "services.api.auth.mtls.tenant_oid", "2.5.4.11"),
			"set one tenant source, not both"},
		{"bad OID", withKeys(withKeys(f.mtlsKeys("api"), "services.api.auth.mtls.tenant_san_pattern", ""),
			"services.api.auth.mtls.tenant_oid", "ou"), `"ou" is not a dotted object identifier`},
		{"bad pattern", withKeys(f.mtlsKeys("api"), "services.api.auth.mtls.tenant_san_pattern", "("),
			"services.api.auth.mtls.tenant_san_pattern:"},
		{"mtls keys without the mode", map[string]any{"services.api.auth.mtls.ca_file": f.caFile},
			`services.api.auth.mtls.ca_file: set, but services.api.auth.mode is not "mtls"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := guardRoot(t, tc.keys)
			oe := usageErr(t, runServeExpect(t, r, []string{"serve", "api"}, 2*time.Second))
			assert.Equal(t, 2, oe.ExitCode)
			assert.Contains(t, oe.Message, tc.want)
		})
	}
}

func withKeys(m map[string]any, kv ...any) map[string]any {
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

func TestServeTLSSharedAndDisabled(t *testing.T) {
	f := newTLSFixture(t)

	// services.all.tls serves every listener; the service's own
	// enabled: false turns it off for that one.
	r := guardRoot(t, map[string]any{
		"services.all.tls.cert_file": f.certFile,
		"services.all.tls.key_file":  f.keyFile,
	})
	st, err := ResolveServeTLS(r, APIServiceName)
	require.NoError(t, err)
	assert.True(t, st.Enabled())
	assert.Equal(t, "https", st.Scheme())
	assert.Nil(t, st.ClientCertAuth(), "plain TLS configures no verifier")

	r.Viper.Set("services.api.tls.enabled", false)
	st, err = ResolveServeTLS(r, APIServiceName)
	require.NoError(t, err)
	assert.False(t, st.Enabled())
	assert.Equal(t, "http", st.Scheme())

	var none *ServeTLS
	assert.False(t, none.Enabled())
	assert.Nil(t, none.ClientCertAuth())
}

func TestServeTLSACME(t *testing.T) {
	r := guardRoot(t, map[string]any{
		"services.api.tls.acme.domains":       "api.example.org, www.example.org",
		"services.api.tls.acme.directory_url": "https://acme-staging-v02.api.letsencrypt.org/directory",
	})
	st, err := ResolveServeTLS(r, APIServiceName)
	require.NoError(t, err)
	require.True(t, st.Enabled())
	assert.NotNil(t, st.config.GetCertificate, "certificates come from the ACME manager")
	assert.True(t, slices.Contains(st.config.NextProtos, "acme-tls/1"),
		"the TLS-ALPN-01 challenge is answered on the listener itself")
	assert.Equal(t, uint16(tls.VersionTLS12), st.config.MinVersion)

	r = guardRoot(t, map[string]any{"services.api.tls.acme.enabled": true})
	_, err = ResolveServeTLS(r, APIServiceName)
	require.ErrorContains(t, err, "services.api.tls.acme.domains: ACME is on but names no domain")
}
