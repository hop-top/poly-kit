package api

import (
	"context"
	"net/http"
	"reflect"
)

type claimsKey struct{}

// AuthFunc validates a request and returns claims on success.
//
// The claims value is the adopter's own; the api package stores it
// in the request context without interpreting it. A transport that
// needs to attribute a call to a principal and tenant asks
// [IdentityOf], which understands three shapes: a value implementing
// [Identity], a [Claims], or a string-keyed map carrying "sub" and
// "tenant" (the shape JWT libraries decode into). Anything else
// yields an empty identity — the call is still authenticated, it is
// merely unattributed in the audit trail.
type AuthFunc func(r *http.Request) (claims any, err error)

// Identity is the interface an adopter's claims type may implement
// so a transport can attribute a call without importing the type.
type Identity interface {
	// Principal is the stable identifier of the authenticated
	// caller: a user id, a service account, a client id.
	Principal() string
	// TenantID is the tenant the principal acts within, empty for
	// single-tenant tools.
	TenantID() string
}

// Claims is a ready-made claims value an [AuthFunc] may return when
// the adopter has no claims type of its own. Field names follow JWT
// registered-claim spelling where one exists.
type Claims struct {
	// Subject is the principal ("sub").
	Subject string `json:"sub"`
	// Tenant is the tenant identifier.
	Tenant string `json:"tenant,omitempty"`
	// Scopes are the entitlements the credential carries. They are
	// forwarded to the permission gate as Meta.Extra["scopes"],
	// comma-joined, so a policy can check them against a command's
	// kit/permissions annotation.
	Scopes []string `json:"scopes,omitempty"`
}

// Principal implements [Identity].
func (c Claims) Principal() string { return c.Subject }

// TenantID implements [Identity].
func (c Claims) TenantID() string { return c.Tenant }

// IdentityOf extracts the principal and tenant from the claims an
// [AuthFunc] returned. See [AuthFunc] for the shapes it understands.
// A nil or unrecognized value yields two empty strings.
func IdentityOf(claims any) (principal, tenant string) {
	switch c := claims.(type) {
	case nil:
		return "", ""
	case Identity:
		return c.Principal(), c.TenantID()
	}
	// A string-keyed map of any element type, including named map
	// types such as a JWT library's MapClaims, which a type switch
	// on map[string]any would not match.
	rv := reflect.ValueOf(claims)
	if rv.Kind() != reflect.Map || rv.Type().Key().Kind() != reflect.String {
		return "", ""
	}
	return mapString(rv, "sub"), mapString(rv, "tenant")
}

// ScopesOf returns the scopes a claims value carries: the Scopes of
// a [Claims], or a "scopes" entry of a string-keyed map holding a
// []string or []any of strings. Nil when there are none.
func ScopesOf(claims any) []string {
	switch c := claims.(type) {
	case Claims:
		return c.Scopes
	case *Claims:
		if c == nil {
			return nil
		}
		return c.Scopes
	}
	rv := reflect.ValueOf(claims)
	if !rv.IsValid() || rv.Kind() != reflect.Map || rv.Type().Key().Kind() != reflect.String {
		return nil
	}
	v := rv.MapIndex(reflect.ValueOf("scopes"))
	if !v.IsValid() {
		return nil
	}
	for v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Slice, reflect.Array:
		out := make([]string, 0, v.Len())
		for i := 0; i < v.Len(); i++ {
			e := v.Index(i)
			for e.Kind() == reflect.Interface && !e.IsNil() {
				e = e.Elem()
			}
			if e.Kind() == reflect.String {
				out = append(out, e.String())
			}
		}
		return out
	case reflect.String:
		return []string{v.String()}
	}
	return nil
}

// mapString reads key from a string-keyed map value, tolerating an
// element type of any or string.
func mapString(rv reflect.Value, key string) string {
	v := rv.MapIndex(reflect.ValueOf(key))
	if !v.IsValid() {
		return ""
	}
	for v.Kind() == reflect.Interface {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if v.Kind() == reflect.String {
		return v.String()
	}
	return ""
}

// AuthOption configures [Auth].
type AuthOption func(*authConfig)

type authConfig struct {
	onRefused func(r *http.Request, err error)
}

// OnAuthRefused installs a hook that observes every request the
// middleware refuses, with the AuthFunc's error, before the 401 is
// written. It exists so a refusal can be recorded in the same audit
// trail as the calls that were allowed; the hook cannot change the
// verdict.
func OnAuthRefused(fn func(r *http.Request, err error)) AuthOption {
	return func(c *authConfig) { c.onRefused = fn }
}

// Auth returns a middleware that calls fn to authenticate each request.
// On success, claims are stored in the request context. On error, a
// 401 JSON response is written.
func Auth(fn AuthFunc, opts ...AuthOption) Middleware {
	var cfg authConfig
	for _, o := range opts {
		o(&cfg)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, err := fn(r)
			if err != nil {
				if cfg.onRefused != nil {
					cfg.onRefused(r, err)
				}
				Error(w, http.StatusUnauthorized, &APIError{
					Status:  http.StatusUnauthorized,
					Code:    "unauthorized",
					Message: err.Error(),
				})
				return
			}
			ctx := context.WithValue(r.Context(), claimsKey{}, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ClaimsFromContext extracts auth claims stored by the Auth middleware.
func ClaimsFromContext(ctx context.Context) any {
	return ctx.Value(claimsKey{})
}
