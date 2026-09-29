package cli

import (
	"bytes"
	"math/big"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/internal/testpki"
)

// servedSerial is the serial of the certificate a fresh connection to
// base is served, or nil when the request fails.
func servedSerial(t *testing.T, c *http.Client, url string) *big.Int {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		return nil
	}
	_ = resp.Body.Close()
	return resp.TLS.PeerCertificates[0].SerialNumber
}

// rejectedSince reports whether a reload logged after offset in logs
// was rejected with want. Any change in a watched directory reloads, so
// a replacement written through a temporary file beside the target can
// log the rejection of the contents still in force before the rename
// lands: only the reason tells a test's replacement apart.
func rejectedSince(logs *logBuffer, offset int, want string) bool {
	for _, line := range strings.Split(logs.String()[offset:], "\n") {
		if strings.Contains(line, "tls: reload rejected") && strings.Contains(line, want) {
			return true
		}
	}
	return false
}

func accepted(c *http.Client, url string) bool {
	resp, err := c.Get(url)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func TestAPITLSReloadsCertificateFiles(t *testing.T) {
	f := newTLSFixture(t)
	logs := &logBuffer{}
	r := guardRoot(t, f.tlsKeys(APIServiceName))
	r.Cmd.SetErr(logs)
	base, stop := serveAPI(t, r)
	defer stop()
	url := strings.Replace(base, "http://", "https://", 1) + "/healthz"

	first := servedSerial(t, f.client(), url)
	require.NotNil(t, first)

	// Rewritten in place, certificate then key: the listener waits
	// for the pair to settle, and serves the new one without a restart.
	next := f.ca.Localhost(t)
	require.NoError(t, os.WriteFile(f.certFile, next.CertPEM, 0o600))
	require.NoError(t, os.WriteFile(f.keyFile, next.KeyPEM, 0o600))
	eventually(t, func() bool {
		s := servedSerial(t, f.client(), url)
		return s != nil && s.Cmp(next.Cert().SerialNumber) == 0
	}, "the replaced certificate is served\n%s", logs)
	// The swap is logged once it is in force, so the line can trail the
	// first handshake served with it.
	eventually(t, func() bool { return strings.Contains(logs.String(), "tls: reloaded certificate files") },
		"the reload is logged\n%s", logs)

	other := f.ca.Localhost(t)
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"not a certificate", []byte("not a certificate"), "services.api.tls.cert_file"},
		{"a certificate the key does not match", other.CertPEM, "private key does not match public key"},
		{"a chain cut short mid-write", append(bytes.Clone(other.CertPEM), f.ca.PEM[:len(f.ca.PEM)/2]...), "PEM block is incomplete"},
	} {
		t.Run(tc.name+" is rejected and the old certificate keeps serving", func(t *testing.T) {
			since := len(logs.String())
			testpki.Replace(t, f.certFile, tc.data)
			eventually(t, func() bool { return rejectedSince(logs, since, tc.want) },
				"the replacement is rejected with %q\n%s", tc.want, logs)
			s := servedSerial(t, f.client(), url)
			require.NotNil(t, s, "the listener still serves")
			assert.Zero(t, s.Cmp(next.Cert().SerialNumber), "the certificate in force is unchanged")
		})
	}
}

func TestAPIMutualTLSReloadsTheCABundle(t *testing.T) {
	f := newTLSFixture(t)
	logs := &logBuffer{}
	r := guardRoot(t, f.mtlsKeys(APIServiceName))
	r.Cmd.SetErr(logs)
	base, stop := serveAPI(t, r)
	defer stop()
	url := strings.Replace(base, "http://", "https://", 1) + "/v1/commands/list"

	ca2 := testpki.NewCA(t, "second CA")
	bob := ca2.Issue(t, testpki.Leaf{CN: "bob", URIs: []string{"spiffe://example.org/tenant/acme/svc/bob"}, Client: true}).TLS
	require.True(t, accepted(f.client(f.clientCert), url))
	require.False(t, accepted(f.client(bob), url), "a certificate the bundle does not hold a CA for")

	testpki.Replace(t, f.caFile, ca2.PEM)
	eventually(t, func() bool { return accepted(f.client(bob), url) }, "the new CA's clients are admitted\n%s", logs)
	assert.False(t, accepted(f.client(f.clientCert), url), "the removed CA's clients are not")

	t.Run("a bundle with no certificate is rejected; the one in force stays", func(t *testing.T) {
		since := len(logs.String())
		testpki.Replace(t, f.caFile, []byte("nope"))
		eventually(t, func() bool { return rejectedSince(logs, since, "holds no PEM certificate") },
			"the bundle is rejected\n%s", logs)
		assert.True(t, accepted(f.client(bob), url))
	})
}

// Reload leaves the verifier auth.mode selected in place: under a
// bearer mode the replaced certificate is served and a token is still
// verified, a request without one still refused.
func TestAPITLSReloadKeepsTheBearerVerifier(t *testing.T) {
	f := newTLSFixture(t)
	idp, priv := jwksServer(t)
	keys := f.tlsKeys(APIServiceName)
	for k, v := range map[string]any{
		"services.api.auth.mode":          "jwks",
		"services.api.auth.jwks.url":      idp.URL + "/jwks.json",
		"services.api.auth.jwks.audience": "kit-api",
		"services.api.auth.jwks.issuer":   idp.URL,
	} {
		keys[k] = v
	}
	logs := &logBuffer{}
	r := bearerRoot(t, keys, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	r.Cmd.SetErr(logs)
	base, stop := serveAPI(t, r)
	defer stop()
	base = strings.Replace(base, "http://", "https://", 1)
	token := rsaToken(t, priv, map[string]any{"sub": "svc-a", "aud": "kit-api", "iss": idp.URL,
		"exp": time.Now().Add(time.Hour).Unix()})
	secret := func() int {
		req, err := http.NewRequest(http.MethodGet, base+"/v1/commands/secret", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := f.client().Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	anonymous := func() int {
		resp, _ := tlsGet(t, f.client(), base+"/v1/commands/secret")
		return resp.StatusCode
	}
	require.Equal(t, http.StatusOK, secret())
	require.Equal(t, http.StatusUnauthorized, anonymous())

	next := f.ca.Localhost(t)
	require.NoError(t, os.WriteFile(f.certFile, next.CertPEM, 0o600))
	require.NoError(t, os.WriteFile(f.keyFile, next.KeyPEM, 0o600))
	eventually(t, func() bool {
		s := servedSerial(t, f.client(), base+"/healthz")
		return s != nil && s.Cmp(next.Cert().SerialNumber) == 0
	}, "the replaced certificate is served\n%s", logs)

	assert.Equal(t, http.StatusOK, secret(), "the bearer verifier still admits a token after reload")
	assert.Equal(t, http.StatusUnauthorized, anonymous(), "and still refuses no token")
}
