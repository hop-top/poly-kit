// Package webhooksink delivers bus events to an HTTP endpoint as
// notification webhooks. Events are rendered through a Template,
// optionally redacted, and POSTed to the configured URL.
//
// Pipeline order on Drain (per the guardrail integration convention
// documented in go/runtime/notify/guardrails.go):
//
//	template render → redactor.ApplyBytes → breaker → http POST
//
// The breaker integration lives at the http.RoundTripper layer:
// WithBreaker wraps client.Transport via breaker.WrapHTTP, so an
// open circuit short-circuits before any HTTP egress and surfaces
// breaker.ErrBrokenCircuit through client.Do. The surrounding
// RetrySink (P2) treats ErrBrokenCircuit as terminal via errors.Is.
//
// Spec: docs/specs/notifications.md §3 #9–#11, §7.5, §8.1.
package webhooksink

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"hop.top/kit/go/core/breaker"
	"hop.top/kit/go/core/redact"
	"hop.top/kit/go/runtime/bus"
)

// defaultTimeout is the default overall request deadline applied via
// http.Client.Timeout when the caller has not supplied a custom
// http.Client. Per spec §8.1.
const defaultTimeout = 5 * time.Second

// Sink POSTs rendered bus events to the configured URL. Construct via
// New; zero-value is not usable.
type Sink struct {
	url      string
	headers  http.Header
	template Template
	client   *http.Client
	redactor *redact.Redactor // nil = no-op
	// Note: the breaker integration lives on client.Transport via
	// breaker.WrapHTTP — there is no separate breaker field. An open
	// circuit short-circuits the RoundTripper and surfaces
	// breaker.ErrBrokenCircuit through client.Do.
}

// compile-time interface check.
var _ bus.Sink = (*Sink)(nil)

// Option configures a Sink at construction time. Options apply in
// the order passed to New; later options override earlier ones for
// the same setting.
type Option func(*opts)

// opts is the package-local builder used by Option closures during
// construction. Kept unexported so only Option helpers in this
// package can mutate it.
type opts struct {
	headers  http.Header
	template Template
	client   *http.Client
	timeout  time.Duration
	redactor *redact.Redactor
	breaker  breaker.Breaker
}

// WithHeader adds an HTTP header to every outgoing request. Multiple
// calls accumulate, and multiple values for the same key are
// preserved (each WithHeader call is an Add, not a Set).
func WithHeader(k, v string) Option {
	return func(o *opts) {
		if o.headers == nil {
			o.headers = http.Header{}
		}
		o.headers.Add(k, v)
	}
}

// WithAuthBearer sets an Authorization: Bearer <token> header. Sugar
// for the common bearer-token case; equivalent to
// WithHeader("Authorization", "Bearer "+token).
func WithAuthBearer(token string) Option {
	return WithHeader("Authorization", "Bearer "+token)
}

// WithTemplate overrides the default JSON template. The template
// renders the body bytes and content-type for each event.
func WithTemplate(t Template) Option {
	return func(o *opts) {
		if t != nil {
			o.template = t
		}
	}
}

// WithHTTPClient overrides the default *http.Client. When set,
// WithTimeout is ignored — the caller is responsible for the
// client's timeout/transport configuration. WithBreaker still
// applies (it wraps the supplied client's Transport).
func WithHTTPClient(c *http.Client) Option {
	return func(o *opts) {
		if c != nil {
			o.client = c
		}
	}
}

// WithTimeout sets the overall request deadline applied via
// http.Client.Timeout. Ignored when WithHTTPClient is also supplied.
// Default is 5 * time.Second per spec §8.1.
func WithTimeout(d time.Duration) Option {
	return func(o *opts) {
		o.timeout = d
	}
}

// WithRedactor wraps the rendered payload through r before egress.
// Default nil = no-op. Per the guardrail convention, the redactor
// runs after Template.Render and before the HTTP POST so the
// redactor sees exactly the wire payload that would otherwise leave
// the process. See go/runtime/notify/guardrails.go.
func WithRedactor(r *redact.Redactor) Option {
	return func(o *opts) {
		o.redactor = r
	}
}

// WithBreaker gates HTTP egress through b by wrapping the
// http.RoundTripper via breaker.WrapHTTP. Default nil = no-op. When
// the circuit is open, client.Do returns an error wrapping
// breaker.ErrBrokenCircuit; Drain returns that error unwrapped so
// callers (RetrySink, ad-hoc) can detect terminal state via
// errors.Is.
func WithBreaker(b breaker.Breaker) Option {
	return func(o *opts) {
		o.breaker = b
	}
}

// New returns a webhook sink that POSTs rendered bus events to url.
// Construction has no IO and cannot fail (per spec decision #9).
//
// Defaults applied when the corresponding Option is not supplied:
//
//   - Template: DefaultJSONTemplate (whole bus.Event as JSON).
//   - HTTP client: a fresh *http.Client with Timeout = 5s. When
//     WithBreaker is set, the client's Transport is wrapped via
//     breaker.WrapHTTP starting from http.DefaultTransport.
//   - Redactor: nil (no redaction).
//   - Breaker: nil (no gating).
func New(url string, options ...Option) bus.Sink {
	o := &opts{
		template: DefaultJSONTemplate(),
		timeout:  defaultTimeout,
	}
	for _, opt := range options {
		if opt != nil {
			opt(o)
		}
	}

	// Build the http.Client. If the caller supplied one, leave it
	// alone except to wrap its Transport when a breaker is set.
	client := o.client
	if client == nil {
		client = &http.Client{Timeout: o.timeout}
	}
	if o.breaker != nil {
		base := client.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		client.Transport = breaker.WrapHTTP(o.breaker, base)
	}

	return &Sink{
		url:      url,
		headers:  o.headers,
		template: o.template,
		client:   client,
		redactor: o.redactor,
	}
}

// Drain renders e through the configured Template, runs the rendered
// body through the redactor (if set), and POSTs it to the configured
// URL. Non-2xx responses produce an error containing the status code
// and up to 512 bytes of the response body for diagnostics.
//
// Errors from client.Do (transport failure, breaker short-circuit,
// context deadline) are returned wrapped with %w so errors.Is keeps
// working — in particular errors.Is(err, breaker.ErrBrokenCircuit)
// remains true when the circuit is open, which the surrounding
// RetrySink uses to decide that retry is futile.
func (s *Sink) Drain(ctx context.Context, e bus.Event) error {
	body, contentType, err := s.template.Render(e)
	if err != nil {
		return fmt.Errorf("webhook: render: %w", err)
	}
	if s.redactor != nil {
		body = s.redactor.ApplyBytes(body)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook: build request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	for k, vs := range s.headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	resp, err := s.client.Do(req)
	if err != nil {
		// breaker.ErrBrokenCircuit (and other transport errors) come
		// through here. We wrap with %w so errors.Is still detects
		// the sentinel for RetrySink's open-circuit-is-terminal rule.
		return fmt.Errorf("webhook: post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Limit-read the body so a misbehaving server can't dump
		// megabytes into our error string.
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("webhook: http %d: %s", resp.StatusCode, bytes.TrimSpace(preview))
	}
	return nil
}

// Close is a no-op. The sink does not own the http.Client (callers
// may have supplied their own via WithHTTPClient) and there are no
// other resources to release. Returning nil keeps the bus.Sink
// contract simple.
func (s *Sink) Close() error { return nil }
