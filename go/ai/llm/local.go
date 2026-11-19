package llm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	llmerrors "hop.top/kit/go/ai/llm/errors"
)

// ErrNotLocal matches (via errors.Is) every [*NotLocalError].
var ErrNotLocal = errors.New("llm: endpoint is not local")

// NotLocalError reports an endpoint, or a provider, [LocalOnly] refuses.
// URI and Endpoint are masked by [RedactURI].
type NotLocalError struct {
	// URI is what was refused: the model URI, or for a request the
	// request URL. Empty for a provider [LocalOnlyClient] refuses.
	URI string
	// Endpoint is the base URL, or the host a request or a resolved
	// address pointed at. Empty when the scheme names none.
	Endpoint string
	// Locality is how the endpoint classified. Empty for a provider not
	// marked local, whose endpoint kit cannot see.
	Locality Locality
	// Provider is the Go type of a provider [LocalOnlyClient] refuses.
	Provider string
}

func (e *NotLocalError) Error() string {
	switch {
	case e.Provider != "":
		return fmt.Sprintf("%s: provider %s is not marked local (resolve it with LocalOnly)", ErrNotLocal, e.Provider)
	case e.Endpoint == "":
		return fmt.Sprintf("%s: %q names no endpoint kit can check", ErrNotLocal, e.URI)
	default:
		return fmt.Sprintf("%s: %s is %s (%q)", ErrNotLocal, e.Endpoint, e.Locality, e.URI)
	}
}

// Is matches [ErrNotLocal].
func (e *NotLocalError) Is(target error) bool { return target == ErrNotLocal }

// LocalOption tunes [LocalOnly].
type LocalOption func(*localConfig)

type localConfig struct {
	allowPrivate bool
	names        map[string]struct{}
	nets         []*net.IPNet
	err          error

	// seams for tests: the DNS lookup and the dial.
	lookup func(ctx context.Context, host string) ([]net.IP, error)
	dial   func(ctx context.Context, network, addr string) (net.Conn, error)
}

// AllowPrivate sets whether the local network counts as local: RFC 1918
// and fc00::/7 addresses. The default is true; false
// keeps requests on this host.
func AllowPrivate(allow bool) LocalOption {
	return func(c *localConfig) { c.allowPrivate = allow }
}

