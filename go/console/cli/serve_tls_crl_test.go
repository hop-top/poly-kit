package cli

import (
	"crypto/tls"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/internal/testpki"
)

func TestAPIMutualTLSRevocation(t *testing.T) {
	f := newTLSFixture(t)
	bob := f.ca.Issue(t, testpki.Leaf{CN: "bob", URIs: []string{"spiffe://example.org/tenant/acme/svc/bob"}, Client: true})
	crlFile := filepath.Join(f.dir, "clients.crl")
	require.NoError(t, os.WriteFile(crlFile, f.ca.CRL(t, bob.Cert()), 0o600))

	logs := &logBuffer{}
	r := guardRoot(t, withKeys(f.mtlsKeys(APIServiceName), "services.api.auth.mtls.crl_file", crlFile))
	r.Cmd.SetErr(logs)
	base, stop := serveAPI(t, r, "-V")
	defer stop()
	url := strings.Replace(base, "http://", "https://", 1) + "/v1/commands/list"

	assert.True(t, accepted(f.client(f.clientCert), url), "a certificate the list does not name")
	assert.False(t, accepted(f.client(bob.TLS), url), "a revoked certificate fails the handshake")
	eventually(t, func() bool { return strings.Contains(logs.String(), "is revoked") },
		"the handshake failure names the revocation\n%s", logs)
	resp, _ := tlsGet(t, f.client(), strings.Replace(url, "/v1/commands/list", "/healthz", 1))
	assert.Equal(t, http.StatusOK, resp.StatusCode, "no certificate is still no certificate")

	// A client that resumes its session is checked again: a session
	// begun before the revocation does not outlive it.
	resuming := f.client(f.clientCert)
	tr := resuming.Transport.(*http.Transport)
	tr.DisableKeepAlives = true
	tr.TLSClientConfig.ClientSessionCache = tls.NewLRUClientSessionCache(4)
	require.True(t, accepted(resuming, url))
	resp, err := resuming.Get(url)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.True(t, resp.TLS.DidResume, "the second connection resumes the session")

	// A reissued list takes effect without a restart, even beside a
	// certificate write that is rejected.
	testpki.Replace(t, f.certFile, []byte("not a certificate"))
	testpki.Replace(t, crlFile, f.ca.CRL(t, bob.Cert(), f.clientCert.Leaf))
	eventually(t, func() bool { return !accepted(f.client(f.clientCert), url) },
		"a certificate revoked while serving is refused\n%s", logs)
	assert.False(t, accepted(resuming, url), "a resumed session of a revoked certificate is refused")
	assert.Contains(t, logs.String(), "tls: reload rejected")
	assert.True(t, accepted(f.client(), strings.Replace(url, "/v1/commands/list", "/healthz", 1)),
		"the certificate in force still serves")
}

func TestServeTLSRevocationConfigErrors(t *testing.T) {
	f := newTLSFixture(t)
	garbage := filepath.Join(f.dir, "garbage.crl")
	require.NoError(t, os.WriteFile(garbage, []byte("-----BEGIN X509 CRL-----\nAAAA\n-----END X509 CRL-----\n"), 0o600))
	impostor := filepath.Join(f.dir, "impostor.crl")
	require.NoError(t, os.WriteFile(impostor, testpki.NewCA(t, "test CA").CRL(t), 0o600))
	good := filepath.Join(f.dir, "good.crl")
	require.NoError(t, os.WriteFile(good, f.ca.CRL(t), 0o600))

	for _, tc := range []struct {
		name string
		keys map[string]any
		want string
	}{
		{"a list that does not parse", withKeys(f.mtlsKeys("api"), "services.api.auth.mtls.crl_file", garbage),
			"services.api.auth.mtls.crl_file:"},
		{"a missing list", withKeys(f.mtlsKeys("api"), "services.api.auth.mtls.crl_file", filepath.Join(f.dir, "gone.crl")),
			"services.api.auth.mtls.crl_file:"},
		{"a list the bundle's CA of that name did not sign", withKeys(f.mtlsKeys("api"), "services.api.auth.mtls.crl_file", impostor),
			`is not signed by that CA in the bundle`},
		{"a list without mtls", withKeys(f.tlsKeys("api"), "services.api.auth.mtls.crl_file", good),
			`services.api.auth.mtls.crl_file: set, but services.api.auth.mode is not "mtls"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ResolveServeTLS(guardRoot(t, tc.keys), APIServiceName)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
	st, err := ResolveServeTLS(guardRoot(t, withKeys(f.mtlsKeys("api"), "services.api.auth.mtls.crl_file", good)), APIServiceName)
	require.NoError(t, err)
	assert.NotNil(t, st.ClientCertAuth())
}
