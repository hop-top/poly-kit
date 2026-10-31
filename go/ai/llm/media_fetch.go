package llm

// The guarded fetch behind URLSource.Reader.
//
// A media URL may come from untrusted input, so the fetch refuses what
// would make it a server-side request forgery or a local file read:
//
//   - http and https only (no file:, ftp:, data:), no userinfo;
//   - every address the host resolves to must be public: loopback,
//     private, link-local (cloud metadata 169.254.169.254 among them),
//     shared, documentation, reserved, unspecified and multicast
//     addresses are refused, v4-mapped and NAT64 IPv6 included;
//   - the connection goes to a vetted address itself (the dialer
//     re-checks the address it connects to), so a second DNS answer
//     cannot rebind it; TLS still verifies the host name;
//   - no proxy, no redirects; a non-2xx status is an error;
//   - the body is capped (DefaultMaxMediaBytes, or MediaMaxBytes), with
//     a timeout;
//   - the --offline policy (core/netpolicy) applies.
//
// Errors name the destination without query or userinfo. There is no
// switch to allow private hosts: media on a local or private host is
// read by the caller and passed as InlineSource or FileSource.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	llmerrors "hop.top/kit/go/ai/llm/errors"
	"hop.top/kit/go/core/netpolicy"
)

// DefaultMaxMediaBytes caps a URLSource download unless MediaMaxBytes
// sets another limit: 32 MiB.
const DefaultMaxMediaBytes int64 = 32 << 20

// mediaFetchTimeout bounds a whole URLSource download, body included.
const mediaFetchTimeout = 30 * time.Second

// ErrMediaURLRefused matches (errors.Is) every refusal of a URLSource
// destination: scheme, userinfo, missing host, or a non-public address.
var ErrMediaURLRefused = errors.New("media URL refused")

type mediaRefusedError struct {
	dest string
	why  string
}

func (e *mediaRefusedError) Error() string {
	return "media: refusing to fetch " + e.dest + ": " + e.why
}

func (e *mediaRefusedError) Is(target error) bool { return target == ErrMediaURLRefused }

// Test seams: the address predicate and the resolver. Production code
// never reassigns them.
var (
	mediaAddrAllowed = isPublicAddr
	mediaLookup      = func(ctx context.Context, host string) ([]netip.Addr, error) {
		return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	}
)

var mediaClient = newMediaClient()

// newMediaClient never follows a redirect: the 3xx is returned as-is.
// CheckRedirect composes that verdict after the cross-origin header
// check.
func newMediaClient() *http.Client {
	c := netpolicy.NewClient(&http.Transport{
		Proxy:                 nil,
		DialContext:           dialPublic,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	})
	c.CheckRedirect = netpolicy.CheckRedirect(func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse })
	c.Timeout = mediaFetchTimeout
	return c
}

// fetchMedia GETs raw under the rules in the file comment and returns
// its body, capped at maxBytes.
func fetchMedia(ctx context.Context, raw string, maxBytes int64) (io.ReadCloser, error) {
	dest := mediaDestination(raw)
	refuse := func(why string) error { return &mediaRefusedError{dest: dest, why: why} }
	u, err := url.Parse(raw)
	if err != nil {
		return nil, refuse("URL does not parse")
	}
	if s := strings.ToLower(u.Scheme); s != "http" && s != "https" {
		return nil, refuse("only http or https URLs are fetched")
	}
	if u.User != nil {
		return nil, refuse("URL carries userinfo")
	}
	if u.Hostname() == "" {
		return nil, refuse("URL has no host")
	}
	// The address check runs in dialPublic, beneath the offline guard:
	// literal or resolved, it judges what is about to be dialed.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, refuse("URL does not parse")
	}
	req.Header.Set("Accept", "*/*")
	resp, err := mediaClient.Do(req)
	if err != nil {
		var r *mediaRefusedError
		if errors.As(err, &r) {
			r.dest = dest
			return nil, r
		}
		// *url.Error quotes the whole URL; keep its cause only.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("media: fetch %s: %w", dest, err)
	}
	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		_ = resp.Body.Close()
		return nil, llmerrors.NewHTTPStatusError(resp.StatusCode, "media: redirect not followed ("+dest+")")
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		_ = resp.Body.Close()
		return nil, fmt.Errorf("media: fetch %s: http %d", dest, resp.StatusCode)
	}
	tooLarge := func(size int64) error {
		return llmerrors.NewMediaTooLarge(size, maxBytes, "media", fmt.Errorf("fetch %s", dest))
	}
	if resp.ContentLength > maxBytes {
		_ = resp.Body.Close()
		return nil, tooLarge(resp.ContentLength)
	}
	return &cappedBody{rc: resp.Body, max: maxBytes, tooLarge: tooLarge}, nil
}

