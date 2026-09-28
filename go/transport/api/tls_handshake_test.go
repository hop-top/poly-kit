package api_test

import (
	"bytes"
	"crypto/tls"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

func TestHandshakeErrorLogSplitsHandshakeFailures(t *testing.T) {
	var next bytes.Buffer
	type failure struct{ addr, reason string }
	var got []failure
	l := api.HandshakeErrorLog(log.New(&next, "", 0), func(addr, reason string) {
		got = append(got, failure{addr, reason})
	})

	l.Printf("http: TLS handshake error from %s: %v", "[::1]:52011", "remote error: tls: bad certificate")
	l.Printf("http: TLS handshake error from %s: %v", "192.0.2.7:4000", "EOF")
	l.Printf("http: Accept error: %v; retrying in %v", "too many open files", "5ms")

	assert.Equal(t, []failure{
		{"[::1]:52011", "remote error: tls: bad certificate"},
		{"192.0.2.7:4000", "EOF"},
	}, got)
	assert.Equal(t, "http: Accept error: too many open files; retrying in 5ms\n", next.String(),
		"every other line reaches the previous log unchanged")
}

// TestHandshakeErrorLogOnAServer pins the line format against
// net/http itself: a plaintext request to a TLS server is reported to
// the hook, and not to the standard logger.
func TestHandshakeErrorLogOnAServer(t *testing.T) {
	var mu sync.Mutex
	var reasons []string
	var next bytes.Buffer
	srv := httptest.NewUnstartedServer(http.NotFoundHandler())
	srv.Config.ErrorLog = api.HandshakeErrorLog(log.New(&next, "", 0), func(_, reason string) {
		mu.Lock()
		defer mu.Unlock()
		reasons = append(reasons, reason)
	})
	srv.StartTLS()
	defer srv.Close()

	resp, err := http.Get(strings.Replace(srv.URL, "https://", "http://", 1))
	require.NoError(t, err)
	_ = resp.Body.Close()
	_, err = (&http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}}).Get(srv.URL)
	require.Error(t, err, "the client does not trust the test server")

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(reasons) == 2
	}, 5*time.Second, 10*time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	all := strings.Join(reasons, "\n")
	assert.Contains(t, all, "client sent an HTTP request to an HTTPS server")
	assert.Contains(t, all, "bad certificate")
	assert.Empty(t, next.String())
}
