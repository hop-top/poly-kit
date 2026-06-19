package httpcache

import (
	"net/http"
	"strings"
)

// cacheableRequest reports whether req is eligible for caching. Only
// GET is cached, and an explicit no-store on the request opts it out.
//
// This is part of the cross-language contract: the cases here are
// mirrored by contracts/httpcache-v1/cacheability.json so the TS and
// Python ports gate identically.
func cacheableRequest(req *http.Request) bool {
	if req.Method != http.MethodGet {
		return false
	}
	return !hasNoStore(req.Header)
}

// cacheableResponse reports whether resp may be stored: 2xx only, and a
// no-store on the response opts it out.
func cacheableResponse(resp *http.Response) bool {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	return !hasNoStore(resp.Header)
}

// hasNoStore reports whether any Cache-Control header carries a
// no-store directive. Directive matching is case-insensitive and
// token-bounded so "no-store-foo" does not match.
func hasNoStore(h http.Header) bool {
	for _, v := range h.Values("Cache-Control") {
		for _, tok := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(tok), "no-store") {
				return true
			}
		}
	}
	return false
}
