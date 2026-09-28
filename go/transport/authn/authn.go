package authn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"hop.top/kit/go/transport/api"
)

// DefaultClockSkew is the tolerance [Options.ClockSkew] defaults to:
// how far the verifier's clock may disagree with the issuer's when it
// checks exp, nbf and iat.
const DefaultClockSkew = time.Minute

// DefaultTenantClaim is the claim the tenant is read from unless
// [Options.TenantClaim] names another.
const DefaultTenantClaim = "tenant"

// Errors a verification wraps, for errors.Is.
var (
	// ErrNoToken is a request that carries no bearer token.
	ErrNoToken = errors.New("authn: no bearer token")
	// ErrInvalidToken is a token that failed verification: malformed,
	// signed by no trusted key, expired, not yet valid, for another
	// issuer or audience, or refused by [Options.Check].
	ErrInvalidToken = errors.New("authn: invalid token")
	// ErrKeySetUnavailable is a remote key set (JWKS or OIDC) that
	// could not be fetched: the token was not judged, and a later
	// attempt may succeed.
	ErrKeySetUnavailable = errors.New("authn: key set unavailable")
)

// algorithms are the signature algorithms a token may use: asymmetric
// only. "none" is never accepted, and neither is HMAC, whose shared
// secret would let every verifier mint tokens.
var algorithms = []jose.SignatureAlgorithm{
	jose.EdDSA,
	jose.RS256, jose.RS384, jose.RS512,
	jose.PS256, jose.PS384, jose.PS512,
	jose.ES256, jose.ES384, jose.ES512,
}

// Options are the checks every verifier applies beyond the signature.
type Options struct {
	// Issuer, when set, must equal the token's iss.
	Issuer string
	// Audience, when set, must intersect the token's aud.
	Audience []string
	// ClockSkew tolerates clock disagreement on exp, nbf and iat.
	// Zero means [DefaultClockSkew]; a negative value tolerates none.
	ClockSkew time.Duration
	// TenantClaim names the claim holding the tenant; empty means
	// [DefaultTenantClaim].
	TenantClaim string
	// Check, when set, runs after every other check passed; a non-nil
	// error refuses the token. It is the revocation hook: look the
	// token's jti, subject or issue time up in a deny list.
	Check func(ctx context.Context, t *Token) error
	// Now is the clock; nil means time.Now. For tests.
	Now func() time.Time
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o Options) skew() time.Duration {
	switch {
	case o.ClockSkew == 0:
		return DefaultClockSkew
	case o.ClockSkew < 0:
		return 0
	}
	return o.ClockSkew
}

// Token is a verified token.
type Token struct {
	// Subject is sub: the principal.
	Subject string `json:"sub"`
	// Tenant is the tenant claim's value, empty when absent.
	Tenant string `json:"tenant,omitempty"`
	// Scopes are the entitlements the token carries.
	Scopes []string `json:"scopes,omitempty"`
	// Issuer is iss.
	Issuer string `json:"iss,omitempty"`
	// Audience is aud.
	Audience []string `json:"aud,omitempty"`
	// ID is jti.
	ID string `json:"jti,omitempty"`
	// KeyID is the kid of the key that verified the signature.
	KeyID string `json:"kid,omitempty"`
	// IssuedAt, NotBefore and Expiry are iat, nbf and exp; zero when
	// absent (exp never is).
	IssuedAt  time.Time `json:"iat,omitzero"`
	NotBefore time.Time `json:"nbf,omitzero"`
	Expiry    time.Time `json:"exp"`
	// Raw holds every claim the payload carries.
	Raw map[string]any `json:"-"`
}

// Claims returns the claims value an [api.AuthFunc] hands the
// transports: principal, tenant and scopes.
func (t *Token) Claims() api.Claims {
	return api.Claims{Subject: t.Subject, Tenant: t.Tenant, Scopes: t.Scopes}
}

// keySource yields the candidate keys for a token's kid; an empty kid
// yields every signing key. It returns [ErrKeySetUnavailable] when it
// has no key set to look in.
type keySource interface {
	lookup(ctx context.Context, kid string) ([]jose.JSONWebKey, error)
}

// Verifier verifies bearer tokens against one key source.
type Verifier struct {
	keys keySource
	opts Options
}

