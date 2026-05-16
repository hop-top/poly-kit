package breaker

import (
	"context"
	"io"
	"net/http"
)

// Wrap returns a closure that gates fn behind b.Allow. Useful for
// hooks/handlers that the caller passes around as a func() error.
func Wrap(b Breaker, fn func() error) func() error {
	return func() error {
		if err := b.Allow(); err != nil {
			return err
		}
		err := fn()
		b.Record(err == nil, 0)
		return err
	}
}

// WrapErr is a single-shot variant: call b.Allow then fn directly,
// no closure allocation. Returns ErrBrokenCircuit if not allowed.
func WrapErr(b Breaker, fn func() error) error {
	if err := b.Allow(); err != nil {
		return err
	}
	err := fn()
	b.Record(err == nil, 0)
	return err
}

// WrapValue runs fn through b.Allow and returns its (T, error). On
// trip the zero value of T is returned with ErrBrokenCircuit.
func WrapValue[T any](b Breaker, fn func() (T, error)) (T, error) {
	var zero T
	if err := b.Allow(); err != nil {
		return zero, err
	}
	v, err := fn()
	b.Record(err == nil, 0)
	return v, err
}

// WrapCtx propagates ctx into fn. Cancel/deadline are honored by
// fn itself; Allow is checked once before the call.
func WrapCtx(b Breaker, ctx context.Context, fn func(context.Context) error) error {
	if err := b.Allow(); err != nil {
		return err
	}
	err := fn(ctx)
	b.Record(err == nil, 0)
	return err
}

// WrapBytes records n=len(out) on success so MaxBytes counters tick.
func WrapBytes(b Breaker, fn func() ([]byte, error)) ([]byte, error) {
	if err := b.Allow(); err != nil {
		return nil, err
	}
	out, err := fn()
	if err != nil {
		b.Record(false, 0)
		return nil, err
	}
	b.Record(true, int64(len(out)))
	return out, nil
}

// WrapWriter returns an io.Writer that gates each Write through
// b.Allow and records n=len(p) on success.
func WrapWriter(b Breaker, w io.Writer) io.Writer {
	return &breakerWriter{b: b, w: w}
}

type breakerWriter struct {
	b Breaker
	w io.Writer
}

func (bw *breakerWriter) Write(p []byte) (int, error) {
	if err := bw.b.Allow(); err != nil {
		return 0, err
	}
	n, err := bw.w.Write(p)
	bw.b.Record(err == nil, int64(n))
	return n, err
}

// WrapReader returns an io.Reader that gates each Read through
// b.Allow and records bytes read.
func WrapReader(b Breaker, r io.Reader) io.Reader {
	return &breakerReader{b: b, r: r}
}

type breakerReader struct {
	b Breaker
	r io.Reader
}

func (br *breakerReader) Read(p []byte) (int, error) {
	if err := br.b.Allow(); err != nil {
		return 0, err
	}
	n, err := br.r.Read(p)
	// EOF is not a failure; only record success on real errors.
	success := err == nil || err == io.EOF
	br.b.Record(success, int64(n))
	return n, err
}

// WrapHTTP gates an http.RoundTripper through b.Allow on each call
// and records Content-Length when known.
//
// Note: failsafe-go ships failsafehttp with a more featureful
// RoundTripper that composes failsafe executors directly. Callers
// wanting that should compose failsafehttp.RoundTripper separately
// and wire its executor into the same kit/breaker via b.Executor().
func WrapHTTP(b Breaker, rt http.RoundTripper) http.RoundTripper {
	return &breakerTransport{b: b, rt: rt}
}

type breakerTransport struct {
	b  Breaker
	rt http.RoundTripper
}

func (bt *breakerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := bt.b.Allow(); err != nil {
		return nil, err
	}
	resp, err := bt.rt.RoundTrip(req)
	if err != nil {
		bt.b.Record(false, 0)
		return nil, err
	}
	// Content-Length may be -1 when unknown; clamp to 0 to avoid
	// negative bookkeeping.
	n := resp.ContentLength
	if n < 0 {
		n = 0
	}
	bt.b.Record(true, n)
	return resp, nil
}
