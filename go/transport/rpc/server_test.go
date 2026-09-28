package rpc_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/rpc"
)

func TestServerImplementsHTTPHandler(t *testing.T) {
	srv := rpc.NewServer()
	var _ http.Handler = srv
}

func TestListenAndServeStartsAndShutdown(t *testing.T) {
	srv := rpc.NewServer()

	// Find a free port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	ln.Close()

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- rpc.ListenAndServe(ctx, addr, srv)
	}()

	// Give server time to start.
	time.Sleep(50 * time.Millisecond)

	// Cancel triggers graceful shutdown.
	cancel()

	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("ListenAndServe did not return after context cancel")
	}
}

func TestOptionsApplied(t *testing.T) {
	srv := rpc.NewServer(
		rpc.WithReadTimeout(3*time.Second),
		rpc.WithWriteTimeout(7*time.Second),
		rpc.WithShutdownTimeout(15*time.Second),
	)

	// Server should still be a valid handler.
	var _ http.Handler = srv
}

func TestServerHandle(t *testing.T) {
	srv := rpc.NewServer()
	called := false
	srv.Handle("/test.v1.Svc/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	// Serve a request through the mux.
	req, _ := http.NewRequest("POST", "/test.v1.Svc/Method", nil)
	rec := &fakeResponseWriter{header: http.Header{}}
	srv.ServeHTTP(rec, req)

	assert.True(t, called)
}

type fakeResponseWriter struct {
	header     http.Header
	statusCode int
	body       []byte
}

func (f *fakeResponseWriter) Header() http.Header { return f.header }
func (f *fakeResponseWriter) Write(b []byte) (int, error) {
	f.body = append(f.body, b...)
	return len(b), nil
}
func (f *fakeResponseWriter) WriteHeader(code int) { f.statusCode = code }

// TestListenAndServeServesH2C checks the server answers unencrypted
// HTTP/2 with prior knowledge, the transport native gRPC clients use
// without TLS, and still answers HTTP/1.1.
func TestListenAndServeServesH2C(t *testing.T) {
	srv := rpc.NewServer()
	srv.Handle("/proto", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, r.Proto)
	}))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- rpc.ListenAndServe(ctx, addr, srv) }()
	t.Cleanup(func() {
		cancel()
		<-errCh
	})

	h2c := new(http.Protocols)
	h2c.SetUnencryptedHTTP2(true)
	clients := map[string]*http.Client{
		"HTTP/2.0": {Transport: &http.Transport{Protocols: h2c}},
		"HTTP/1.1": {Transport: &http.Transport{}},
	}
	for want, c := range clients {
		var body string
		require.Eventually(t, func() bool {
			resp, err := c.Get("http://" + addr + "/proto")
			if err != nil {
				return false
			}
			defer func() { _ = resp.Body.Close() }()
			b, _ := io.ReadAll(resp.Body)
			body = string(b)
			return true
		}, 5*time.Second, 20*time.Millisecond)
		assert.Equal(t, want, body)
	}
}

// TestHTTPServerCarriesTheConfiguredServer pins that HTTPServer is the
// server ListenAndServe runs: the handler, both timeouts, and h2c
// beside HTTP/1.1.
func TestHTTPServerCarriesTheConfiguredServer(t *testing.T) {
	srv := rpc.NewServer(rpc.WithReadTimeout(3*time.Second), rpc.WithWriteTimeout(7*time.Second))
	hs := srv.HTTPServer()
	assert.Same(t, srv, hs.Handler)
	assert.Equal(t, 3*time.Second, hs.ReadTimeout)
	assert.Equal(t, 7*time.Second, hs.WriteTimeout)
	require.NotNil(t, hs.Protocols)
	assert.True(t, hs.Protocols.HTTP1())
	assert.True(t, hs.Protocols.UnencryptedHTTP2())
	assert.Empty(t, hs.Addr)
}
