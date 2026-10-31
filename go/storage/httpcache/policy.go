package httpcache

import (
	"net/http"
	"sort"
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

// hasDirective reports whether any Cache-Control header carries the named
// directive (case-insensitive, token-bounded, with or without "=value").
func hasDirective(h http.Header, name string) bool {
	for _, v := range h.Values("Cache-Control") {
		for _, tok := range strings.Split(v, ",") {
			tok = strings.TrimSpace(tok)
			if i := strings.IndexByte(tok, '='); i >= 0 {
				tok = strings.TrimSpace(tok[:i])
			}
			if strings.EqualFold(tok, name) {
				return true
			}
		}
	}
	return false
}

// storableFor reports whether resp, fetched for req, may be written to
// the store. It layers the request-dependent rules of RFC 9111 on top of
// cacheableResponse:
//
//   - §4.1: a response with "Vary: *" can never be matched, so it is not
//     stored.
//   - §5.2.2.7: a response marked private is never stored; the store is
//     shared across identities. This wins over the §3.5 exemptions.
//   - §3.5: a request carrying Authorization is only stored when the
//     response explicitly permits shared caching (public, s-maxage or
//     must-revalidate); otherwise one identity's response could be served
//     to another.
func storableFor(req *http.Request, resp *http.Response) bool {
	if !cacheableResponse(resp) {
		return false
	}
	if vf := varyFields(resp.Header); len(vf) == 1 && vf[0] == "*" {
		return false
	}
	// §5.2.2.7: the store is shared across identities, so a private
	// response is never stored. Checked before the Authorization
	// exemptions: "private, must-revalidate" must not slip through.
	if hasDirective(resp.Header, "private") {
		return false
	}
	if req.Header.Get("Authorization") != "" {
		return hasDirective(resp.Header, "public") ||
			hasDirective(resp.Header, "s-maxage") ||
			hasDirective(resp.Header, "must-revalidate")
	}
	return true
}

// varyFields returns the lowercased, sorted, de-duplicated field names of
// every Vary header line in h. "*" collapses the result to ["*"]. It
// returns nil when there is no Vary.
func varyFields(h http.Header) []string {
	seen := map[string]struct{}{}
	for _, v := range h.Values("Vary") {
		for _, f := range strings.Split(v, ",") {
			f = strings.ToLower(strings.TrimSpace(f))
			if f == "" {
				continue
			}
			if f == "*" {
				return []string{"*"}
			}
			seen[f] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}
