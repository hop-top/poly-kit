package main

import (
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/internal/testpki"
)

// TestTLSListenerCountsHandshakesAndReloadsItsCertificate serves the api
// over TLS with the kit provider scraping: a failed handshake is counted
// as tls_handshake, and a certificate renewed on disk is served without
// a restart.
func TestTLSListenerCountsHandshakesAndReloadsItsCertificate(t *testing.T) {
	ca := testpki.NewCA(t, "served test CA")
	dir := t.TempDir()
	certFile, keyFile := ca.Localhost(t).Write(t, dir, "server")
	config := map[string]any{
		"services.api.tls.cert_file": certFile,
		"services.api.tls.key_file":  keyFile,
	}
	for k, v := range scrapeOnly {
		config[k] = v
	}
	run := startServe(t, options{config: config}, "api", "--addr", "127.0.0.1:0")
	addr := run.waitReady(t, "api").Address
	client := func() *http.Client {
		return &http.Client{Transport: &http.Transport{TLSClientConfig: ca.ClientTLS(), DisableKeepAlives: true}}
	}

	// Plaintext to the TLS port fails the handshake before any request.
	resp, err := http.Get("http://" + addr + "/healthz")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	require.Eventually(t, func() bool {
		resp, err := client().Get("https://" + addr + "/metrics")
		if err != nil {
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return strings.Contains(string(body), `kit_refusal_reason="tls_handshake"`)
	}, 10*time.Second, 50*time.Millisecond, "the failed handshake is counted")

	next := ca.Localhost(t)
	testpki.Replace(t, certFile+".new", next.CertPEM)
	testpki.Replace(t, keyFile, next.KeyPEM)
	require.NoError(t, os.Rename(certFile+".new", certFile))
	require.Eventually(t, func() bool {
		resp, err := client().Get("https://" + addr + "/healthz")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.TLS.PeerCertificates[0].SerialNumber.Cmp(next.Cert().SerialNumber) == 0
	}, 10*time.Second, 50*time.Millisecond, "the renewed certificate is served\n%s", run.stderr.String())
}
