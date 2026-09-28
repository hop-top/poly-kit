package cmdsurface

import (
	"net/http"
	"strings"

	"hop.top/kit/go/transport/api"
)

// scopesExtraKey is the Meta.Extra entry the permission gate reads a
// credential's scopes from.
const scopesExtraKey = "scopes"

// establishHTTP returns meta with its identity taken from the verdict
// of the [api.Auth] middleware that ran in front of r, if any did.
//
// A verified request replaces whatever the client claimed: Caller and
// Tenant become the verified principal and tenant, Extra["scopes"] the
// credential's scopes, and Established is [EstablishedVerified]. An
// unverified request keeps its claims as provenance, except a
// client-written Extra["scopes"], which the permission gate would
// otherwise read as a credential's, and leaves Established empty.
func establishHTTP(meta Meta, r *http.Request) Meta {
	meta.Established = EstablishedNone
	if _, claimed := meta.Extra[scopesExtraKey]; claimed {
		meta.Extra = copyExtraWithout(meta.Extra, scopesExtraKey)
	}
	if r == nil || !api.Authenticated(r.Context()) {
		return meta
	}
	claims := api.ClaimsFromContext(r.Context())
	meta.Caller, meta.Tenant = api.IdentityOf(claims)
	if scopes := api.ScopesOf(claims); len(scopes) > 0 {
		meta.Extra = copyExtraWithout(meta.Extra, "")
		meta.Extra[scopesExtraKey] = strings.Join(scopes, ",")
	}
	meta.Established = EstablishedVerified
	return meta
}

// copyExtraWithout returns a copy of extra without key, never nil, so
// a transport never writes into a map the caller still holds.
func copyExtraWithout(extra map[string]string, key string) map[string]string {
	out := make(map[string]string, len(extra)+1)
	for k, v := range extra {
		if k != key {
			out[k] = v
		}
	}
	return out
}

// writeUnauthenticated answers an [ErrAuthRefused] from the bridge on
// an HTTP surface: 401 unauthenticated with a WWW-Authenticate
// challenge, as the edge answers a failed credential.
func writeUnauthenticated(w http.ResponseWriter, err error) {
	api.WriteUnauthenticated(w, "", err.Error())
}
