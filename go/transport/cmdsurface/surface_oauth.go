package cmdsurface

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"hop.top/kit/go/transport/api"
)

// OAuthProvider declares an OAuth callback route.
type OAuthProvider struct {
	// Name is the URL slug: /oauth/{Name}/callback.
	Name string
	// Path is the leaf command path invoked on callback.
	Path []string
	// FlagFromQuery maps query parameter names to flag names. For
	// example, {"code":"auth_code","state":"state"} would set
	// inv.Flags["auth_code"] = r.URL.Query().Get("code"). Query
	// parameters not listed here are dropped.
	FlagFromQuery map[string]string
	// ErrorRedirect is the URL to redirect the user to on failure
	// (auth error from provider, state mismatch, invoke error).
	// Empty = render a plain text error page.
	ErrorRedirect string
	// SuccessRedirect is the URL to redirect the user to on success.
	// Empty = render a plain text success page.
	SuccessRedirect string
}

// OAuthOption configures MountOAuth.
type OAuthOption func(*oauthConfig)

type oauthConfig struct {
	prefix      string
	stateTTL    time.Duration
	authorizeFn func(provider string) (string, error)
}

func defaultOAuthConfig() oauthConfig {
	return oauthConfig{
		prefix:   "/oauth",
		stateTTL: 10 * time.Minute,
	}
}

// WithOAuthPrefix sets the URL prefix under which OAuth routes mount.
// Default is "/oauth". A trailing "/" is stripped so the prefix +
// "/{name}/callback" join cleanly.
func WithOAuthPrefix(prefix string) OAuthOption {
	return func(c *oauthConfig) {
		for len(prefix) > 1 && strings.HasSuffix(prefix, "/") {
			prefix = prefix[:len(prefix)-1]
		}
		c.prefix = prefix
	}
}

// WithOAuthStateTTL sets the lifetime of state nonces issued via the
// authorize endpoint. Default is 10 minutes. Non-positive values are
// silently clamped to the default.
func WithOAuthStateTTL(d time.Duration) OAuthOption {
	return func(c *oauthConfig) {
		if d > 0 {
			c.stateTTL = d
		}
	}
}

// WithOAuthAuthorizeFn installs the callback that returns the upstream
// provider's authorization URL given a provider name. When unset, the
// authorize endpoint replies 501 not_configured — useful for adopters
// who build the authorize URL client-side and only need the callback
// route. The returned URL must not already include a `state` query
// parameter; if it does, the supplied URL wins and validation will
// fail on callback (caller's mistake).
func WithOAuthAuthorizeFn(fn func(provider string) (string, error)) OAuthOption {
	return func(c *oauthConfig) { c.authorizeFn = fn }
}

// MountOAuth registers OAuth authorize + callback endpoints for each
// provider in providers.
//
// Routes:
//
//	GET {prefix}/{Name}/authorize  → issues state, redirects to
//	                                  AuthorizeFn(Name) with state appended
//	GET {prefix}/{Name}/callback   → validates state, invokes the leaf,
//	                                  redirects (or renders) per
//	                                  SuccessRedirect / ErrorRedirect
//
// Mount-time refusals:
//
//   - empty Name or Path: error.
//   - leaf path does not resolve: ErrUnknownCommand.
//   - leaf is not exposed on SurfaceOAuthCB: ErrSurfaceNotEnabled.
//   - leaf is destructive and Policy.AllowDestructiveOn does not
//     include SurfaceOAuthCB: ErrDestructiveBlocked.
//   - leaf has Class.RequiresConfirmation: error (a redirect-driven
//     flow cannot surface a confirm-token prompt).
//
// Auth: the validated OAuth state IS the authentication. The bridge
// treats Class.AuthRequired leaves as authenticated when state
// consumes successfully — no separate AuthFunc is needed.
func MountOAuth(b *Bridge, r *api.Router, providers []OAuthProvider, store StateStore, opts ...OAuthOption) error {
	if b == nil {
		return errors.New("cmdsurface: nil Bridge")
	}
	if r == nil {
		return errors.New("cmdsurface: nil api.Router")
	}
	if store == nil {
		return errors.New("cmdsurface: nil StateStore")
	}
	cfg := defaultOAuthConfig()
	for _, o := range opts {
		o(&cfg)
	}

	// Pre-validate every provider before registering any route. This
	// guarantees an all-or-nothing mount.
	type prepared struct {
		provider OAuthProvider
		leaf     *Leaf
	}
	prep := make([]prepared, 0, len(providers))
	seen := make(map[string]bool, len(providers))
	for i, p := range providers {
		if strings.TrimSpace(p.Name) == "" {
			return fmt.Errorf("cmdsurface: oauth provider %d: empty Name", i)
		}
		if seen[p.Name] {
			return fmt.Errorf("cmdsurface: oauth provider %d (%s): duplicate Name", i, p.Name)
		}
		seen[p.Name] = true
		if len(p.Path) == 0 {
			return fmt.Errorf("cmdsurface: oauth provider %s: empty Path", p.Name)
		}
		leaf, err := b.resolveLeaf(p.Path)
		if err != nil {
			return fmt.Errorf("cmdsurface: oauth provider %s: %w", p.Name, err)
		}
		if !leaf.Enabled[SurfaceOAuthCB] {
			return fmt.Errorf("%w: %s on %s",
				ErrSurfaceNotEnabled, leaf.PathKey(), SurfaceOAuthCB)
		}
		if leaf.Class.Destructive && !b.cfg.policy.Allowed(leaf.Class, SurfaceOAuthCB) {
			return fmt.Errorf("%w: %s on %s",
				ErrDestructiveBlocked, leaf.PathKey(), SurfaceOAuthCB)
		}
		if leaf.Class.RequiresConfirmation {
			return fmt.Errorf("cmdsurface: oauth provider %s: leaf %s requires confirmation; not supported on oauth-cb",
				p.Name, leaf.PathKey())
		}
		prep = append(prep, prepared{provider: p, leaf: leaf})
	}

	for _, e := range prep {
		p := e.provider
		leaf := e.leaf
		authorizePath := cfg.prefix + "/" + p.Name + "/authorize"
		callbackPath := cfg.prefix + "/" + p.Name + "/callback"
		r.Handle(http.MethodGet, authorizePath, newOAuthAuthorizeHandler(p, store, cfg))
		r.Handle(http.MethodGet, callbackPath, newOAuthCallbackHandler(b, leaf, p, store))
	}
	return nil
}

