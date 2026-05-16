package breaker_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/breaker"
)

func TestWrap_PassesThroughWhenAllowed(t *testing.T) {
	const name = "test-wrap-ok"
	t.Cleanup(func() { breaker.Unregister(name) })

	b := breaker.New(name)
	ran := false
	wrapped := breaker.Wrap(b, func() error {
		ran = true
		return nil
	})
	assert.NoError(t, wrapped())
	assert.True(t, ran)
}

func TestWrap_ShortCircuitsWhenTripped(t *testing.T) {
	const name = "test-wrap-tripped"
	t.Cleanup(func() { breaker.Unregister(name) })

	b := breaker.New(name)
	b.Trip("test")
	ran := false
	wrapped := breaker.Wrap(b, func() error {
		ran = true
		return nil
	})
	assert.ErrorIs(t, wrapped(), breaker.ErrBrokenCircuit)
	assert.False(t, ran)
}

func TestWrapErr_SingleShot(t *testing.T) {
	const name = "test-wrap-err"
	t.Cleanup(func() { breaker.Unregister(name) })

	b := breaker.New(name)
	err := breaker.WrapErr(b, func() error { return nil })
	assert.NoError(t, err)
}

func TestWrapValue_ReturnsValueOnAllow(t *testing.T) {
	const name = "test-wrap-value"
	t.Cleanup(func() { breaker.Unregister(name) })

	b := breaker.New(name)
	got, err := breaker.WrapValue(b, func() (int, error) { return 42, nil })
	require.NoError(t, err)
	assert.Equal(t, 42, got)
}

func TestWrapValue_ZeroValueOnTrip(t *testing.T) {
	const name = "test-wrap-value-trip"
	t.Cleanup(func() { breaker.Unregister(name) })

	b := breaker.New(name)
	b.Trip("test")
	got, err := breaker.WrapValue(b, func() (int, error) { return 42, nil })
	assert.Zero(t, got)
	assert.ErrorIs(t, err, breaker.ErrBrokenCircuit)
}

func TestWrapCtx_PropagatesCancel(t *testing.T) {
	const name = "test-wrap-ctx"
	t.Cleanup(func() { breaker.Unregister(name) })

	b := breaker.New(name)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := breaker.WrapCtx(b, ctx, func(c context.Context) error {
		select {
		case <-c.Done():
			return c.Err()
		default:
			return nil
		}
	})
	assert.ErrorIs(t, err, context.Canceled)
}

func TestWrapBytes_RecordsLength(t *testing.T) {
	const name = "test-wrap-bytes"
	t.Cleanup(func() { breaker.Unregister(name) })

	b := breaker.New(name)
	out, err := breaker.WrapBytes(b, func() ([]byte, error) {
		return []byte("hello, world"), nil
	})
	require.NoError(t, err)
	assert.Equal(t, "hello, world", string(out))
	assert.Equal(t, int64(12), b.Stats().Counters["bytes"])
}

func TestWrapWriter_RecordsAndShortCircuits(t *testing.T) {
	const name = "test-wrap-writer"
	t.Cleanup(func() { breaker.Unregister(name) })

	b := breaker.New(name)
	buf := &bytes.Buffer{}
	w := breaker.WrapWriter(b, buf)

	n, err := w.Write([]byte("abcdef"))
	require.NoError(t, err)
	assert.Equal(t, 6, n)
	assert.Equal(t, int64(6), b.Stats().Counters["bytes"])

	b.Trip("test")
	_, err = w.Write([]byte("ghi"))
	assert.ErrorIs(t, err, breaker.ErrBrokenCircuit)
}

func TestWrapReader_RecordsRead(t *testing.T) {
	const name = "test-wrap-reader"
	t.Cleanup(func() { breaker.Unregister(name) })

	b := breaker.New(name)
	src := bytes.NewReader([]byte("123456789"))
	r := breaker.WrapReader(b, src)

	out, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, "123456789", string(out))
	assert.Equal(t, int64(9), b.Stats().Counters["bytes"])
}

func TestWrapHTTP_AllowsAndRecordsContentLength(t *testing.T) {
	const name = "test-wrap-http"
	t.Cleanup(func() { breaker.Unregister(name) })

	b := breaker.New(name)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	rt := breaker.WrapHTTP(b, http.DefaultTransport)
	client := &http.Client{Transport: rt}

	resp, err := client.Get(server.URL)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, "ok", string(body))
	// content-length 2 should be recorded
	assert.GreaterOrEqual(t, b.Stats().Counters["bytes"], int64(2))
}

func TestWrapHTTP_ShortCircuitsWhenTripped(t *testing.T) {
	const name = "test-wrap-http-trip"
	t.Cleanup(func() { breaker.Unregister(name) })

	b := breaker.New(name)
	b.Trip("test")

	rt := breaker.WrapHTTP(b, http.DefaultTransport)
	client := &http.Client{Transport: rt}

	_, err := client.Get("http://example.invalid")
	assert.Error(t, err)
	// real RoundTrip never runs because Allow short-circuits first
	assert.True(t, errors.Is(err, breaker.ErrBrokenCircuit) ||
		errors.Is(unwrapURLErr(err), breaker.ErrBrokenCircuit))
}

// unwrapURLErr handles net/http's url.Error wrapper.
func unwrapURLErr(err error) error {
	var u interface{ Unwrap() error }
	if errors.As(err, &u) {
		return u.Unwrap()
	}
	return err
}
