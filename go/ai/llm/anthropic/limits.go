package anthropic

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/option"

	llmerrors "hop.top/kit/go/ai/llm/errors"
)

// Default response limits: a whole non-streamed body, and one SSE event
// of a stream. Provider params max_body_bytes and max_event_bytes
// override them.
const (
	defaultMaxBodyBytes  = 32 << 20
	defaultMaxEventBytes = 8 << 20
)

type limits struct{ body, event int64 }

func limitsFrom(params map[string]string) (limits, error) {
	l := limits{body: defaultMaxBodyBytes, event: defaultMaxEventBytes}
	for name, dst := range map[string]*int64{"max_body_bytes": &l.body, "max_event_bytes": &l.event} {
		v, ok := params[name]
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil || n <= 0 {
			return limits{}, fmt.Errorf("anthropic: %s must be a positive byte count", name)
		}
		*dst = n
	}
	return l, nil
}

// middleware bounds what a response may hold: an event stream per
// event, any other body whole. An overrun ends the read with an
// [llmerrors.ErrResponseTooLarge] and marks the response not to be
// retried.
func (l limits) middleware(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
	res, err := next(req)
	if res == nil || res.Body == nil {
		return res, err
	}
	if strings.HasPrefix(strings.ToLower(res.Header.Get("Content-Type")), "text/event-stream") {
		res.Body = &eventLimit{rc: res.Body, res: res, max: l.event}
	} else {
		res.Body = &bodyLimit{rc: res.Body, res: res, max: l.body}
	}
	return res, err
}

func overrun(res *http.Response, what string, max int64) error {
	// The SDK reads x-should-retry after a dropped body: no retry.
	res.Header.Set("X-Should-Retry", "false")
	return llmerrors.NewResponseTooLarge(scheme, what, max)
}

type bodyLimit struct {
	rc   io.ReadCloser
	res  *http.Response
	max  int64
	read int64
}

func (b *bodyLimit) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	b.read += int64(n)
	if b.read > b.max {
		return 0, overrun(b.res, "body", b.max)
	}
	return n, err
}

func (b *bodyLimit) Close() error { return b.rc.Close() }

// eventLimit counts bytes since the last blank line (an event boundary).
type eventLimit struct {
	rc    io.ReadCloser
	res   *http.Response
	max   int64
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
		if e.event > e.max {
			return 0, overrun(e.res, "event", e.max)
		}
	}
	return n, err
}

func (e *eventLimit) Close() error { return e.rc.Close() }

// secretsOf lists the non-blank credential values to scrub.
func secretsOf(vals ...string) []string {
	var out []string
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

// scrub returns err with every secret replaced by REDACTED in its
// message; errors.Is and errors.As still reach err.
func scrub(err error, secrets []string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	clean := msg
	for _, s := range secrets {
		clean = strings.ReplaceAll(clean, s, "REDACTED")
	}
	if clean == msg {
		return err
	}
	return &scrubbed{msg: clean, err: err}
}

type scrubbed struct {
	msg string
	err error
}

func (s *scrubbed) Error() string { return s.msg }
func (s *scrubbed) Unwrap() error { return s.err }

// Format keeps %+v and %#v to the scrubbed message.
func (s *scrubbed) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, s.msg) }
