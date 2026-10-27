package llm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"unicode"

	"hop.top/kit/go/core/netpolicy"
)

// ErrKeyEndpoint matches (via errors.Is) every [*KeyEndpointError].
var ErrKeyEndpoint = errors.New("llm: provider key refused for endpoint")

// KeyEndpointError reports a provider key kit will not send to an
// endpoint. Endpoint is masked by [RedactURI]; the key never appears.
type KeyEndpointError struct {
	Scheme   string
	Endpoint string
	// Insecure is set when the refusal is plain http to a remote host.
	Insecure bool
	// Malformed is set when the endpoint carries userinfo, a backslash,
	// a percent-escape, whitespace or a control character, or does not
	// parse: no credential goes to it.
	Malformed bool
	// Neither set: the endpoint came from the URI and is not trusted.
}

func (e *KeyEndpointError) Error() string {
	if e.Malformed {
		return fmt.Sprintf("llm: refusing to send the %s API key to %q: an endpoint must parse and carry "+
			"no userinfo, backslash, percent-escape, whitespace or control character", e.Scheme, e.Endpoint)
	}
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

// endpoint is a base URL parsed as the HTTP client parses it (net/url).
type endpoint struct {
	scheme, host, port string
}

// parseEndpoint parses raw for a credential check. ok is false, and no
// credential may go to raw, when it does not parse, has no host, or
// carries userinfo, a backslash, a percent-escape, whitespace or a
// control character: forms where parsers disagree on the host.
func parseEndpoint(raw string) (endpoint, bool) {
	if strings.ContainsAny(raw, "\\%") || strings.IndexFunc(raw, func(r rune) bool {
		return r <= ' ' || r == 0x7f || unicode.IsSpace(r)
	}) >= 0 {
		return endpoint{}, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Hostname() == "" {
		return endpoint{}, false
	}
	e := endpoint{scheme: strings.ToLower(u.Scheme), host: strings.ToLower(u.Hostname()), port: u.Port()}
	if e.port == "" {
		switch e.scheme {
		case "https":
			e.port = "443"
		case "http":
			e.port = "80"
		}
	}
	return e, true
}

func (e endpoint) origin() string { return e.scheme + "://" + net.JoinHostPort(e.host, e.port) }

func (e endpoint) loopback() bool { return netpolicy.IsLoopback(e.host) }

// CheckKeyEndpoint refuses, as a [*KeyEndpointError], a non-blank key
// over plain http to a host other than loopback. Adapters call it on
// the base URL they settle on; [Resolve] and [LoadConfig] already do.
func CheckKeyEndpoint(scheme, key, baseURL string) error {
	if blankKey(key) || baseURL == "" {
		return nil
	}
	e, ok := parseEndpoint(baseURL)
	if !ok {
		return &KeyEndpointError{Scheme: scheme, Endpoint: RedactURI(baseURL), Malformed: true}
	}
	if e.scheme == "http" && !e.loopback() {
		return &KeyEndpointError{Scheme: scheme, Endpoint: RedactURI(baseURL), Insecure: true}
	}
	return nil
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
	e, ok := parseEndpoint(endpoint)
	if !ok {
		return false
	}
	if e.loopback() {
		return true
	}
	for _, trusted := range append(r.trustedEndpoints(ctx, scheme), extra...) {
		if t, ok := parseEndpoint(trusted); ok && t.origin() == e.origin() {
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