// Verify checks raw, a compact JWS, and returns the token it carries.
// Every refusal wraps [ErrInvalidToken], except a remote key set that
// could not be fetched, which wraps [ErrKeySetUnavailable].
func (v *Verifier) Verify(ctx context.Context, raw string) (*Token, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrNoToken
	}
	tok, err := jwt.ParseSigned(raw, algorithms)
	if err != nil {
		return nil, invalid("%v", err)
	}
	if len(tok.Headers) != 1 {
		return nil, invalid("want one signature, got %d", len(tok.Headers))
	}
	hdr := tok.Headers[0]
	keys, err := v.keys.lookup(ctx, hdr.KeyID)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		if hdr.KeyID != "" {
			return nil, invalid("no trusted key has kid %q", hdr.KeyID)
		}
		return nil, invalid("no trusted key")
	}

	var (
		std     jwt.Claims
		payload map[string]any
		used    *jose.JSONWebKey
	)
	for i := range keys {
		k := &keys[i]
		if k.Algorithm != "" && k.Algorithm != hdr.Algorithm {
			continue
		}
		if err := tok.Claims(k.Key, &std, &payload); err == nil {
			used = k
			break
		}
	}
	if used == nil {
		return nil, invalid("signature verifies with no trusted key")
	}

	if std.Expiry == nil {
		return nil, invalid("no exp: a token must expire")
	}
	exp := jwt.Expected{Issuer: v.opts.Issuer, AnyAudience: v.opts.Audience, Time: v.opts.now()}
	if err := std.ValidateWithLeeway(exp, v.opts.skew()); err != nil {
		switch {
		case errors.Is(err, jwt.ErrInvalidIssuer):
			return nil, invalid("issuer %q is not %q", std.Issuer, v.opts.Issuer)
		case errors.Is(err, jwt.ErrInvalidAudience):
			return nil, invalid("audience %v does not include %s", []string(std.Audience),
				strings.Join(v.opts.Audience, " or "))
		}
		return nil, invalid("%v", strings.TrimPrefix(err.Error(), "go-jose/go-jose/jwt: validation failed, "))
	}
	if std.Subject == "" {
		return nil, invalid("no sub: the token names no principal")
	}

	t := &Token{
		Subject:  std.Subject,
		Issuer:   std.Issuer,
		Audience: []string(std.Audience),
		ID:       std.ID,
		KeyID:    used.KeyID,
		Expiry:   std.Expiry.Time(),
		Tenant:   stringClaim(payload, v.opts.tenantClaim()),
		Scopes:   api.ScopesOf(payload),
		Raw:      payload,
	}
	if t.KeyID == "" {
		t.KeyID = hdr.KeyID
	}
	if std.IssuedAt != nil {
		t.IssuedAt = std.IssuedAt.Time()
	}
	if std.NotBefore != nil {
		t.NotBefore = std.NotBefore.Time()
	}
	if v.opts.Check != nil {
		if err := v.opts.Check(ctx, t); err != nil {
			return nil, invalid("%v", err)
		}
	}
	return t, nil
}

// AuthFunc returns the verifier as an [api.AuthFunc]: it reads the
// bearer token from the Authorization header and returns the token's
// [api.Claims].
func (v *Verifier) AuthFunc() api.AuthFunc {
	return func(r *http.Request) (any, error) {
		raw, err := BearerToken(r)
		if err != nil {
			return nil, err
		}
		t, err := v.Verify(r.Context(), raw)
		if err != nil {
			return nil, err
		}
		return t.Claims(), nil
	}
}

// BearerToken returns the token of r's "Authorization: Bearer"
// header (RFC 6750 §2.1; the scheme is case-insensitive). A request
// without one wraps [ErrNoToken].
func BearerToken(r *http.Request) (string, error) {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if h == "" {
		return "", ErrNoToken
	}
	scheme, tok, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(tok) == "" {
		return "", fmt.Errorf("%w: the Authorization header is not a bearer token", ErrNoToken)
	}
	return strings.TrimSpace(tok), nil
}

func (o Options) tenantClaim() string {
	if o.TenantClaim != "" {
		return o.TenantClaim
	}
	return DefaultTenantClaim
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidToken, fmt.Sprintf(format, args...))
}

// stringClaim reads a string claim; a number is formatted, anything
// else reads empty.
func stringClaim(payload map[string]any, name string) string {
	switch v := payload[name].(type) {
	case string:
		return v
	case float64, json.Number:
		return fmt.Sprint(v)
	}
	return ""
}