// newOAuthAuthorizeHandler issues a state nonce and redirects the
// caller to the upstream provider's authorization URL with the state
// appended.
func newOAuthAuthorizeHandler(p OAuthProvider, store StateStore, cfg oauthConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.authorizeFn == nil {
			oauthWriteError(w, p, http.StatusNotImplemented, "not_configured",
				"WithOAuthAuthorizeFn not supplied")
			return
		}
		state, err := store.Issue(r.Context(), p.Name, cfg.stateTTL)
		if err != nil {
			oauthWriteError(w, p, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		raw, err := cfg.authorizeFn(p.Name)
		if err != nil {
			oauthWriteError(w, p, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		http.Redirect(w, r, appendStateParam(raw, state), http.StatusFound)
	}
}

// newOAuthCallbackHandler validates state, builds an Invocation, and
// dispatches via Bridge.Invoke.
func newOAuthCallbackHandler(b *Bridge, leaf *Leaf, p OAuthProvider, store StateStore) http.HandlerFunc {
	leafPath := append([]string(nil), leaf.Path...)
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		// Provider rejection takes precedence over state validation:
		// the provider may have signaled "user denied access" before
		// the user round-tripped back through our authorize endpoint,
		// in which case state may legitimately be absent.
		if perr := q.Get("error"); perr != "" {
			oauthWriteError(w, p, http.StatusBadRequest,
				"provider_error:"+perr, q.Get("error_description"))
			return
		}

		state := q.Get("state")
		if state == "" {
			oauthWriteError(w, p, http.StatusBadRequest, "missing_state",
				"state query parameter required")
			return
		}
		if err := store.Consume(r.Context(), p.Name, state); err != nil {
			oauthWriteError(w, p, http.StatusBadRequest, "invalid_state", err.Error())
			return
		}

		flags := make(map[string]any, len(p.FlagFromQuery))
		for queryKey, flagName := range p.FlagFromQuery {
			flags[flagName] = q.Get(queryKey)
		}

		inv := Invocation{
			Path:  append([]string(nil), leafPath...),
			Flags: flags,
			Meta: Meta{
				Surface:     SurfaceOAuthCB,
				Caller:      p.Name,
				TraceID:     r.Header.Get("X-Request-ID"),
				RequestedAt: time.Now(),
			},
		}

		_ = leaf // resolution validated at mount time; bridge re-resolves by path
		_, err := b.Invoke(r.Context(), inv)
		if err != nil {
			oauthWriteInvokeError(w, p, err)
			return
		}
		oauthWriteSuccess(w, p)
	}
}

// appendStateParam appends ?state=<value> or &state=<value> to raw
// depending on whether raw already has a query string. Encodes value
// for URL safety.
func appendStateParam(raw, state string) string {
	sep := "?"
	if strings.Contains(raw, "?") {
		sep = "&"
	}
	return raw + sep + "state=" + url.QueryEscape(state)
}

// oauthWriteSuccess redirects or renders a plain success page.
func oauthWriteSuccess(w http.ResponseWriter, p OAuthProvider) {
	if p.SuccessRedirect != "" {
		// Use a synthetic request-less redirect: http.Redirect needs a
		// *http.Request only to derive a relative path base; for an
		// absolute URL it does not consult the request, so a nil-safe
		// shortcut is to set Location explicitly.
		w.Header().Set("Location", p.SuccessRedirect)
		w.WriteHeader(http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OAuth complete\n"))
}

// oauthWriteError redirects with ?error=<code> if ErrorRedirect is
// set, or renders a plain text error page with the given status.
func oauthWriteError(w http.ResponseWriter, p OAuthProvider, status int, code, message string) {
	if p.ErrorRedirect != "" {
		sep := "?"
		if strings.Contains(p.ErrorRedirect, "?") {
			sep = "&"
		}
		w.Header().Set("Location", p.ErrorRedirect+sep+"error="+url.QueryEscape(code))
		w.WriteHeader(http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	body := "OAuth callback error: " + code
	if message != "" {
		body += "\n" + message
	}
	_, _ = w.Write([]byte(body + "\n"))
}

// oauthWriteInvokeError maps Bridge.Invoke sentinel errors to
// surface-appropriate responses.
func oauthWriteInvokeError(w http.ResponseWriter, p OAuthProvider, err error) {
	switch {
	case errors.Is(err, ErrUnknownCommand):
		oauthWriteError(w, p, http.StatusInternalServerError, "unknown_command", err.Error())
	case errors.Is(err, ErrSurfaceNotEnabled):
		oauthWriteError(w, p, http.StatusInternalServerError, "not_enabled", err.Error())
	case errors.Is(err, ErrDestructiveBlocked):
		oauthWriteError(w, p, http.StatusForbidden, "destructive_blocked", err.Error())
	default:
		oauthWriteError(w, p, http.StatusInternalServerError, "internal_error", err.Error())
	}
}

// Compile-time interface guard.
var _ StateStore = (*InMemoryStateStore)(nil)
