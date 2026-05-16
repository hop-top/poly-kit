package rpc_test

import (
	"context"
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
