package cmdsurface

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"hop.top/kit/go/transport/api"
)

// MountREST registers one HTTP endpoint per leaf where SurfaceREST is
// enabled on b. Endpoints are POSTs that accept a JSON Invocation body
// and return a JSON Result.
//
//	POST {prefix}/{cmd-path}   body = Invocation, response = Result
//
// cmd-path is the leaf's cobra path joined with "/". Example: leaf
// ["widget","add"] at prefix="/cmd" → POST /cmd/widget/add.
//
// MountREST forces inv.Path to the resolved leaf's path (callers
// cannot reroute by lying in the body) and forces inv.Meta.Surface to
// SurfaceREST. On success the response body is the Result with its
// ExitCode preserved as JSON; non-zero exit codes do NOT translate to
// HTTP error codes — they are the command's own contract.
//
// Sentinel-error mapping:
//
//	ErrUnknownCommand     → 404 code=unknown_command
//	ErrSurfaceNotEnabled  → 404 code=not_enabled
//	ErrDestructiveBlocked → 403 code=destructive_blocked
//	body decode error     → 400 code=bad_request
//	any other error       → api.MapError passthrough
//
// Per-leaf middleware:
//
//   - Class.AuthRequired wraps the route with api.Auth (caller must
//     have supplied an AuthFunc via WithRESTAuth).
//   - Class.RequiresConfirmation gates the route on the presence of an
//     X-Confirm-Token header; missing header → 428 code=confirmation_required.
//     The token value is not validated here (issuance is a later task).
func MountREST(b *Bridge, r *api.Router, opts ...RESTOption) error {
	if b == nil {
		return errors.New("cmdsurface: nil Bridge")
	}
	if r == nil {
		return errors.New("cmdsurface: nil api.Router")
	}
	cfg := defaultRESTConfig()
	for _, o := range opts {
		o(&cfg)
	}

	for _, leaf := range b.Leaves() {
		if !leaf.Enabled[SurfaceREST] {
			continue
		}
		path := cfg.prefix + "/" + strings.Join(leaf.Path, "/")
		handler := newLeafHandler(b, leaf)
		wrapped := wrapMiddleware(handler, leaf, cfg)
		r.Handle(http.MethodPost, path, wrapped)

		if cfg.humaRegister != nil {
			cfg.humaRegister(b, leaf, path)
		}
	}
	return nil
}

// RESTOption configures MountREST.
type RESTOption func(*restConfig)

type restConfig struct {
	prefix       string
	humaRegister func(b *Bridge, leaf *Leaf, path string)
	middleware   []func(http.Handler) http.Handler
	authFn       api.AuthFunc
}

func defaultRESTConfig() restConfig {
	return restConfig{prefix: "/cmd"}
}

// WithRESTPrefix sets the URL prefix for every mounted route. Default
// is "/cmd". A trailing "/" on prefix is stripped to avoid producing
// "/cmd//widget/add".
func WithRESTPrefix(prefix string) RESTOption {
	return func(c *restConfig) {
		for len(prefix) > 1 && strings.HasSuffix(prefix, "/") {
			prefix = prefix[:len(prefix)-1]
		}
		c.prefix = prefix
	}
}

// WithRESTMiddleware installs middleware applied to every mounted
// route, after any per-leaf safety wrapping (auth / confirmation).
// Middleware is applied in the order given: first wraps outermost.
func WithRESTMiddleware(mw ...func(http.Handler) http.Handler) RESTOption {
	return func(c *restConfig) {
		c.middleware = append(c.middleware, mw...)
	}
}

// WithRESTAuth installs the AuthFunc used by api.Auth to wrap routes
// whose leaf has Class.AuthRequired. When unset, AuthRequired leaves
// are wrapped with a default AuthFunc that always returns an error,
// so unauthenticated calls receive 401.
func WithRESTAuth(fn api.AuthFunc) RESTOption {
	return func(c *restConfig) { c.authFn = fn }
}

// newLeafHandler returns the http.HandlerFunc that decodes the JSON
// body, forces Path + Meta.Surface, invokes the bridge, and writes the
// Result (or error) as JSON.
func newLeafHandler(b *Bridge, leaf *Leaf) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var inv Invocation
		if r.Body != nil && r.ContentLength != 0 {
			dec := json.NewDecoder(r.Body)
			if err := dec.Decode(&inv); err != nil {
				api.Error(w, http.StatusBadRequest, &api.APIError{
					Status:  http.StatusBadRequest,
					Code:    "bad_request",
					Message: err.Error(),
				})
				return
			}
		}
		// The leaf path is authoritative; ignore any client-supplied path.
		inv.Path = append([]string(nil), leaf.Path...)
		inv.Meta.Surface = SurfaceREST
		inv.Meta.RequestedAt = time.Now()

		res, err := b.Invoke(r.Context(), inv)
		if err != nil {
			writeBridgeError(w, err)
			return
		}
		api.JSON(w, http.StatusOK, res)
	}
}

// writeBridgeError maps sentinel errors from Bridge.Invoke to API
// responses; everything else falls through to api.MapError so kit
// transport conventions stay uniform.
func writeBridgeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrUnknownCommand):
		api.Error(w, http.StatusNotFound, &api.APIError{
			Status:  http.StatusNotFound,
			Code:    "unknown_command",
			Message: err.Error(),
		})
	case errors.Is(err, ErrSurfaceNotEnabled):
		api.Error(w, http.StatusNotFound, &api.APIError{
			Status:  http.StatusNotFound,
			Code:    "not_enabled",
			Message: err.Error(),
		})
	case errors.Is(err, ErrDestructiveBlocked):
		api.Error(w, http.StatusForbidden, &api.APIError{
			Status:  http.StatusForbidden,
			Code:    "destructive_blocked",
			Message: err.Error(),
		})
	default:
		ae := api.MapError(err)
		api.Error(w, ae.Status, ae)
	}
}

// wrapMiddleware composes confirmation gate → auth → user middleware
// around h. The inner-most wrapper runs first: the request is checked
// for X-Confirm-Token (if required) before auth and user middleware.
func wrapMiddleware(h http.HandlerFunc, leaf *Leaf, cfg restConfig) http.HandlerFunc {
	var handler http.Handler = h
	if leaf.Class.RequiresConfirmation {
		handler = confirmationGate(handler)
	}
	if leaf.Class.AuthRequired {
		fn := cfg.authFn
		if fn == nil {
			fn = denyAuth
		}
		handler = api.Auth(fn)(handler)
	}
	// User middleware wraps outermost (last in, first to run).
	for i := len(cfg.middleware) - 1; i >= 0; i-- {
		handler = cfg.middleware[i](handler)
	}
	return handler.ServeHTTP
}

// confirmationGate refuses requests that lack the X-Confirm-Token
// header with 428 Precondition Required. The token's value is not
// inspected — only its presence.
func confirmationGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Confirm-Token") == "" {
			api.Error(w, http.StatusPreconditionRequired, &api.APIError{
				Status:  http.StatusPreconditionRequired,
				Code:    "confirmation_required",
				Message: "X-Confirm-Token header required for this command",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// denyAuth is the fallback AuthFunc used when WithRESTAuth is not
// supplied but a leaf requires auth. It always refuses, producing 401.
func denyAuth(_ *http.Request) (any, error) {
	return nil, errors.New("authentication required")
}