// AllowHosts names hosts [LocalOnly] accepts though they are not local
// by address: a DNS name ("gpu.tailnet.ts.net"), an IP, or a CIDR
// ("100.64.0.0/10" for a tailnet). Names match exactly and
// case-insensitively; whatever an allowed name resolves to is accepted.
// Tailnet addresses and names are not local by default. An entry that
// is none of those makes [Resolve] fail.
func AllowHosts(hosts ...string) LocalOption {
	return func(c *localConfig) {
		for _, h := range hosts {
			h = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
			switch ip := net.ParseIP(h); {
			case h == "":
				c.fail(h)
			case strings.Contains(h, "/"):
				_, n, err := net.ParseCIDR(h)
				if err != nil {
					c.fail(h)
					continue
				}
				c.nets = append(c.nets, n)
			case ip != nil:
				bits := 8 * len(ip)
				if ip4 := ip.To4(); ip4 != nil {
					ip, bits = ip4, 32
				}
				c.nets = append(c.nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			case strings.ContainsAny(h, " \\%@:"):
				c.fail(h)
			default:
				if c.names == nil {
					c.names = map[string]struct{}{}
				}
				c.names[h] = struct{}{}
			}
		}
	}
}

func (c *localConfig) fail(entry string) {
	if c.err == nil {
		c.err = fmt.Errorf("llm: AllowHosts: %q is not a host name, IP or CIDR", entry)
	}
}

func withLookup(f func(ctx context.Context, host string) ([]net.IP, error)) LocalOption {
	return func(c *localConfig) { c.lookup = f }
}

func withDial(f func(ctx context.Context, network, addr string) (net.Conn, error)) LocalOption {
	return func(c *localConfig) { c.dial = f }
}

// LocalOnly makes [Resolve] build a provider that talks only to this
// host or, by default, its local network:
//
//   - Resolve refuses, with a [*NotLocalError], a URI whose [Endpoint]
//     is not local, or names none kit can check. The decision is made
//     on the endpoint Resolve would use, so a base_url that points
//     elsewhere ("openai://localhost:8000/m?base_url=https://api.openai.com/v1")
//     is refused.
//   - Every request is checked again, whatever transport carries it:
//     one whose URL host is not local fails before it is sent. That
//     holds with a transport from [WithTransport] too, and whatever
//     endpoint an adapter settled on from its own environment.
//   - When kit owns the transport (no [WithTransport]), it dials only
//     addresses that are local: a name is resolved, and every address
//     it yields must be loopback, private (unless disallowed) or
//     explicitly allowed, or the dial fails; the checked address is
//     the one dialed. That transport ignores proxy environment
//     variables.
//   - The provider is marked local: [IsLocal] reports it, and
//     [LocalOnlyClient] accepts it. It forwards every capability
//     interface and has Unwrap for the adapter itself; build a [Client]
//     with [LocalOnlyClient], which unwraps, so capability probing stays
//     exact.
//
// Redirects are never followed (see [ResolvedConfig.HTTPClient]). Media
// URL fetches of a [Client] do not go through the provider and are not
// covered; pass media as bytes. An adapter built by calling its New
// directly is not covered either.
func LocalOnly(opts ...LocalOption) ResolveOption {
	c := &localConfig{allowPrivate: true}
	for _, o := range opts {
		o(c)
	}
	return func(o *resolveOptions) { o.local = c }
}

// accepts reports whether host (no port) is acceptable by name or
// literal address, and how it classified.
func (c *localConfig) accepts(host string) (Locality, bool) {
	h := strings.TrimSuffix(strings.ToLower(host), ".")
	loc := hostLocality(h)
	switch {
	case loc == LocalityLoopback, loc == LocalityPrivate && c.allowPrivate, c.explicit(h, nil):
		return loc, true
	}
	return loc, false
}

// explicit reports whether AllowHosts names host or ip.
func (c *localConfig) explicit(host string, ip net.IP) bool {
	if _, ok := c.names[host]; ok {
		return true
	}
	if ip == nil {
		ip = net.ParseIP(host)
	}
	if ip == nil {
		return false
	}
	for _, n := range c.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// guard is the Resolve-time check: it refuses a non-local endpoint and
// wraps cfg's transport to hold every request to the same rule.
func (c *localConfig) guard(uri string, rt route, cfg *ResolvedConfig) error {
	if c.err != nil {
		return c.err
	}
	refuse := func(endpoint string, loc Locality) error {
		return &NotLocalError{URI: RedactURI(uri), Endpoint: RedactURI(endpoint), Locality: loc}
	}
	base, err := endpointOf(rt, *cfg)
	if err != nil {
		return refuse("", LocalityRemote)
	}
	e, ok := parseEndpoint(base)
	if !ok || (e.scheme != "http" && e.scheme != "https") {
		return refuse(base, LocalityRemote)
	}
	if loc, ok := c.accepts(e.host); !ok {
		return refuse(base, loc)
	}
	cfg.Transport = c.transport(cfg.Transport)
	return nil
}

// transport wraps base, or when nil a transport of kit's own that
// guards the dial, with the per-request host check.
func (c *localConfig) transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = c.ownTransport()
	}
	return &localTransport{base: base, c: c}
}

func (c *localConfig) ownTransport() *http.Transport {
	var t *http.Transport
	if d, ok := http.DefaultTransport.(*http.Transport); ok {
		t = d.Clone()
	} else {
		t = &http.Transport{
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        100,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		}
	}
	t.Proxy = nil
	t.DialContext = c.dialContext
	t.DialTLSContext = nil
	return t
}

// dialContext resolves addr's host, refuses it unless every address it
// yields is acceptable, and dials the checked address.
func (c *localConfig) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	h := strings.TrimSuffix(strings.ToLower(host), ".")
	ips, err := c.resolve(ctx, h)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if !c.ipAccepted(h, ip) {
			return nil, &NotLocalError{URI: addr, Endpoint: ip.String(), Locality: ipLocality(ip)}
		}
	}
	dial := c.dial
	if dial == nil {
		dial = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	var last error
	for _, ip := range ips {
		conn, err := dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		last = err
	}
	return nil, last
}

