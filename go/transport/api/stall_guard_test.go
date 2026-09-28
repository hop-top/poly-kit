package api_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// stallServer serves h behind the guard, with read timeouts far longer
// than any test waits, so only the guard can end a stall promptly. It
// returns the address and the server. onState, when set, observes
// connection states beside the guard; it is installed before the
// server starts, as ConnState must be.
func stallServer(t *testing.T, h http.Handler, protocols *http.Protocols, onState ...func(net.Conn, http.ConnState)) (string, *http.Server) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: time.Minute,
		ReadTimeout:       time.Minute,
		Protocols:         protocols,
	}
	for _, f := range onState {
		srv.ConnState = f
	}
	api.ReleaseStalledOnShutdown(srv)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String(), srv
}

// shutdownTook runs Shutdown with a budget far longer than it should
// need and reports how long it took.
func shutdownTook(t *testing.T, srv *http.Server) time.Duration {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	start := time.Now()
	require.NoError(t, srv.Shutdown(ctx), "Shutdown waited out its budget")
	return time.Since(start)
}

// readAll is a handler that reads its whole body, then answers with its
// length; readErr receives the read's error.
func readAll(readErr chan<- error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if readErr != nil {
			readErr <- err
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = fmt.Fprint(w, len(b))
	})
}

const quick = 2 * time.Second

func TestReleaseStalledOnShutdown_MidHeaderFirstRequest(t *testing.T) {
	addr, srv := stallServer(t, readAll(nil), nil)
	c, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	_, err = io.WriteString(c, "POST / HTTP/1.1\r\nHost: 127.0.0.1")
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	assert.Less(t, shutdownTook(t, srv), quick)
}

func TestReleaseStalledOnShutdown_UnusedConnection(t *testing.T) {
	addr, srv := stallServer(t, readAll(nil), nil)
	c, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	time.Sleep(100 * time.Millisecond)

	assert.Less(t, shutdownTook(t, srv), quick)
}

func TestReleaseStalledOnShutdown_MidHeaderAfterKeepAlive(t *testing.T) {
	addr, srv := stallServer(t, readAll(nil), nil)
	c, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	_, err = io.WriteString(c, "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 2\r\n\r\nhi")
	require.NoError(t, err)
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_, err = io.WriteString(c, "POST / HTTP/1.1\r\nHost: 127.0.0.1")
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	assert.Less(t, shutdownTook(t, srv), quick)
}

func TestReleaseStalledOnShutdown_MidBody(t *testing.T) {
	readErr := make(chan error, 1)
	addr, srv := stallServer(t, readAll(readErr), nil)
	c, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	_, err = io.WriteString(c, "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 100\r\n\r\n{\"par")
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	assert.Less(t, shutdownTook(t, srv), quick)
	select {
	case err := <-readErr:
		assert.Error(t, err, "the stalled read ended in an error")
	case <-time.After(time.Second):
		t.Fatal("the handler never returned from its read")
	}
}

func TestReleaseStalledOnShutdown_MidBodyH2C(t *testing.T) {
	p := new(http.Protocols)
	p.SetHTTP1(true)
	p.SetUnencryptedHTTP2(true)
	readErr := make(chan error, 1)
	addr, srv := stallServer(t, readAll(readErr), p)

	cp := new(http.Protocols)
	cp.SetUnencryptedHTTP2(true)
	client := &http.Client{Transport: &http.Transport{Protocols: cp}}
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	go func() {
		_, _ = io.WriteString(pw, `{"par`)
	}()
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/", pr)
	require.NoError(t, err)
	go func() {
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	time.Sleep(200 * time.Millisecond)

	assert.Less(t, shutdownTook(t, srv), quick)
	select {
	case err := <-readErr:
		assert.Error(t, err, "the stalled stream's read ended in an error")
	case <-time.After(time.Second):
		t.Fatal("the handler never returned from its read")
	}
}

// TestReleaseStalledOnShutdown_LeavesARunningCallAlone pins that a
// request whose body has been read drains to completion with its
// context intact: the guard ends only stalled reads.
func TestReleaseStalledOnShutdown_LeavesARunningCallAlone(t *testing.T) {
	started := make(chan struct{})
	ctxErr := make(chan error, 1)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		close(started)
		time.Sleep(300 * time.Millisecond)
		ctxErr <- r.Context().Err()
		_, _ = fmt.Fprint(w, "done")
	})
	addr, srv := stallServer(t, h, nil)

	type result struct {
		body string
		err  error
	}
	got := make(chan result, 1)
	go func() {
		resp, err := http.Post("http://"+addr+"/", "text/plain", strings.NewReader("payload"))
		if err != nil {
			got <- result{err: err}
			return
		}
		b, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		got <- result{string(b), err}
	}()
	<-started
	shutdownTook(t, srv)

	assert.NoError(t, <-ctxErr, "the running call's context was not canceled")
	r := <-got
	require.NoError(t, r.err)
	assert.Equal(t, "done", r.body)
}