// cappedBody fails a read that takes the body past max bytes.
type cappedBody struct {
	rc       io.ReadCloser
	n, max   int64
	tooLarge func(int64) error
}

func (b *cappedBody) Read(p []byte) (int, error) {
	if b.n > b.max {
		return 0, b.tooLarge(b.n)
	}
	// Read at most one byte past the limit: enough to tell it was
	// exceeded.
	if room := b.max - b.n + 1; int64(len(p)) > room {
		p = p[:room]
	}
	n, err := b.rc.Read(p)
	b.n += int64(n)
	if b.n > b.max {
		return n - int(b.n-b.max), b.tooLarge(b.n)
	}
	return n, err
}

func (b *cappedBody) Close() error { return b.rc.Close() }

// dialPublic resolves addr's host once, refuses it unless every address
// is allowed, and dials the vetted addresses themselves; the dialer's
// Control re-checks the address each socket connects to.
func dialPublic(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	var addrs []netip.Addr
	if a, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{a}
	} else if addrs, err = mediaLookup(ctx, host); err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, &mediaRefusedError{why: "host resolves to no address"}
	}
	for _, a := range addrs {
		if !mediaAddrAllowed(a) {
			return nil, &mediaRefusedError{why: "non-public address " + a.String()}
		}
	}
	d := &net.Dialer{Timeout: mediaFetchTimeout, Control: controlPublic}
	var firstErr error
	for _, a := range addrs {
		c, err := d.DialContext(ctx, network, net.JoinHostPort(a.WithZone("").String(), port))
		if err == nil {
			return c, nil
		}
		var r *mediaRefusedError
		if errors.As(err, &r) {
			return nil, r
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, firstErr
}

// controlPublic refuses a socket about to connect to a disallowed
// address. dialPublic only hands it vetted addresses; this is the check
// on the address actually connected to.
func controlPublic(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return &mediaRefusedError{why: "unparseable dial address"}
	}
	if !mediaAddrAllowed(ap.Addr()) {
		return &mediaRefusedError{why: "non-public address " + ap.Addr().String()}
	}
	return nil
}

// nonPublic lists the special-purpose ranges (IANA registries) that
// netip's predicates leave as global unicast.
var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("5f00::/16"),
	netip.MustParsePrefix("fec0::/10"),
}

var nat64 = netip.MustParsePrefix("64:ff9b::/96")

// isPublicAddr reports whether a is a globally routable unicast
// address. A v4-mapped or NAT64 (64:ff9b::/96) address is judged by the
// IPv4 address it carries.
func isPublicAddr(a netip.Addr) bool {
	a = a.WithZone("").Unmap()
	if nat64.Contains(a) {
		b := a.As16()
		a = netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
	}
	if !a.IsValid() || !a.IsGlobalUnicast() || a.IsPrivate() {
		return false
	}
	for _, p := range nonPublic {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// mediaDestination renders raw as scheme, host and path only: enough
// to say where a refused fetch was going, nothing it carried (query,
// fragment and userinfo may hold credentials). An opaque URL (data:)
// keeps its scheme alone; one that does not parse is not echoed.
func mediaDestination(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparseable URL>"
	}
	if u.Opaque != "" {
		return u.Scheme + ":"
	}
	d := url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path, RawPath: u.RawPath}
	return d.String()
}