func (c *localConfig) resolve(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	if c.lookup != nil {
		return c.lookup(ctx, host)
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, len(addrs))
	for i, a := range addrs {
		ips[i] = a.IP
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("llm: no address for %q", host)
	}
	return ips, nil
}

// ipAccepted: loopback always; private when allowed; anything else only
// when AllowHosts names the host it came from or the address.
func (c *localConfig) ipAccepted(host string, ip net.IP) bool {
	switch ipLocality(ip) {
	case LocalityLoopback:
		return true
	case LocalityPrivate:
		if c.allowPrivate {
			return true
		}
	}
	return c.explicit(host, ip)
}

// localTransport holds each request to the local rule before the base
// transport sees it.
type localTransport struct {
	base http.RoundTripper
	c    *localConfig
}

func (t *localTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if loc, ok := t.c.accepts(r.URL.Hostname()); !ok {
		if r.Body != nil {
			_ = r.Body.Close()
		}
		return nil, &NotLocalError{URI: RedactURI(r.URL.String()), Endpoint: RedactURI(r.URL.Scheme + "://" + r.URL.Host), Locality: loc}
	}
	return t.base.RoundTrip(r)
}

// ---------------------------------------------------------------------------
// Marking
// ---------------------------------------------------------------------------

// IsLocal reports whether p vouches for itself as local: it has a
// Local() bool method that returns true. What [LocalOnly] resolves does;
// so may a provider of your own that talks to a local server only.
func IsLocal(p Provider) bool {
	l, ok := p.(interface{ Local() bool })
	return ok && l.Local()
}

// localProvider is a provider [LocalOnly] built. It forwards every
// capability interface, answering ErrCapabilityNotSupported where the
// adapter lacks it; [LocalOnlyClient] unwraps it for exact probing.
type localProvider struct{ Provider }

func markLocal(p Provider) Provider {
	if p == nil || IsLocal(p) {
		return p
	}
	return localProvider{p}
}

// Local marks the provider local.
func (localProvider) Local() bool { return true }

// Unwrap returns the adapter.
func (l localProvider) Unwrap() Provider { return l.Provider }

func unmark(p Provider) Provider {
	if l, ok := p.(localProvider); ok {
		return l.Provider
	}
	return p
}

func (l localProvider) unsupported(capability string) error {
	return llmerrors.NewCapabilityNotSupported(capability, fmt.Sprintf("%T", l.Provider))
}

func (l localProvider) Complete(ctx context.Context, req Request) (Response, error) {
	if p, ok := l.Provider.(Completer); ok {
		return p.Complete(ctx, req)
	}
	return Response{}, l.unsupported("complete")
}

func (l localProvider) Stream(ctx context.Context, req Request) (TokenIterator, error) {
	if p, ok := l.Provider.(Streamer); ok {
		return p.Stream(ctx, req)
	}
	return nil, l.unsupported("stream")
}

func (l localProvider) CallWithTools(ctx context.Context, req Request, tools []ToolDef) (ToolResponse, error) {
	if p, ok := l.Provider.(ToolCaller); ok {
		return p.CallWithTools(ctx, req, tools)
	}
	return ToolResponse{}, l.unsupported("tool_call")
}

func (l localProvider) StreamWithTools(ctx context.Context, req Request, tools []ToolDef) (ToolStream, error) {
	if p, ok := l.Provider.(ToolStreamer); ok {
		return p.StreamWithTools(ctx, req, tools)
	}
	return nil, l.unsupported("stream_tools")
}

func (l localProvider) GenerateImage(ctx context.Context, req ImageRequest) (ImageResponse, error) {
	if p, ok := l.Provider.(ImageGenerator); ok {
		return p.GenerateImage(ctx, req)
	}
	return ImageResponse{}, l.unsupported("image_gen")
}

