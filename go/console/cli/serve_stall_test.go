package cli

import (
	"bufio"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stallConn opens a connection to base (http://host:port) and writes
// raw, leaving the request unfinished.
func stallConn(t *testing.T, base, raw string) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", strings.TrimPrefix(base, "http://"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	_, err = io.WriteString(c, raw)
	require.NoError(t, err)
	return c
}

// TestAPIServiceStopIsNotHeldByStalledClients pins that stopping the
// api service does not wait on clients stalled mid-header or mid-body,
// even with read timeouts longer than the stop budget.
func TestAPIServiceStopIsNotHeldByStalledClients(t *testing.T) {
	isolateHome(t)
	r := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	r.Viper.Set("services.api.timeouts.read", "1m")
	r.Viper.Set("services.api.timeouts.read_header", "1m")
	base, stop := serveAPI(t, r)
	host := strings.TrimPrefix(base, "http://")

	stallConn(t, base, "POST /v1/commands/add HTTP/1.1\r\nHost: "+host)
	stallConn(t, base, "POST /v1/commands/add HTTP/1.1\r\nHost: "+host+
		"\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{\"ar")
	time.Sleep(200 * time.Millisecond)

	start := time.Now()
	stop()
	assert.Less(t, time.Since(start), 3*time.Second)
}

// TestAPIServiceRefusalClosesAStalledBody pins that a request refused
// before its body is read — here by the Host check — is answered and
// its connection closed at once, rather than held while net/http
// drains a body the client stalled.
func TestAPIServiceRefusalClosesAStalledBody(t *testing.T) {
	isolateHome(t)
	r := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	r.Viper.Set("services.api.timeouts.read", "1m")
	base, stop := serveAPI(t, r)
	defer stop()

	c := stallConn(t, base, "POST /v1/commands/add HTTP/1.1\r\nHost: evil.example\r\n"+
		"Content-Type: application/json\r\nContent-Length: 100\r\n\r\n{\"ar")
	require.NoError(t, c.SetReadDeadline(time.Now().Add(3*time.Second)))
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	_, err = br.ReadByte()
	assert.ErrorIs(t, err, io.EOF, "the connection closed after the refusal")
}

// TestAPIServiceStopOverTLSIsNotHeldByStalledClients pins the same
// over a TLS listener: the stall guard and ServeTLS compose, so
// stopping does not wait on a connection that never finishes its
// handshake, an HTTP/1 request stalled mid-header or mid-body, or an
// HTTP/2 stream stalled mid-body.
func TestAPIServiceStopOverTLSIsNotHeldByStalledClients(t *testing.T) {
	isolateHome(t)
	f := newTLSFixture(t)
	r := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	setKeys(r, f.tlsKeys(APIServiceName))
	r.Viper.Set("services.api.timeouts.read", "1m")
	r.Viper.Set("services.api.timeouts.read_header", "1m")
	base, stop := serveAPI(t, r)
	host := strings.TrimPrefix(base, "http://")

	// Served over TLS: a request completes.
	resp, _ := tlsGet(t, f.client(), strings.Replace(base, "http://", "https://", 1)+"/healthz")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// No handshake at all.
	stallConn(t, base, "")
	// HTTP/1 over TLS, stalled mid-header and mid-body.
	h1 := func(raw string) {
		cfg := f.ca.ClientTLS()
		cfg.NextProtos = []string{"http/1.1"}
		c, err := tls.Dial("tcp", host, cfg)
		require.NoError(t, err)
		t.Cleanup(func() { _ = c.Close() })
		_, err = io.WriteString(c, raw)
		require.NoError(t, err)
	}
	h1("POST /v1/commands/add HTTP/1.1\r\nHost: " + host)
	h1("POST /v1/commands/add HTTP/1.1\r\nHost: " + host +
		"\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{\"ar")
	// HTTP/2 over TLS, a stream stalled mid-body.
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	go func() { _, _ = io.WriteString(pw, `{"ar`) }()
	req, err := http.NewRequest(http.MethodPost, "https://"+host+"/v1/commands/add", pr)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	go func() {
		if resp, err := f.client().Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()
	time.Sleep(300 * time.Millisecond)

	start := time.Now()
	stop()
	assert.Less(t, time.Since(start), 3*time.Second)
}
