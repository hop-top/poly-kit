package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/rs/cors"
)

// CORSConfig configures [CORS]: which pages on other origins may read
// this server's responses, under the CORS protocol of the Fetch
// standard.
type CORSConfig struct {
	// AllowOrigins lists the origins granted, each
	// "scheme://host[:port]" as a browser sends it in Origin, compared
	// without case. The single entry "*" grants every origin. Empty
	// grants none, and the middleware passes every request through
	// untouched.
	AllowOrigins []string
	// AllowMethods are the methods a preflight admits. Empty admits
	// GET, HEAD and POST.
	AllowMethods []string
	// AllowHeaders are the request headers a preflight admits, without
	// case; "*" admits any. Empty admits Accept, Content-Type and
	// X-Requested-With.
	AllowHeaders []string
	// ExposeHeaders are the response headers beyond the CORS-safelisted
	// ones a granted page may read.
	ExposeHeaders []string
	// AllowCredentials lets a granted page send cookies, HTTP
	// authentication or a client certificate and read the response.
	// The Fetch standard refuses it with the "*" origin.
	AllowCredentials bool
	// MaxAge is how long, in seconds, a browser may cache a preflight
	// answer. Zero sends no Access-Control-Max-Age, and the browser
	// keeps it five seconds.
	MaxAge int
}

// Validate reports what the Fetch standard or a browser would make of
// the configuration that is not what it says: an origin that is not a
// bare origin, "*" beside other origins, "*" with credentials (for
// origins or exposed headers), a method or header that is not a token,
// a negative MaxAge. [CORS] accepts an invalid configuration, and an
// origin that does not parse is never granted.
func (c CORSConfig) Validate() error {
	var errs []error
	wildcard := false
	for _, o := range c.AllowOrigins {
		if strings.TrimSpace(o) == "*" {
			wildcard = true
			continue
		}
		if _, err := ParseOrigin(o); err != nil {
			errs = append(errs, err)
		}
	}
	if wildcard && len(c.AllowOrigins) > 1 {
		errs = append(errs, errors.New(`"*" grants every origin; list it alone or list origins`))
	}
	if wildcard && c.AllowCredentials {
		errs = append(errs, errors.New(
			`credentials cannot be allowed with the "*" origin (Fetch standard); list the origins instead`))
	}
	for _, m := range c.AllowMethods {
		if !isToken(strings.TrimSpace(m)) {
			errs = append(errs, fmt.Errorf("method %q is not an HTTP method token", m))
		}
	}
	for _, h := range c.AllowHeaders {
		if h = strings.TrimSpace(h); h != "*" && !isToken(h) {
			errs = append(errs, fmt.Errorf("header %q is not a header name", h))
		}
	}
	for _, h := range c.ExposeHeaders {
		h = strings.TrimSpace(h)
		switch {
		case h == "*" && c.AllowCredentials:
			errs = append(errs, errors.New(
				`a credentialed response exposes no header by "*" (Fetch standard); name the headers`))
		case h != "*" && !isToken(h):
			errs = append(errs, fmt.Errorf("header %q is not a header name", h))
		}
	}
	if c.MaxAge < 0 {
		errs = append(errs, fmt.Errorf("max age %d is negative", c.MaxAge))
	}
	return errors.Join(errs...)
}

// ParseOrigin returns origin as a browser serializes it in the Origin
// header — scheme and host lowercased, the scheme's default port
// dropped — or an error when it is not a bare "scheme://host[:port]":
// no path, query, fragment, user or wildcard. "null" is not an origin
// it accepts: sandboxed and file pages all send it.
func ParseOrigin(origin string) (string, error) {
	s := strings.TrimSpace(origin)
	bad := func(why string) (string, error) {
		return "", fmt.Errorf("origin %q: %s; want scheme://host[:port]", origin, why)
	}
	if strings.Contains(s, "*") {
		return bad(`a wildcard matches nothing; list each origin, or "*" alone for every origin`)
	}
	u, err := url.Parse(s)
	switch {
	case err != nil:
		return bad(err.Error())
	case u.Scheme == "" || u.Host == "" || u.Opaque != "":
		return bad("scheme and host are required")
	case u.User != nil:
		return bad("user information is not part of an origin")
	case u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery:
		return bad("path, query and fragment are not part of an origin")
	}
	scheme, host := strings.ToLower(u.Scheme), strings.ToLower(u.Host)
	if (scheme == "https" && strings.HasSuffix(host, ":443")) ||
		(scheme == "http" && strings.HasSuffix(host, ":80")) {
		host = host[:strings.LastIndexByte(host, ':')]
	}
	return scheme + "://" + host, nil
}

// isToken reports whether s is an HTTP token (RFC 9110 §5.6.2), the
// syntax of a method and a header name.
func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0 {
			continue
		}
		return false
	}
	return true
}

// CORS returns a middleware answering the CORS protocol of the Fetch
// standard for the origins cfg grants:
//
//   - A preflight — OPTIONS with Access-Control-Request-Method — is
//     answered here, 204, and never reaches next: it carries no
//     credentials, so authentication behind this middleware would
//     refuse it. From a granted origin, for an admitted method and
//     headers, the answer carries the grant; otherwise it carries no
//     Access-Control-* header, and the browser refuses the request.
//   - Any other request passes to next; from a granted origin the
//     response carries Access-Control-Allow-Origin, the exposed
//     headers and, when allowed, credentials.
//   - Every response carries Vary: Origin (a preflight's also names
//     the request method and headers), so a cache never serves one
//     origin's grant to another.
//
// A grant makes responses readable; it does not admit a write refused
// elsewhere. Behind [OriginCheck], list the origin there too to let
// its page write.
//
// Check cfg with [CORSConfig.Validate] first; CORS never grants an
// origin that does not parse.
func CORS(cfg CORSConfig) Middleware {
	var origins []string
	for _, o := range cfg.AllowOrigins {
		if strings.TrimSpace(o) == "*" {
			origins = []string{"*"}
			break
		}
		if n, err := ParseOrigin(o); err == nil {
			origins = append(origins, n)
		}
	}
	if len(origins) == 0 {
		// rs/cors reads an empty list as every origin.
		return func(next http.Handler) http.Handler { return next }
	}
	c := cors.New(cors.Options{
		AllowedOrigins:   origins,
		AllowedMethods:   trimAll(cfg.AllowMethods),
		AllowedHeaders:   trimAll(cfg.AllowHeaders),
		ExposedHeaders:   trimAll(cfg.ExposeHeaders),
		AllowCredentials: cfg.AllowCredentials,
		MaxAge:           cfg.MaxAge,
	})
	return c.Handler
}

// trimAll trims each entry of list, dropping empty ones.
func trimAll(list []string) []string {
	var out []string
	for _, s := range list {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
