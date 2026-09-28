package authn

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// Remote key set defaults.
const (
	// DefaultRefresh is how long a fetched key set is used before it
	// is fetched again.
	DefaultRefresh = time.Hour
	// DefaultMinRefresh is the least time between two fetches: a
	// token naming an unknown kid, or a fetch that failed, does not
	// trigger another fetch sooner.
	DefaultMinRefresh = time.Minute
	// DefaultFetchTimeout bounds one fetch when no HTTPClient is set.
	DefaultFetchTimeout = 10 * time.Second
	// maxDocumentBytes bounds a key set or discovery document.
	maxDocumentBytes = 1 << 20
)

// Remote configures how a remote key set is fetched and cached.
type Remote struct {
	// HTTPClient fetches the documents; nil means a client with
	// [DefaultFetchTimeout]. Tests put a recording transport here.
	HTTPClient *http.Client
	// Refresh is how long a fetched key set is used; zero means
	// [DefaultRefresh].
	Refresh time.Duration
	// MinRefresh is the least time between fetches; zero means
	// [DefaultMinRefresh].
	MinRefresh time.Duration
}

// NewJWKS returns a verifier for tokens signed by a key in the JSON
// Web Key Set at jwksURL. The set is fetched on first use, then again
// once it is older than Refresh, and when a token names a kid it does
// not hold — the issuer rotated its keys — at most once per
// MinRefresh. A failed refetch keeps the last good set. The URL must
// be https, or http to a loopback host.
func NewJWKS(jwksURL string, remote Remote, opts Options) (*Verifier, error) {
	if err := CheckURL(jwksURL); err != nil {
		return nil, fmt.Errorf("authn: jwks url: %w", err)
	}
	rk := newRemoteKeys(remote, opts, func(context.Context) (string, error) { return jwksURL, nil })
	return &Verifier{keys: rk, opts: opts}, nil
}

// NewOIDC returns a verifier for tokens an OpenID Connect provider
// issued: discovery (issuer + "/.well-known/openid-configuration")
// yields its jwks_uri, handled as [NewJWKS] handles its URL. The
// discovery document must name issuer exactly (OpenID Connect
// Discovery §4.3), and a token's iss must be issuer: opts.Issuer is
// set to it. Discovery runs on first use and is retried, at most once
// per MinRefresh, until it succeeds.
func NewOIDC(issuer string, remote Remote, opts Options) (*Verifier, error) {
	if err := CheckURL(issuer); err != nil {
		return nil, fmt.Errorf("authn: oidc issuer: %w", err)
	}
	opts.Issuer = issuer
	d := &discovery{issuer: issuer}
	rk := newRemoteKeys(remote, opts, nil)
	rk.resolve = func(ctx context.Context) (string, error) { return d.jwksURI(ctx, rk.client) }
	return &Verifier{keys: rk, opts: opts}, nil
}

