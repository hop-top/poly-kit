package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"hop.top/kit/go/console/cli/svcconfig"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/authn"
)

// The bearer-token values of services.<svc>.auth.mode. Each selects a
// verifier from go/transport/authn, configured by the block of the
// same name under auth.
const (
	// AuthModeJWT verifies JWTs signed by the tool's own identity
	// keypair (cli.WithIdentity) and by the keys auth.jwt names.
	AuthModeJWT = "jwt"
	// AuthModeJWKS verifies JWTs against the JSON Web Key Set at
	// auth.jwks.url.
	AuthModeJWKS = "jwks"
	// AuthModeOIDC verifies JWTs an OpenID Connect provider issued,
	// its key set found by discovery from auth.oidc.issuer.
	AuthModeOIDC = "oidc"
)

// The auth sub-blocks, one per mode.
const (
	authJWTBlock  = "auth.jwt"
	authJWKSBlock = "auth.jwks"
	authOIDCBlock = "auth.oidc"
)

// authModes lists every auth.mode value, each with its block.
var authModes = []struct{ mode, block string }{
	{AuthModeMTLS, authMTLSBlock},
	{AuthModeJWT, authJWTBlock},
	{AuthModeJWKS, authJWKSBlock},
	{AuthModeOIDC, authOIDCBlock},
	{AuthModeAPIKey, authAPIKeyBlock},
}

// authModeNames is the auth.mode values, quoted, for messages.
func authModeNames() string {
	names := make([]string, len(authModes))
	for i, m := range authModes {
		names[i] = fmt.Sprintf("%q", m.mode)
	}
	return strings.Join(names, ", ")
}

// isAuthMode reports whether mode is a known auth.mode value.
func isAuthMode(mode string) bool {
	for _, m := range authModes {
		if m.mode == mode {
			return true
		}
	}
	return false
}

// checkAuthBlocks refuses a key of a mode's block set while auth.mode
// names another mode (or none): it would be silently ignored. A block
// under services.all is a shared default, so it is refused only while
// the mode in force is shared too; a service that names its own mode
// simply does not use the shared blocks of the others.
func (t tlsResolver) checkAuthBlocks(mode string) error {
	_, modeFrom, _ := t.cfg.Lookup(t.svc, authBlock, "mode")
	ownMode := strings.HasPrefix(modeFrom, svcconfig.Key(t.svc, authBlock, ""))
	for _, m := range authModes {
		if m.mode == mode {
			continue
		}
		b, _ := svcconfig.Lookup(m.block)
		for _, key := range b.Keys {
			_, from, ok := t.cfg.Lookup(t.svc, m.block, key)
			if !ok {
				continue
			}
			if ownMode && !strings.HasPrefix(from, svcconfig.Key(t.svc, m.block, "")) {
				continue
			}
			return fmt.Errorf("%s: set, but %s is not %q", from,
				svcconfig.Key(t.svc, authBlock, "mode"), m.mode)
		}
	}
	return nil
}

// bearerVerifier builds the verifier a bearer auth.mode selects, from
// the block of that mode. It fetches nothing: a JWKS or OIDC key set
// is fetched on the first request that needs it.
func (t tlsResolver) bearerVerifier(r *Root, mode string) (*authn.Verifier, error) {
	switch mode {
	case AuthModeJWT:
		return t.jwtVerifier(r)
	case AuthModeJWKS:
		u, uKey := t.str(authJWKSBlock, "url")
		if u == "" {
			return nil, fmt.Errorf("%s: auth.mode %q needs the key set's URL", uKey, AuthModeJWKS)
		}
		if err := authn.CheckURL(u); err != nil {
			return nil, fmt.Errorf("%s: %w", uKey, err)
		}
		opts, err := t.bearerOptions(authJWKSBlock, true)
		if err != nil {
			return nil, err
		}
		remote, err := t.remote(authJWKSBlock)
		if err != nil {
			return nil, err
		}
		return authn.NewJWKS(u, remote, opts)
	case AuthModeOIDC:
		issuer, issKey := t.str(authOIDCBlock, "issuer")
		if issuer == "" {
			return nil, fmt.Errorf("%s: auth.mode %q needs the provider's issuer URL", issKey, AuthModeOIDC)
		}
		if err := authn.CheckURL(issuer); err != nil {
			return nil, fmt.Errorf("%s: %w", issKey, err)
		}
		opts, err := t.bearerOptions(authOIDCBlock, true)
		if err != nil {
			return nil, err
		}
		remote, err := t.remote(authOIDCBlock)
		if err != nil {
			return nil, err
		}
		return authn.NewOIDC(opts.Issuer, remote, opts)
	}
	return nil, fmt.Errorf("%s: %q is not a bearer mode", svcconfig.Key(t.svc, authBlock, "mode"), mode)
}

// protectedResource is the OAuth protected resource a bearer mode
// describes: its authorization server — oidc's issuer, else the
// block's issuer — and its first audience that is an absolute http(s)
// URL, which the verifier already requires every token to carry. Nil
// when either is missing: a client refused there has nowhere to go.
func (t tlsResolver) protectedResource(mode string) *api.ProtectedResource {
	block := ""
	for _, m := range authModes {
		if m.mode == mode {
			block = m.block
		}
	}
	issuer, _ := t.str(block, "issuer")
	if issuer == "" {
		return nil
	}
	for _, aud := range t.list(block, "audience") {
		if pr, err := api.NewProtectedResource(aud, []string{issuer}, nil); err == nil {
			return pr
		}
	}
	return nil
}

