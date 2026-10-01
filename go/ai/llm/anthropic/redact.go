package anthropic

import (
	"fmt"
	"net/http"
	"net/url"

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
