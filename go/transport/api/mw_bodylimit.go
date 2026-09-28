package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
)

// DefaultMaxBodyBytes is the request body cap kit's HTTP entry points
// apply when given no limit of their own: 1 MiB. It is sized for a
// command invocation — flags, positional args, a JSON document —
// not for uploads; an adopter that accepts larger payloads raises
// the cap for that entry point explicitly.
const DefaultMaxBodyBytes int64 = 1 << 20

// CodeBodyTooLarge is the stable reason a body-limit refusal
// carries in the [APIError] body, alongside HTTP 413.
const CodeBodyTooLarge = "body_too_large"

// MaxBodyBytesOrDefault resolves a configured body cap: zero means
// [DefaultMaxBodyBytes], a negative value means no limit, anything
// else is the cap itself. Every kit entry point that takes a body
// limit resolves it through here, so the zero value of a config
// struct is always the safe default.
func MaxBodyBytesOrDefault(n int64) int64 {
	if n == 0 {
		return DefaultMaxBodyBytes
	}
	return n
}

// BodyLimitOption configures [BodyLimit].
type BodyLimitOption func(*bodyLimitConfig)

type bodyLimitConfig struct {
	onTooLarge func(r *http.Request, limit int64)
}

// OnBodyTooLarge installs a hook that observes every request whose
// body exceeds the cap, with the cap. It fires once per request:
// before the 413 is written when a declared Content-Length is
// already over, or on the read that crosses the cap for a body of
// unknown length (chunked, HTTP/2). It exists so a refusal can be
// recorded in the same audit trail as the calls that were allowed;
// the hook cannot change the verdict.
func OnBodyTooLarge(fn func(r *http.Request, limit int64)) BodyLimitOption {
	return func(c *bodyLimitConfig) { c.onTooLarge = fn }
}

// BodyLimit returns a middleware that caps the request body at
// maxBytes, resolved through [MaxBodyBytesOrDefault] (zero is
// [DefaultMaxBodyBytes], negative disables the cap).
//
// A request whose Content-Length already exceeds the cap is refused
// with 413 and a [CodeBodyTooLarge] body before any handler runs.
// Any other body is wrapped in [http.MaxBytesReader], so a body of
// unknown length is stopped on the read that crosses the cap: the
// handler's read fails with an [*http.MaxBytesError], which
// [AsBodyTooLarge] recognizes and [WriteBodyTooLarge] renders. The
// server also closes the connection after the response rather than
// draining the rest.
//
// Place it before any middleware or handler that reads the body.
func BodyLimit(maxBytes int64, opts ...BodyLimitOption) Middleware {
	limit := MaxBodyBytesOrDefault(maxBytes)
	var cfg bodyLimitConfig
	for _, o := range opts {
		o(&cfg)
	}
	return func(next http.Handler) http.Handler {
		if limit < 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > limit {
				if cfg.onTooLarge != nil {
					cfg.onTooLarge(r, limit)
				}
				WriteBodyTooLarge(w, limit)
				return
			}
			if r.Body != nil && r.Body != http.NoBody {
				body := http.MaxBytesReader(w, r.Body, limit)
				if cfg.onTooLarge != nil {
					body = &observedBody{ReadCloser: body, fire: func() { cfg.onTooLarge(r, limit) }}
				}
				r.Body = body
			}
			next.ServeHTTP(w, r)
		})
	}
}

// observedBody reports the first read that fails on the body cap.
// The limiting itself is http.MaxBytesReader's; this only watches
// its error so the refusal hook fires for bodies of unknown length.
type observedBody struct {
	io.ReadCloser
	once sync.Once
	fire func()
}

func (b *observedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		if _, over := AsBodyTooLarge(err); over {
			b.once.Do(b.fire)
		}
	}
	return n, err
}

// AsBodyTooLarge reports whether err is (or wraps) the error a
// capped body returns once the cap is crossed, and the cap.
func AsBodyTooLarge(err error) (limit int64, ok bool) {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return mbe.Limit, true
	}
	return 0, false
}

// WriteBodyTooLarge writes the 413 refusal every kit HTTP entry point
// uses for an oversized body: an [APIError] with code
// [CodeBodyTooLarge] naming the cap.
func WriteBodyTooLarge(w http.ResponseWriter, limit int64) {
	Error(w, http.StatusRequestEntityTooLarge, &APIError{
		Status:  http.StatusRequestEntityTooLarge,
		Code:    CodeBodyTooLarge,
		Message: fmt.Sprintf("request body exceeds %d bytes", limit),
	})
}
