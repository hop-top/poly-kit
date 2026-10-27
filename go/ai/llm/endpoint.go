package llm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"hop.top/kit/go/core/netpolicy"
)

// ErrKeyEndpoint matches (via errors.Is) every [*KeyEndpointError].
var ErrKeyEndpoint = errors.New("llm: provider key refused for endpoint")

// KeyEndpointError reports a provider key kit will not send to an
// endpoint. Endpoint is masked by [RedactURI]; the key never appears.
type KeyEndpointError struct {
	Scheme   string
	Endpoint string
	// Insecure is set when the refusal is plain http to a remote host;
	// otherwise the endpoint came from the URI and is not trusted.
	Insecure bool
}

func (e *KeyEndpointError) Error() string {
	if e.Insecure {
		return fmt.Sprintf("llm: refusing to send the %s API key over plain http to %q; use https or a loopback host",
			e.Scheme, e.Endpoint)
	}
	return fmt.Sprintf("llm: refusing to send the %s API key to %q, which only the URI names; "+
		"set it as llm.yaml base_url or LLM_BASE_URL, or pass api_key in the URI",
		e.Scheme, e.Endpoint)
}

// Is matches [ErrKeyEndpoint].
func (e *KeyEndpointError) Is(target error) bool { return target == ErrKeyEndpoint }

// uriEndpoint is the endpoint a URI itself names: its base_url param,
// else its host. Empty when it names none.
func uriEndpoint(u URI) string {
	if v, ok := u.Params["base_url"]; ok && v != "" {
		return v
	}
	if u.Host != "" {
		return "http://" + u.Host
	}
	return ""
}

// origin is scheme://host:port of raw, lower case with the scheme's
// default port filled in; ok is false when raw has no host.
func origin(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	if port == "" {
		switch scheme {
		case "https":
			port = "443"
		case "http":
			port = "80"
		}
	}
	return scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port), true
}

// insecureEndpoint reports whether a key sent to raw would cross the
// network in clear: plain http to a host that is not loopback.
func insecureEndpoint(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false // unparseable: the adapter rejects it
	}
	return strings.EqualFold(u.Scheme, "http") && !netpolicy.IsLoopback(u.Host)
}

// CheckKeyEndpoint refuses, as a [*KeyEndpointError], a non-blank key
// over plain http to a host other than loopback. Adapters call it on
// the base URL they settle on; [Resolve] and [LoadConfig] already do.
func CheckKeyEndpoint(scheme, key, baseURL string) error {
	if blankKey(key) || baseURL == "" || !insecureEndpoint(baseURL) {
		return nil
	}
	return &KeyEndpointError{Scheme: scheme, Endpoint: RedactURI(baseURL), Insecure: true}
}

// guardKeyEndpoint checks that a key resolved from src may go where the
// URI points. A key from the URI itself is the caller's choice for any
// https host. Any other key goes to a URI-named endpoint only when it
// is loopback or shares an origin with the provider's default or
// declared endpoints, llm.yaml base_url or LLM_BASE_URL. Plain http to
// a remote host is refused whatever the key's source.
func (r *Registry) guardKeyEndpoint(ctx context.Context, u URI, src KeySource, key string) error {
	endpoint := uriEndpoint(u)
	if blankKey(key) || endpoint == "" {
		return nil
	}
	if err := CheckKeyEndpoint(u.Scheme, key, endpoint); err != nil {
		return err
	}
	if src.Kind == KeySourceURI {
		return nil
	}
	if r.credentialsTrusted(ctx, u.Scheme, endpoint) {
		return nil
	}
	return &KeyEndpointError{Scheme: u.Scheme, Endpoint: RedactURI(endpoint)}
}

// CredentialsTrusted reports whether credentials kit resolved itself,
// not taken from the URI (a stored or env key, a bearer token, a
// workspace header), may go to baseURL for scheme: it is loopback, or
// shares an origin with the scheme's default or declared endpoints, its
// llm.yaml base_url, LLM_BASE_URL or one of extra (an adapter's own
// base URL variable). Every credential path uses this one decision.
func CredentialsTrusted(scheme, baseURL string, extra ...string) bool {
	return DefaultRegistry.credentialsTrusted(context.Background(), scheme, baseURL, extra...)
}

func (r *Registry) credentialsTrusted(ctx context.Context, scheme, endpoint string, extra ...string) bool {
	if eu, err := url.Parse(strings.TrimSpace(endpoint)); err == nil && netpolicy.IsLoopback(eu.Host) {
		return true
	}
	want, ok := origin(endpoint)
	if !ok {
		return false
	}
	for _, trusted := range append(r.trustedEndpoints(ctx, scheme), extra...) {
		if got, ok := origin(trusted); ok && got == want {
			return true
		}
	}
	return false
}

// trustedEndpoints lists where scheme's key may go besides loopback.
func (r *Registry) trustedEndpoints(ctx context.Context, scheme string) []string {
	var out []string
	if rt, ok := r.route(ctx, scheme); ok {
		if base, err := rt.baseURL(scheme); err == nil && base != "" {
			out = append(out, base)
		}
		out = append(out, rt.decl.Endpoints...)
	}
	if fp, _, ok := r.providerBlock(ctx, scheme); ok && fp.BaseURL != "" {
		out = append(out, fp.BaseURL)
	}
	if v := os.Getenv("LLM_BASE_URL"); v != "" {
		out = append(out, v)
	}
	return out
}
