package output

import (
	"regexp"
	"strings"
	"sync"

	"hop.top/cite/handle"
)

// uriSchemes lists URI schemes that get auto-linkified in table
// output. Extend this list as new hop.top schemes are registered.
var (
	schemesMu  sync.RWMutex
	uriSchemes = []string{"tlc", "aps"}
	uriPattern = compilePattern(uriSchemes)
)

func compilePattern(schemes []string) *regexp.Regexp {
	return regexp.MustCompile(
		`\b(` + strings.Join(schemes, "|") + `)://[^\s]+`,
	)
}

// RegisterLinkScheme adds a URI scheme to the auto-linkify list
// and recompiles the pattern. Call during init or before rendering.
func RegisterLinkScheme(scheme string) {
	schemesMu.Lock()
	defer schemesMu.Unlock()
	for _, s := range uriSchemes {
		if s == scheme {
			return
		}
	}
	uriSchemes = append(uriSchemes, scheme)
	uriPattern = compilePattern(uriSchemes)
}

// linkifyCell replaces recognized URIs in a cell value with
// OSC 8 hyperlinks via hop.top/cite/handle. Falls back to the
// short label when the terminal does not support hyperlinks.
func linkifyCell(val string) string {
	schemesMu.RLock()
	pat := uriPattern
	schemesMu.RUnlock()
	return pat.ReplaceAllStringFunc(val, func(uri string) string {
		return handle.Linkify(uri, shortLabel(uri))
	})
}

// shortLabel extracts a human-friendly label from a URI by taking
// the trailing path segment.
//
//	"tlc://project/T-0078"         → "T-0078"
//	"aps://workspace/profile/noor" → "noor"
func shortLabel(uri string) string {
	idx := strings.Index(uri, "://")
	if idx < 0 {
		return uri
	}
	path := uri[idx+3:]
	if path == "" {
		return uri
	}
	if last := strings.LastIndex(path, "/"); last >= 0 && last < len(path)-1 {
		return path[last+1:]
	}
	return path
}
