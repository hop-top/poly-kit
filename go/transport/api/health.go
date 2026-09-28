package api

import (
	"net/http"
	"strings"
)

// Health route paths, relative to [HealthConfig.PathPrefix].
//
// The names follow the convention orchestrators and load balancers
// already expect: /healthz answers "is the process serving HTTP at
// all" (liveness — restart me if not), /readyz answers "should I be
// sent traffic" (readiness — take me out of rotation if not).
const (
	LivenessPath  = "/healthz"
	ReadinessPath = "/readyz"
)

// Health response status values.
const (
	HealthStatusOK          = "ok"
	HealthStatusUnavailable = "unavailable"
)

// ReadinessCheck is one named input to the readiness verdict. Ready
// is called on every probe, so it must be cheap and must not block:
// report a state something else maintains rather than doing I/O.
type ReadinessCheck struct {
	Name  string
	Ready func() bool
}

// HealthConfig configures [HealthRoutes].
type HealthConfig struct {
	// PathPrefix is prepended to both routes ("" serves them at
	// /healthz and /readyz). It must start with "/" and not end
	// with one; the caller validates it.
	PathPrefix string
	// Checks decide readiness: every check must report ready for
	// /readyz to answer 200. No checks means ready.
	Checks []ReadinessCheck
	// Detail lists the names of failing checks in the 503 body. Off,
	// the body says only that the process is unavailable, which is
	// what an unauthenticated caller on a remote bind should learn.
	Detail bool
}

// HealthStatus is the JSON body both routes answer with.
type HealthStatus struct {
	Status  string   `json:"status"`
	Failing []string `json:"failing,omitempty"`
}

// HealthRoutes returns a handler that answers the liveness and
// readiness routes itself and passes every other request to next.
//
// It is a wrapper rather than a [Middleware] or a route on a
// [Router] on purpose: the routes are answered BEFORE next and end
// the request there, so none of the middleware configured on the
// router next usually is — Host and Origin checks, body limits,
// authentication — ever sees a probe. An orchestrator probing a pod
// addresses it by IP and carries no credentials; a probe refused by
// either would restart a healthy process. Middleware that must cover
// the probes too (request id, access log, recovery, telemetry,
// security headers) wraps the handler HealthRoutes returns.
//
// A route the adopter registered at exactly the same path wins: when
// next is a [*Router] or an [*http.ServeMux] that already serves GET
// at a probe path, that path is passed through and only the other
// probe is answered here. A subtree mount ("/") does not claim a
// probe path.
//
// Both routes answer GET and HEAD, never cache, and never describe
// anything beyond their verdict: no version, no path, no error text.
// They are not registered on the router, so they appear in neither
// the capabilities listing, the command discovery document, nor the
// OpenAPI description.
func HealthRoutes(next http.Handler, cfg HealthConfig) http.Handler {
	prefix := strings.TrimSuffix(cfg.PathPrefix, "/")
	live := prefix + LivenessPath
	ready := prefix + ReadinessPath
	if servesExactly(next, live) {
		live = ""
	}
	if servesExactly(next, ready) {
		ready = ""
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case p == live && live != "":
			writeHealth(w, r, HealthStatus{Status: HealthStatusOK})
		case p == ready && ready != "":
			writeHealth(w, r, readiness(cfg))
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// servesExactly reports whether h is a mux that routes GET p to a
// pattern registered for p itself, as opposed to a subtree above it.
// It is decided once, when the wrapper is built, because every route
// is registered before a server starts.
func servesExactly(h http.Handler, p string) bool {
	var mux *http.ServeMux
	switch m := h.(type) {
	case *Router:
		mux = m.mux
	case *http.ServeMux:
		mux = m
	default:
		return false
	}
	req, err := http.NewRequest(http.MethodGet, p, nil)
	if err != nil {
		return false
	}
	_, pattern := mux.Handler(req)
	// A pattern is "[METHOD ][HOST]/path"; keep the path.
	if i := strings.IndexByte(pattern, ' '); i >= 0 {
		pattern = pattern[i+1:]
	}
	if i := strings.IndexByte(pattern, '/'); i > 0 {
		pattern = pattern[i:]
	}
	return pattern == p
}

// readiness evaluates every check. All of them run on every probe so
// a detailed body names every failing input, not just the first.
func readiness(cfg HealthConfig) HealthStatus {
	var failing []string
	for _, c := range cfg.Checks {
		if c.Ready == nil || !c.Ready() {
			failing = append(failing, c.Name)
		}
	}
	if len(failing) == 0 {
		return HealthStatus{Status: HealthStatusOK}
	}
	st := HealthStatus{Status: HealthStatusUnavailable}
	if cfg.Detail {
		st.Failing = failing
	}
	return st
}

// writeHealth writes st with 200 when ok and 503 otherwise.
func writeHealth(w http.ResponseWriter, r *http.Request, st HealthStatus) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		Error(w, http.StatusMethodNotAllowed, &APIError{
			Status:  http.StatusMethodNotAllowed,
			Code:    "method_not_allowed",
			Message: "health routes answer GET and HEAD",
		})
		return
	}
	code := http.StatusOK
	if st.Status != HealthStatusOK {
		code = http.StatusServiceUnavailable
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		return
	}
	JSON(w, code, st)
}
