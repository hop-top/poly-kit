package anthropic

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/option"

	"hop.top/kit/go/ai/llm"
)

// redactRequestURL masks, with [llm.RedactURI], the credentials in
// req's URL. The SDK quotes the request URL, userinfo and query
// included, in every API error; a base URL carrying a credential would
// otherwise surface in the error message.
func redactRequestURL(req *http.Request) {
	if req == nil || req.URL == nil {
		return
	}
	u, err := url.Parse(llm.RedactURI(req.URL.String()))
	if err != nil {
		u = &url.URL{Scheme: req.URL.Scheme, Host: req.URL.Host, Path: req.URL.Path}
	}
	req.URL = u
}

// checkBaseURL rejects a base URL the SDK cannot parse. The SDK would
// report it on every request with the raw URL flattened into a string
// no caller can mask; this error carries it masked by [llm.RedactURI].
// what names the setting in the message ("base URL", an env var).
func checkBaseURL(what, base string) error {
	if _, err := url.Parse(base); err != nil {
		return fmt.Errorf("anthropic: invalid %s: %w", what, llm.RedactURLError(err))
	}
	return nil
}

// maxRetryAfter is the longest wait a server may ask for between
// retries, as the Anthropic SDKs cap it.
const maxRetryAfter = 60 * time.Second

// boundRetryAfter drops a Retry-After-Ms or Retry-After header asking
// for a wait outside (0, maxRetryAfter], so the SDK falls back to its
// own backoff: a hostile or broken server cannot stall the client with
// a huge, negative, NaN or unparsable value.
func boundRetryAfter(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
	res, err := next(req)
	if res != nil {
		for _, h := range []struct {
			name string
			unit time.Duration
		}{{"Retry-After-Ms", time.Millisecond}, {"Retry-After", time.Second}} {
			if v := res.Header.Get(h.name); v != "" && !retryAfterInRange(v, h.unit) {
				res.Header.Del(h.name)
			}
		}
	}
	return res, err
}

func retryAfterInRange(v string, unit time.Duration) bool {
	if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
		max := float64(maxRetryAfter) / float64(unit)
		return f > 0 && f <= max // false for NaN
	}
	if unit == time.Second {
		if t, err := time.Parse(time.RFC1123, v); err == nil {
			d := time.Until(t)
			return d > 0 && d <= maxRetryAfter
		}
	}
	return false
}