// TestReleaseStalledOnShutdown_KeepAlive200 pins that the guard leaves
// keep-alive intact: 200 requests with bodies reuse one connection.
func TestReleaseStalledOnShutdown_KeepAlive200(t *testing.T) {
	var conns atomic.Int32
	addr, srv := stallServer(t, readAll(nil), nil, func(_ net.Conn, st http.ConnState) {
		if st == http.StateNew {
			conns.Add(1)
		}
	})

	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 1}}
	defer client.CloseIdleConnections()
	reused := 0
	for i := range 200 {
		body := strings.Repeat("x", i+1)
		trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
			if info.Reused {
				reused++
			}
		}}
		req, err := http.NewRequestWithContext(httptrace.WithClientTrace(context.Background(), trace),
			http.MethodPost, "http://"+addr+"/", strings.NewReader(body))
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err, "request %d", i)
		b, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode, "request %d", i)
		require.Equal(t, fmt.Sprint(len(body)), string(b), "request %d", i)
	}
	assert.Equal(t, 199, reused, "every request after the first reused the connection")
	assert.LessOrEqual(t, conns.Load(), int32(1), "one connection served all 200")
	assert.Less(t, shutdownTook(t, srv), quick)
}

// refuse answers 403 without reading the body, as the Host, Origin,
// body-limit and authentication refusals do.
var refuse = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "refused", http.StatusForbidden)
})

// TestReleaseStalledOnShutdown_RefusalBeforeTheBody pins that a request
// refused before its body is read, whose client then stalls mid-body,
// gets its answer and a closed connection at once, rather than a
// connection held while net/http drains the body for the read timeout;
// stopping the server is prompt too.
func TestReleaseStalledOnShutdown_RefusalBeforeTheBody(t *testing.T) {
	addr, srv := stallServer(t, refuse, nil)
	c, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	_, err = io.WriteString(c, "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 100\r\n\r\n{\"par")
	require.NoError(t, err)

	require.NoError(t, c.SetReadDeadline(time.Now().Add(quick)))
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	require.NoError(t, err, "the refusal is answered while the body is stalled")
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.True(t, resp.Close, "the answer says the connection closes")
	_, err = br.ReadByte()
	assert.ErrorIs(t, err, io.EOF, "the server closed the connection, not a read timeout")

	assert.Less(t, shutdownTook(t, srv), quick)
}

// TestReleaseStalledOnShutdown_RefusalClosesTheConnection pins that a
// refused request whose body arrived whole is still answered with
// Connection: close, and the next request gets a fresh connection
// that works: closing replaces the drain, whatever the body's state.
func TestReleaseStalledOnShutdown_RefusalClosesTheConnection(t *testing.T) {
	var conns atomic.Int32
	addr, _ := stallServer(t, refuse, nil, func(_ net.Conn, st http.ConnState) {
		if st == http.StateNew {
			conns.Add(1)
		}
	})
	client := &http.Client{}
	for range 3 {
		resp, err := client.Post("http://"+addr+"/", "application/json", strings.NewReader(`{"small":"body"}`))
		require.NoError(t, err)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
		assert.True(t, resp.Close)
	}
	assert.Equal(t, int32(3), conns.Load(), "each refusal closed its connection")
}
