package classifier

import (
	"strings"

	xrrhttp "hop.top/xrr/adapters/http"
)

// ClassifyHTTP returns the Class for an HTTP request, keyed off
// the verb per RFC 7231:
//
//   - GET / HEAD / OPTIONS / TRACE  → Read (safe methods)
//   - POST / PUT / PATCH            → Write
//   - DELETE                        → Destructive
//   - anything else                 → Unknown (treated as mutating)
//
// Status-code reclassification on the response side is intentionally
// not done: a POST that returned 400 still *intended* to mutate, and
// the dry-run contract says we should not have issued it at all.
func ClassifyHTTP(req *xrrhttp.Request) Class {
	if req == nil {
		return ClassUnknown
	}
	switch strings.ToUpper(req.Method) {
	case "GET", "HEAD", "OPTIONS", "TRACE":
		return ClassRead
	case "POST", "PUT", "PATCH":
		return ClassWrite
	case "DELETE":
		return ClassDestructive
	default:
		return ClassUnknown
	}
}