// jwtVerifier builds the auth.mode: jwt verifier: the tool's identity
// key, when it has one, and every key auth.jwt.public_key_files names.
func (t tlsResolver) jwtVerifier(r *Root) (*authn.Verifier, error) {
	opts, err := t.bearerOptions(authJWTBlock, false)
	if err != nil {
		return nil, err
	}
	var keys []authn.Key
	if r != nil && r.Identity != nil {
		keys = append(keys, authn.IdentityKey(r.Identity))
	}
	filesKey := svcconfig.Key(t.svc, authJWTBlock, "public_key_files")
	if _, k, ok := t.cfg.Lookup(t.svc, authJWTBlock, "public_key_files"); ok {
		filesKey = k
	}
	for _, f := range t.list(authJWTBlock, "public_key_files") {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filesKey, err)
		}
		k, err := authn.ParsePublicKeyPEM(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %w", filesKey, f, err)
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%s: auth.mode %q has no key to verify with; the tool has no identity "+
			"keypair (cli.WithIdentity), so name the signer's public keys in %s",
			svcconfig.Key(t.svc, authBlock, "mode"), AuthModeJWT, filesKey)
	}
	return authn.NewJWT(keys, opts)
}

// bearerOptions reads the claim checks every bearer block shares:
// issuer, audience (required when needAudience: a third-party issuer
// mints tokens for every application it serves, and without an
// audience a token minted for any of them would be accepted here),
// clock_skew and tenant_claim.
func (t tlsResolver) bearerOptions(block string, needAudience bool) (authn.Options, error) {
	var opts authn.Options
	opts.Issuer, _ = t.str(block, "issuer")
	opts.Audience = t.list(block, "audience")
	if needAudience && len(opts.Audience) == 0 {
		return opts, fmt.Errorf("%s: required; name the audience (the API identifier or client id) "+
			"this service's tokens are issued for", svcconfig.Key(t.svc, block, "audience"))
	}
	opts.TenantClaim, _ = t.str(block, "tenant_claim")
	skew, set, err := t.duration(block, "clock_skew")
	if err != nil {
		return opts, err
	}
	if set {
		if skew < 0 {
			return opts, fmt.Errorf("%s: must not be negative", svcconfig.Key(t.svc, block, "clock_skew"))
		}
		opts.ClockSkew = skew
		if skew == 0 {
			opts.ClockSkew = -1 // authn: a negative skew tolerates none
		}
	}
	return opts, nil
}

// remote reads a remote key set's refresh interval.
func (t tlsResolver) remote(block string) (authn.Remote, error) {
	d, set, err := t.duration(block, "refresh")
	if err != nil {
		return authn.Remote{}, err
	}
	if set && d <= 0 {
		return authn.Remote{}, fmt.Errorf("%s: must be positive", svcconfig.Key(t.svc, block, "refresh"))
	}
	return authn.Remote{Refresh: d}, nil
}

// duration reads a duration key; set is false when it is unset.
func (t tlsResolver) duration(block, key string) (d time.Duration, set bool, err error) {
	raw, k, ok := t.cfg.Lookup(t.svc, block, key)
	if !ok {
		return 0, false, nil
	}
	d, err = durationValue(raw)
	if err != nil {
		return 0, true, fmt.Errorf("%s: %w", k, err)
	}
	return d, true, nil
}

// resolveAuthMode validates svc's auth blocks and resolves auth.mode:
// lowercased, "" when unset, and the key that set it.
func resolveAuthMode(cfg svcconfig.Resolver, svc string) (tlsResolver, string, string, error) {
	res := tlsResolver{cfg: cfg, svc: svc}
	for _, b := range []string{authBlock, authMTLSBlock, authJWTBlock, authJWKSBlock, authOIDCBlock, authAPIKeyBlock} {
		if err := cfg.ValidateBlock(b, svc, svcconfig.Shared); err != nil {
			return res, "", "", err
		}
	}
	mode, modeKey := res.str(authBlock, "mode")
	mode = strings.ToLower(mode)
	if mode == AuthModePeer {
		if modeKey == svcconfig.Key(svc, authBlock, "mode") {
			return res, "", "", fmt.Errorf("%s: %q reads a Unix socket peer's credentials, and only the %s service has a peer; "+
				"an HTTP listener supports %s", modeKey, mode, SocketServiceName, authModeNames())
		}
		// services.all.auth.mode: peer is the socket's default, which
		// an HTTP listener does not read.
		mode = ""
	}
	if mode != "" && !isAuthMode(mode) {
		return res, "", "", fmt.Errorf("%s: unknown mode %q; kit supports %s", modeKey, mode, authModeNames())
	}
	if err := res.checkAuthBlocks(mode); err != nil {
		return res, "", "", err
	}
	return res, mode, modeKey, nil
}

// ResolveServeVerifier returns the bearer-token verifier
// services.<svc>.auth.mode configures, or nil when the mode is unset
// or mtls. It validates the auth blocks as [ResolveServeTLS] does;
// every refusal names the key at fault.
func ResolveServeVerifier(r *Root, svc string) (*authn.Verifier, error) {
	if r == nil || r.Viper == nil {
		return nil, nil
	}
	res, mode, _, err := resolveAuthMode(svcconfig.New(r.Viper), svc)
	if err != nil {
		return nil, err
	}
	switch mode {
	case AuthModeJWT, AuthModeJWKS, AuthModeOIDC:
		return res.bearerVerifier(r, mode)
	}
	return nil, nil
}
