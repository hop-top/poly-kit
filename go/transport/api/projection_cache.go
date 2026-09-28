package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// CacheDirective says how a projected command's result may be cached by
// the client that asked for it. An executor sets it on
// [CommandResult].Cache for a read whose result is served from, or
// stored in, a result cache; the projection then renders it on the GET
// route as ETag and Cache-Control, and answers a request whose
// If-None-Match matches with 304 Not Modified and no body.
type CacheDirective struct {
	// ETag is the result's opaque validator, unquoted. It is sent weak
	// (W/"…"): it identifies the result, not the bytes of one
	// encoding, so a compressed response carries it unchanged.
	ETag string
	// MaxAge is how long the result stays fresh from now; it becomes
	// Cache-Control max-age, in whole seconds.
	MaxAge time.Duration
	// Private marks a result that belongs to one caller: Cache-Control
	// says private, so no shared cache keeps it.
	Private bool
}

// etag is the header form of d.ETag.
func (d CacheDirective) etag() string { return `W/"` + d.ETag + `"` }

// cacheControl is the Cache-Control value for d.
func (d CacheDirective) cacheControl() string {
	scope := "public"
	if d.Private {
		scope = "private"
	}
	secs := int64(max(d.MaxAge, 0) / time.Second)
	return scope + ", max-age=" + strconv.FormatInt(secs, 10)
}

// writeCacheHeaders renders d on a successful GET or HEAD response and
// reports whether it answered 304 because r's If-None-Match matched.
func writeCacheHeaders(w http.ResponseWriter, r *http.Request, d CacheDirective) bool {
	if d.ETag == "" || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return false
	}
	h := w.Header()
	h.Set("ETag", d.etag())
	h.Set("Cache-Control", d.cacheControl())
	if !noneMatchHits(r.Header.Values("If-None-Match"), d.ETag) {
		return false
	}
	w.WriteHeader(http.StatusNotModified)
	return true
}

// noneMatchHits reports whether an If-None-Match header names etag,
// with the weak comparison RFC 9110 prescribes for it: W/ is ignored on
// both sides, and "*" matches any current representation.
func noneMatchHits(values []string, etag string) bool {
	for _, v := range values {
		for _, tag := range strings.Split(v, ",") {
			tag = strings.TrimSpace(tag)
			if tag == "*" {
				return true
			}
			tag = strings.TrimPrefix(tag, "W/")
			if len(tag) >= 2 && tag[0] == '"' && tag[len(tag)-1] == '"' && tag[1:len(tag)-1] == etag {
				return true
			}
		}
	}
	return false
}
