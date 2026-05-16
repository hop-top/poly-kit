package client

import (
	"net/http"
	"time"
)

// Option configures a Client at construction. Functional options
// preserve compat as new fields land — adopters who don't set the
// option get the documented default.
type Option func(*Client)

// WithToken sets the bearer token used for svc authentication. The
// token is never logged verbatim; debug output shows only a SHA-256
// fingerprint prefix (see redactToken).
func WithToken(t string) Option { return func(c *Client) { c.token = t } }

// WithHTTPClient injects an *http.Client. Default is a fresh
// http.Client{} with no per-request Timeout (ctx controls). Adopter
// supply is honored verbatim; if the adopter wants a per-request
// deadline, set Timeout on the injected client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.http = h
		}
	}
}

// WithUserAgent overrides the default User-Agent
// ("kit-conformance-client/<ver>"). Adopters embedding the client
// inside their own tool may prefer to identify themselves.
func WithUserAgent(ua string) Option {
	return func(c *Client) {
		if ua != "" {
			c.userAgent = ua
		}
	}
}

// WithMaxAttempts caps the retry loop. The first attempt counts;
// WithMaxAttempts(3) means initial + 2 retries. Values < 1 are
// silently clamped to 1 (zero retries).
func WithMaxAttempts(n int) Option {
	return func(c *Client) {
		if n < 1 {
			n = 1
		}
		c.maxAttempts = n
	}
}

// WithBackoff replaces the default exponential backoff. All four
// parameters are honored verbatim; sub-millisecond values are
// allowed but discouraged. Jitter is fractional in [0, 1).
func WithBackoff(initial, maxBackoff time.Duration, multiplier, jitter float64) Option {
	return func(c *Client) {
		c.backoff = backoffPolicy{
			InitialBackoff:    initial,
			MaxBackoff:        maxBackoff,
			BackoffMultiplier: multiplier,
			BackoffJitter:     jitter,
		}
	}
}

// WithMaxCassetteSize caps the packed cassette body. Pack returns
// ErrCassetteTooLarge when the streamed bytes exceed the cap. Default
// is 50 MiB.
func WithMaxCassetteSize(n int64) Option {
	return func(c *Client) {
		if n > 0 {
			c.maxCassette = n
		}
	}
}
