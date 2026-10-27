// Package bodylimit bounds what a provider response may hold: a whole
// non-streamed body, and one SSE event of a stream. Adapters install
// [Limits.Middleware] on their SDK client; an overrun ends the read
// with an [llmerrors.ErrResponseTooLarge].
package bodylimit

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	llmerrors "hop.top/kit/go/ai/llm/errors"
)

// Default limits; provider params max_body_bytes and max_event_bytes
// override them.
const (
	DefaultBody  = 32 << 20
	DefaultEvent = 8 << 20
)

// Limits are the byte bounds for one provider.
type Limits struct {
	provider    string
	Body, Event int64
}

// FromParams reads max_body_bytes and max_event_bytes from params,
// defaulting each; provider prefixes errors and names the provider of
// an overrun.
func FromParams(provider string, params map[string]string) (Limits, error) {
	l := Limits{provider: provider, Body: DefaultBody, Event: DefaultEvent}
	for _, p := range []struct {
		name string
		dst  *int64
	}{{"max_body_bytes", &l.Body}, {"max_event_bytes", &l.Event}} {
		v, ok := params[p.name]
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil || n <= 0 {
			return Limits{}, fmt.Errorf("%s: %s must be a positive byte count", provider, p.name)
		}
		*p.dst = n
	}
	return l, nil
}

// Middleware bounds every response next returns: an event stream per
// event, any other body whole. An overrun marks the response not to be
// retried. Its signature is the SDKs' option.Middleware.
func (l Limits) Middleware(req *http.Request, next func(*http.Request) (*http.Response, error)) (*http.Response, error) {
	res, err := next(req)
	if res == nil || res.Body == nil {
		return res, err
	}
	if strings.HasPrefix(strings.ToLower(res.Header.Get("Content-Type")), "text/event-stream") {
		res.Body = &eventLimit{rc: res.Body, res: res, l: l}
	} else {
		res.Body = &bodyLimit{rc: res.Body, res: res, l: l}
	}
	return res, err
}

func (l Limits) overrun(res *http.Response, what string, max int64) error {
	// The SDKs read x-should-retry after a dropped body: no retry.
	res.Header.Set("X-Should-Retry", "false")
	return llmerrors.NewResponseTooLarge(l.provider, what, max)
}

type bodyLimit struct {
	rc   io.ReadCloser
	res  *http.Response
	l    Limits
	read int64
}

func (b *bodyLimit) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	b.read += int64(n)
	if b.read > b.l.Body {
		return 0, b.l.overrun(b.res, "body", b.l.Body)
	}
	return n, err
}

func (b *bodyLimit) Close() error { return b.rc.Close() }

// eventLimit counts bytes since the last blank line (an event boundary).
type eventLimit struct {
	rc    io.ReadCloser
	res   *http.Response
	l     Limits
	event int64
	prev  byte
}

func (e *eventLimit) Read(p []byte) (int, error) {
	n, err := e.rc.Read(p)
	for _, c := range p[:n] {
		switch {
		case c == '\n' && e.prev == '\n':
			e.event = 0
		case c == '\r':
			continue
		default:
			e.event++
		}
		e.prev = c
		if e.event > e.l.Event {
			return 0, e.l.overrun(e.res, "event", e.l.Event)
		}
	}
	return n, err
}

func (e *eventLimit) Close() error { return e.rc.Close() }