// CheckURL accepts an https URL, or an http one to a loopback host
// (127.0.0.0/8, ::1, localhost): a key set fetched in plaintext over
// the network could be replaced in transit.
func CheckURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Host == "" {
		return fmt.Errorf("%q is not an absolute URL", raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if host == "localhost" {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
		return fmt.Errorf("%q: use https; plain http is accepted only to a loopback host", raw)
	}
	return fmt.Errorf("%q: scheme must be https", raw)
}

// remoteKeys is a fetched, cached key set.
type remoteKeys struct {
	client     *http.Client
	refresh    time.Duration
	minRefresh time.Duration
	now        func() time.Time
	// resolve yields the key set's URL: fixed for JWKS, discovered
	// for OIDC.
	resolve func(ctx context.Context) (string, error)

	mu          sync.Mutex
	keys        []jose.JSONWebKey
	fetched     time.Time
	lastAttempt time.Time
	lastErr     error
}

func newRemoteKeys(r Remote, opts Options, resolve func(context.Context) (string, error)) *remoteKeys {
	rk := &remoteKeys{
		client:     r.HTTPClient,
		refresh:    r.Refresh,
		minRefresh: r.MinRefresh,
		now:        opts.now,
		resolve:    resolve,
	}
	if rk.client == nil {
		rk.client = &http.Client{Timeout: DefaultFetchTimeout}
	}
	if rk.refresh <= 0 {
		rk.refresh = DefaultRefresh
	}
	if rk.minRefresh <= 0 {
		rk.minRefresh = DefaultMinRefresh
	}
	return rk
}

func (rk *remoteKeys) lookup(ctx context.Context, kid string) ([]jose.JSONWebKey, error) {
	rk.mu.Lock()
	defer rk.mu.Unlock()
	now := rk.now()
	if (rk.keys == nil || now.Sub(rk.fetched) >= rk.refresh) && rk.mayFetch(now) {
		rk.fetch(ctx, now)
	}
	if rk.keys == nil {
		return nil, fmt.Errorf("%w: %v", ErrKeySetUnavailable, rk.lastErr)
	}
	keys := match(rk.keys, kid)
	if len(keys) == 0 && kid != "" && rk.mayFetch(now) {
		// An unknown kid: the issuer may have rotated since the set
		// was fetched.
		rk.fetch(ctx, now)
		keys = match(rk.keys, kid)
	}
	return keys, nil
}

func (rk *remoteKeys) mayFetch(now time.Time) bool {
	return rk.lastAttempt.IsZero() || now.Sub(rk.lastAttempt) >= rk.minRefresh
}

// fetch replaces the key set; on failure it keeps the last good one
// and records the error.
func (rk *remoteKeys) fetch(ctx context.Context, now time.Time) {
	rk.lastAttempt = now
	u, err := rk.resolve(ctx)
	if err != nil {
		rk.lastErr = err
		return
	}
	var set jose.JSONWebKeySet
	if err := getJSON(ctx, rk.client, u, &set); err != nil {
		rk.lastErr = err
		return
	}
	var keys []jose.JSONWebKey
	for _, k := range set.Keys {
		// A key this package cannot use (an encryption key, an
		// unsupported type) is skipped rather than failing the set.
		if !k.Valid() || (k.Use != "" && k.Use != "sig") {
			continue
		}
		keys = append(keys, k.Public())
	}
	if len(keys) == 0 {
		rk.lastErr = fmt.Errorf("%s holds no signing key", u)
		return
	}
	rk.keys, rk.fetched, rk.lastErr = keys, now, nil
}

// discovery resolves and remembers an OpenID provider's jwks_uri.
type discovery struct {
	issuer string
	mu     sync.Mutex
	uri    string
}

func (d *discovery) jwksURI(ctx context.Context, client *http.Client) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.uri != "" {
		return d.uri, nil
	}
	wellKnown := strings.TrimSuffix(d.issuer, "/") + "/.well-known/openid-configuration"
	var doc struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"`
	}
	if err := getJSON(ctx, client, wellKnown, &doc); err != nil {
		return "", fmt.Errorf("discovery: %w", err)
	}
	if doc.Issuer != d.issuer {
		return "", fmt.Errorf("discovery: %s names issuer %q, not %q", wellKnown, doc.Issuer, d.issuer)
	}
	if doc.JWKSURI == "" {
		return "", fmt.Errorf("discovery: %s names no jwks_uri", wellKnown)
	}
	if err := CheckURL(doc.JWKSURI); err != nil {
		return "", fmt.Errorf("discovery: jwks_uri: %w", err)
	}
	d.uri = doc.JWKSURI
	return d.uri, nil
}

// getJSON fetches u and decodes its JSON body into dst.
func getJSON(ctx context.Context, client *http.Client, u string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", u, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDocumentBytes+1))
	if err != nil {
		return fmt.Errorf("GET %s: %w", u, err)
	}
	if len(body) > maxDocumentBytes {
		return fmt.Errorf("GET %s: document larger than %d bytes", u, maxDocumentBytes)
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("GET %s: %w", u, err)
	}
	return nil
}