func (l localProvider) Transcribe(ctx context.Context, req TranscribeRequest) (TranscribeResponse, error) {
	if p, ok := l.Provider.(Transcriber); ok {
		return p.Transcribe(ctx, req)
	}
	return TranscribeResponse{}, l.unsupported("transcribe")
}

func (l localProvider) Synthesize(ctx context.Context, req SynthesizeRequest) (SynthesizeResponse, error) {
	if p, ok := l.Provider.(SpeechSynthesizer); ok {
		return p.Synthesize(ctx, req)
	}
	return SynthesizeResponse{}, l.unsupported("synthesize")
}

func (l localProvider) AnalyzeVideo(ctx context.Context, req VideoRequest) (VideoResponse, error) {
	if p, ok := l.Provider.(VideoAnalyzer); ok {
		return p.AnalyzeVideo(ctx, req)
	}
	return VideoResponse{}, l.unsupported("video_analyze")
}

func (l localProvider) GenerateVideo(ctx context.Context, req VideoGenRequest) (VideoResponse, error) {
	if p, ok := l.Provider.(VideoGenerator); ok {
		return p.GenerateVideo(ctx, req)
	}
	return VideoResponse{}, l.unsupported("video_gen")
}

// ---------------------------------------------------------------------------
// Client and pool
// ---------------------------------------------------------------------------

// LocalOnlyClient is [NewClient] for a chain that stays local: it
// refuses, with a [*NotLocalError], a primary or any [WithFallback]
// provider that is not marked local ([IsLocal]), so a cloud provider
// cannot sit in the chain. Resolve each with [LocalOnly]. The client
// holds the adapters themselves (the marker is unwrapped), so
// [Client.Capabilities] and [Client.Provider] are exact.
//
// It checks the providers it is given. A provider resolved without
// [LocalOnly] is refused; one resolved by another path (the picker, a
// pool, an llm.yaml fallback list, which are only names) must be
// resolved with [LocalOnly] before it reaches here.
func LocalOnlyClient(primary Provider, opts ...Option) (*Client, error) {
	if primary == nil || !IsLocal(primary) {
		return nil, notMarked(primary)
	}
	// Options are applied once, by NewClient; the fallbacks are read
	// back from the client. On a refusal the client is dropped, though
	// On* hooks it subscribed stay on a bus the caller passed.
	c := NewClient(unmark(primary), opts...)
	for i, f := range c.cfg.fallbacks {
		if f == nil || !IsLocal(f) {
			return nil, notMarked(f)
		}
		c.cfg.fallbacks[i] = unmark(f)
	}
	return c, nil
}

func notMarked(p Provider) error {
	return &NotLocalError{Provider: fmt.Sprintf("%T", p)}
}

// FilterLocal keeps the pool entries whose scheme reaches a local
// endpoint by default: the scheme's [Endpoint], local by [LocalOnly]'s
// default rule (ollama and lmstudio; not openai, anthropic or a catalog
// provider). Only scheme and model are known, so an entry is judged by
// its scheme's default alone: a host or base_url the caller later puts
// in a URI is not seen. It filters the pool; [LocalOnly] still has to
// guard what is resolved from it.
//
// A non-local entry is not dropped but returned with Enabled false, and
// the result is as long as the input: [PickProviderInPool] reads an
// empty pool as "accept everything", so a filter that emptied the pool
// would turn the filter off. Check Enabled, as the picker does. An empty
// input stays empty, so give a local-only picker an explicit pool.
func FilterLocal(entries []PoolEntry) []PoolEntry {
	if len(entries) == 0 {
		return entries
	}
	out := make([]PoolEntry, len(entries))
	copy(out, entries)
	policy := &localConfig{allowPrivate: true}
	for i, e := range out {
		if !e.Enabled {
			continue
		}
		base, err := Endpoint(e.Scheme + "://m")
		if err != nil {
			out[i].Enabled = false
			continue
		}
		pe, ok := parseEndpoint(base)
		if !ok {
			out[i].Enabled = false
			continue
		}
		if _, ok := policy.accepts(pe.host); !ok {
			out[i].Enabled = false
		}
	}
	return out
}
