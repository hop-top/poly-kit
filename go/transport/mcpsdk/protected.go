package mcpsdk

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// verifiedHeader carries the identity [WithProtectedResource]'s
// verifier accepted from the HTTP layer to the tool handler: the SDK
// hands a handler the request's headers, not its context. It is
// server-owned — the recorder deletes whatever a client sent under
// this name and writes its own after verification — so a client
// cannot claim an identity by setting it.
const verifiedHeader = "X-Kit-Mcpsdk-Verified"

// verifiedCaller is the value of [verifiedHeader].
type verifiedCaller struct {
	Principal string   `json:"principal,omitempty"`
	Tenant    string   `json:"tenant,omitempty"`
	Scopes    []string `json:"scopes,omitempty"`
}

// WithProtectedResource puts the MCP authorization flow (MCP
// authorization spec, RFC 9728) in front of the surface's streamable
// HTTP handler: the protected resource metadata document answers at
// pr's metadata path to anyone, and every other request must carry a
// credential fn accepts — a bearer token from an authn verifier, say —
// or is refused 401 with a WWW-Authenticate challenge naming the
// document (resource_metadata), so an MCP client finds the
// authorization server. fn should bind the token's audience to
// pr.Resource.
//
// The caller fn verified becomes each tool call's provenance — its
// principal, tenant and scopes, established as verified — unless
// [WithCallMeta] supplies the provenance itself. Mount also routes the
// metadata path to the handler.
//
// Use it when nothing in front of the surface authenticates; a router
// whose own Auth wraps every route would refuse the public document.
func WithProtectedResource(pr *api.ProtectedResource, fn api.AuthFunc) Option {
	return func(c *config) {
		c.protected = pr
		c.protectAuth = fn
		c.protectSet = true
	}
}

// checkProtected refuses a WithProtectedResource missing either half.
func checkProtected(c config) error {
	if c.protectSet && (c.protected == nil || c.protectAuth == nil) {
		return errors.New("mcpsdk: WithProtectedResource needs a resource and a verifier")
	}
	return nil
}

// protect wraps h in the authorization flow when configured.
func (s *Surface) protect(h http.Handler) http.Handler {
	if s.cfg.protected == nil {
		return h
	}
	recorded := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del(verifiedHeader)
		p, tenant := api.IdentityOf(api.ClaimsFromContext(r.Context()))
		raw, _ := json.Marshal(verifiedCaller{
			Principal: p, Tenant: tenant, Scopes: api.ScopesOf(api.ClaimsFromContext(r.Context())),
		})
		r.Header.Set(verifiedHeader, string(raw))
		h.ServeHTTP(w, r)
	})
	return s.cfg.protected.Guard(s.cfg.protectAuth)(recorded)
}

// verifiedMeta is the provenance [verifiedHeader] carries, established
// as verified; ok is false when the request carries none.
func verifiedMeta(h http.Header) (cmdsurface.Meta, bool) {
	raw := h.Get(verifiedHeader)
	if raw == "" {
		return cmdsurface.Meta{}, false
	}
	var v verifiedCaller
	if json.Unmarshal([]byte(raw), &v) != nil {
		return cmdsurface.Meta{}, false
	}
	meta := cmdsurface.Meta{
		Caller:      v.Principal,
		Tenant:      v.Tenant,
		Established: cmdsurface.EstablishedVerified,
	}
	if len(v.Scopes) > 0 {
		// The entry the permission gate reads a credential's scopes
		// from, as every kit HTTP surface writes it.
		meta.Extra = map[string]string{"scopes": strings.Join(v.Scopes, ",")}
	}
	return meta, true
}
