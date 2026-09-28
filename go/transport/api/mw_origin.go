package api

import (
	"fmt"
	"net/http"
	"strings"
)

// CodeOriginRejected is the stable refusal code [OriginCheck] writes.
const CodeOriginRejected = "origin_rejected"

// OriginCheckConfig configures [OriginCheck].
type OriginCheckConfig struct {
	// Allow lists the cross-origin browser origins permitted
	// to send state-changing requests, each "scheme://host[:port]"
	// with no path. The single entry "*" disables the check.
	//
	// An origin granted by [CORS] is only readable; to let that page
	// also write, list it here too.
	Allow []string
	// Refuse writes the refusal; nil writes an [APIError].
	Refuse RefusalWriter
}

// OriginCheck returns a middleware that refuses state-changing
// requests sent by a browser page on another origin: cross-site
// request forgery against a local or remote tool.
//
// It is [http.CrossOriginProtection] with a stable refusal body:
//
//   - GET, HEAD and OPTIONS pass; they must not change state.
//   - A request with neither Origin nor Sec-Fetch-Site passes; it
//     did not come from a browser (curl, an SDK, another server).
//   - A same-origin request passes: Origin's host:port equals Host,
//     or Sec-Fetch-Site says same-origin. Put [HostCheck] before
//     this middleware so "same origin" means a host the server
//     answers for.
//   - An origin in Allow passes.
//   - Anything else gets 403 and the code [CodeOriginRejected].
//
// A WebSocket upgrade is a GET; [WSHandler] applies its own
// same-origin check (see [WithAcceptOrigins]).
//
// It returns an error for an Allow entry that is not a bare origin.
func OriginCheck(cfg OriginCheckConfig) (Middleware, error) {
	cop := http.NewCrossOriginProtection()
	for _, o := range cfg.Allow {
		o = strings.TrimSpace(o)
		if o == "*" {
			return func(next http.Handler) http.Handler { return next }, nil
		}
		if err := cop.AddTrustedOrigin(o); err != nil {
			return nil, fmt.Errorf("allowed origin %q: %w", o, err)
		}
	}
	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = "Sec-Fetch-Site: " + r.Header.Get("Sec-Fetch-Site")
		}
		RecordRefusal(r, CodeOriginRejected)
		cfg.Refuse.write(w, r, &APIError{
			Status:  http.StatusForbidden,
			Code:    CodeOriginRejected,
			Message: fmt.Sprintf("cross-origin request from %q refused", origin),
		})
	}))
	return cop.Handler, nil
}
