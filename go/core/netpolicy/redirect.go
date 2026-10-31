package netpolicy

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ErrCrossOriginHeaders is matched (errors.Is) by the *RedirectError a
// guarded client returns when a redirect hop would leave the request's
// origin while the request carries a header outside the cross-origin
// allowlist.
var ErrCrossOriginHeaders = errors.New("request carries headers that may not leave their origin")

// crossOriginHeaders is every header a hop to another origin may carry,
// in canonical form. Anything else on the request (Authorization,
// Proxy-Authorization, Cookie, X-Api-Key, or a custom header that holds
// identity just as well) cannot be told safe from sensitive by name, so
// a request carrying one is refused at the first hop that leaves its
// origin, never stripped. net/http strips only Authorization, Cookie and
// a few others on its own and would forward the rest.
var crossOriginHeaders = map[string]bool{
	"Accept":          true,
	"Accept-Encoding": true,
	"Accept-Language": true,
	"User-Agent":      true,
	"Content-Type":    true,
}

// RedirectError is the typed refusal of a redirect hop that would carry
// non-allowlisted headers to another origin. Nothing was sent to the
// target. From and To are scheme, host and path only, never query,
// fragment or userinfo. It wraps ErrCrossOriginHeaders.
type RedirectError struct {
	From, To string
}

func (e *RedirectError) Error() string {
	return fmt.Sprintf("redirect from %s to %s refused: %v", e.From, e.To, ErrCrossOriginHeaders)
}

func (e *RedirectError) Unwrap() error { return ErrCrossOriginHeaders }

// maxRedirects is net/http's own default hop limit.
const maxRedirects = 10

// CheckRedirect returns an http.Client.CheckRedirect that refuses a hop
// to another origin when the original request carried any header
// outside Accept, Accept-Encoding, Accept-Language, User-Agent and
// Content-Type. The refusal is a *RedirectError, audited (op
// "redirect") through WithAudit. Same-origin hops are unaffected.
//
// next, when non-nil, runs after the header check passes and decides
// the rest (a hop limit, http.ErrUseLastResponse); the header check
// cannot be bypassed by it. A nil next applies net/http's default
// limit of 10 hops.
//
// Origin is scheme, host and port, with 80 and 443 implied for http and
// https.
func CheckRedirect(next func(req *http.Request, via []*http.Request) error) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 {
			first := via[0]
			if !sameOrigin(first.URL, req.URL) && carriesUnsafeHeaders(first.Header) {
				err := &RedirectError{From: destination(first.URL), To: destination(req.URL)}
				audit(req.Context(), ErrCrossOriginHeaders, "redirect", err.To)
				return err
			}
		}
		if next != nil {
			return next(req, via)
		}
		if len(via) >= maxRedirects {
			return fmt.Errorf("stopped after %d redirects", maxRedirects)
		}
		return nil
	}
}

// NewClient returns an http.Client whose transport is Guard(base) (nil
// base is http.DefaultTransport) and whose redirects are checked by
// CheckRedirect(nil): every hop is judged by the policy, and a hop to
// another origin is refused when the request carries a header that may
// not leave its origin.
//
// A caller that replaces the returned client's CheckRedirect drops the
// header check; compose with CheckRedirect(next) instead.
func NewClient(base http.RoundTripper) *http.Client {
	return &http.Client{Transport: Guard(base), CheckRedirect: CheckRedirect(nil)}
}

func carriesUnsafeHeaders(h http.Header) bool {
	for k := range h {
		if !crossOriginHeaders[http.CanonicalHeaderKey(k)] {
			return true
		}
	}
	return false
}

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		originPort(a) == originPort(b)
}

func originPort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "ws":
		return "80"
	case "https", "wss":
		return "443"
	}
	return ""
}
