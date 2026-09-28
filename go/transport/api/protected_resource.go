package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ProtectedResourceWellKnown is the well-known path prefix of OAuth 2.0
// protected resource metadata (RFC 9728 §3). The document for a
// resource with a path is served at this prefix followed by the path.
const ProtectedResourceWellKnown = "/.well-known/oauth-protected-resource"

// ProtectedResource describes an HTTP endpoint as an OAuth 2.0
// protected resource (RFC 9728): the identifier its tokens are issued
// for and the authorization servers that issue them. It serves the
// metadata document clients discover it by — MCP clients among them —
// and names that document in the 401 challenge, so a client that was
// refused learns where to get a token.
type ProtectedResource struct {
	// Resource is the resource identifier: an absolute https URL (or
	// http to a loopback host), the endpoint a client calls, such as
	// https://mcp.example.com/mcp. Tokens must carry it as an
	// audience.
	Resource string `json:"resource"`
	// AuthorizationServers are the issuer identifiers of the
	// authorization servers whose tokens the resource accepts.
	AuthorizationServers []string `json:"authorization_servers"`
	// ScopesSupported are the scopes the resource understands; empty
	// omits them.
	ScopesSupported []string `json:"scopes_supported,omitempty"`
	// BearerMethodsSupported is how a token is sent; the Authorization
	// header only.
	BearerMethodsSupported []string `json:"bearer_methods_supported,omitempty"`
}

// NewProtectedResource checks resource and servers and returns the
// description: resource must be an absolute http(s) URL with no query
// or fragment, and at least one authorization server is required (the
// MCP authorization spec requires one; a client has nowhere to go
// without it).
func NewProtectedResource(resource string, servers, scopes []string) (*ProtectedResource, error) {
	u, err := url.Parse(resource)
	if err != nil {
		return nil, fmt.Errorf("protected resource %q: %w", resource, err)
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("protected resource %q: want an absolute http(s) URL with no query or fragment", resource)
	}
	if len(servers) == 0 {
		return nil, errors.New("protected resource: no authorization server")
	}
	return &ProtectedResource{
		Resource:               resource,
		AuthorizationServers:   servers,
		ScopesSupported:        scopes,
		BearerMethodsSupported: []string{"header"},
	}, nil
}

// MetadataPath is the path the metadata document is served at: the
// well-known prefix, then the resource's path (RFC 9728 §3.1).
func (p *ProtectedResource) MetadataPath() string {
	u, err := url.Parse(p.Resource)
	if err != nil {
		return ProtectedResourceWellKnown
	}
	path := strings.TrimSuffix(u.EscapedPath(), "/")
	return ProtectedResourceWellKnown + path
}

// MetadataURL is the document's absolute URL, on the resource's origin.
func (p *ProtectedResource) MetadataURL() string {
	u, err := url.Parse(p.Resource)
	if err != nil {
		return p.MetadataPath()
	}
	return u.Scheme + "://" + u.Host + p.MetadataPath()
}

// Challenge is the WWW-Authenticate challenge a refusal carries: the
// bearer scheme naming the metadata document (RFC 9728 §5.1), for
// [AuthChallenge].
func (p *ProtectedResource) Challenge() string {
	return fmt.Sprintf("Bearer resource_metadata=%q", p.MetadataURL())
}

// Handler serves the metadata document: GET answers it as JSON, with
// CORS open to any origin, since it is public by design (RFC 9728
// §3); OPTIONS answers a preflight; anything else is 405.
func (p *ProtectedResource) Handler() http.Handler {
	body, _ := json.Marshal(p)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		switch r.Method {
		case http.MethodOptions:
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet, http.MethodHead:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "public, max-age=3600")
			w.WriteHeader(http.StatusOK)
			if r.Method == http.MethodGet {
				_, _ = w.Write(body)
			}
		default:
			w.Header().Set("Allow", "GET, HEAD, OPTIONS")
			Error(w, http.StatusMethodNotAllowed, &APIError{
				Status: http.StatusMethodNotAllowed, Code: "method_not_allowed",
				Message: r.Method + " is not allowed on the protected resource metadata",
			})
		}
	})
}

// Guard is [Auth] for the resource: it answers the metadata document
// at [ProtectedResource.MetadataPath] to anyone, and every other
// request must pass fn, refused with the resource's challenge. opts
// apply to the Auth middleware; a challenge among them is replaced.
//
// A request it passes carries the resource on its context
// ([ProtectedResourceFrom]), so a later refusal names the same
// metadata document ([ScopeChallenge]).
func (p *ProtectedResource) Guard(fn AuthFunc, opts ...AuthOption) Middleware {
	doc := p.Handler()
	path := p.MetadataPath()
	auth := Auth(fn, append(opts, AuthChallenge(p.Challenge()))...)
	return func(next http.Handler) http.Handler {
		authed := auth(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == path {
				doc.ServeHTTP(w, r)
				return
			}
			authed.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), protectedResourceKey{}, p)))
		})
	}
}

type protectedResourceKey struct{}

// ProtectedResourceFrom returns the protected resource whose
// [ProtectedResource.Guard] passed the request ctx belongs to, or nil.
func ProtectedResourceFrom(ctx context.Context) *ProtectedResource {
	if ctx == nil {
		return nil
	}
	p, _ := ctx.Value(protectedResourceKey{}).(*ProtectedResource)
	return p
}
