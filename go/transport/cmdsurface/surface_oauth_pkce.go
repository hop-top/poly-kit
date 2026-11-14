package cmdsurface

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"hop.top/kit/go/storage/secret/oauth"
	"hop.top/kit/go/transport/api"
)

// ErrOAuthDenied marks a callback where the provider reported an error
// (typically access_denied: the user declined consent).
var ErrOAuthDenied = errors.New("cmdsurface: oauth authorization denied")

// PKCEProvider declares an OAuth authorization-code + PKCE (S256) provider
// that exchanges the code itself and yields a refresh-token grant. It is the
// PKCE counterpart of OAuthProvider; MountOAuth is unchanged.
type PKCEProvider struct {
	// Name is the URL slug: /oauth/{Name}/authorize and /callback.
	Name string
	// Config carries ClientID, Endpoint, Scopes and RedirectURL (the
	// absolute callback URL; the loopback helper fills it in). A
	// ClientSecret makes it a confidential client; the zero value is a
	// public client. Endpoint.AuthStyle is forced to AuthStyleInParams.
	Config oauth2.Config
	// SecretRef is where the host stores the refresh-token envelope
	// (Grant.SecretRef). Required.
	SecretRef string
	// ClientSecretRef is where the host stores Config.ClientSecret
	// (Grant.ClientSecretRef). Required iff Config.ClientSecret is set.
	ClientSecretRef string
	// ExpectedIssuer enables RFC 9207 iss validation, as on OAuthProvider.
	ExpectedIssuer string
	// AuthParams are extra authorization-URL options (access_type=offline,
	// prompt=consent, ...).
	AuthParams []oauth2.AuthCodeOption
	// HTTPClient is used for the token exchange; nil = default.
	HTTPClient *http.Client
	// OnResult receives the exchange outcome after state, issuer and
	// exchange checks pass. A returned error fails the callback.
	OnResult func(ctx context.Context, res PKCEResult) error
	// SecureCookie forces the state-binding cookie to be Secure and
	// __Host- prefixed even when the request did not arrive over TLS
	// (a TLS-terminating proxy in front). Leave false to follow r.TLS.
	SecureCookie bool
	// ErrorRedirect / SuccessRedirect behave as on OAuthProvider.
	ErrorRedirect   string
	SuccessRedirect string
}

// PKCEResult is a completed consent: a validated refresh_token Grant, the uma
// "secret" envelope to store at Grant.SecretRef, and the first access token.
// Persist via oauth.EncodeGrant and RefreshSecret; formatting is redacted.
type PKCEResult struct {
	Provider      string
	Grant         oauth.Grant
	RefreshSecret []byte
	Token         oauth.Token
}

func (PKCEResult) String() string               { return "cmdsurface.PKCEResult{[redacted]}" }
func (r PKCEResult) GoString() string           { return r.String() }
func (PKCEResult) MarshalJSON() ([]byte, error) { return []byte(`"[redacted]"`), nil }
func (r PKCEResult) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(r.String())) }

// callbackHook observes terminal callback outcomes (loopback helper).
type pkceHooks struct {
	// done is called with the final result or error. fatal=false errors
	// (stray state failures) must not end a login.
	done func(res PKCEResult, err error, fatal bool)
	// noAuthorize skips the generic /authorize route (loopback pre-issues
	// its single state instead).
	noAuthorize bool
}

// MountOAuthPKCE registers GET {prefix}/{Name}/authorize and /callback for
// each provider. Authorize generates an S256 code_verifier, stores it with
// the state in store and redirects with the challenge. Callback validates
// and consumes state first (a missing or unknown state is a non-fatal 400),
// then checks issuer and provider error, exchanges the code with the stored
// verifier and hands the PKCEResult to OnResult.
//
// Authorize also sets an HttpOnly, SameSite=Lax cookie holding a random nonce
// whose hash is stored with the state; callback requires it before consuming
// the state, so a callback URL minted by an attacker's browser is useless in
// the victim's (login CSRF). A missing or wrong cookie is a non-fatal 400 and
// leaves the state usable. The cookie is __Host- prefixed and Secure when the
// request arrived over TLS or PKCEProvider.SecureCookie is set (use it behind
// a TLS-terminating proxy); otherwise it is a plain cookie.
func MountOAuthPKCE(r *api.Router, providers []PKCEProvider, store PKCEStateStore, opts ...OAuthOption) error {
	return mountPKCE(r, providers, store, pkceHooks{}, opts...)
}

func mountPKCE(r *api.Router, providers []PKCEProvider, store PKCEStateStore, h pkceHooks, opts ...OAuthOption) error {
	if r == nil {
		return errors.New("cmdsurface: nil api.Router")
	}
	if store == nil {
		return errors.New("cmdsurface: nil PKCEStateStore")
	}
	cfg := defaultOAuthConfig()
	for _, o := range opts {
		o(&cfg)
	}
	seen := map[string]bool{}
	for i, p := range providers {
		if p.Name == "" {
			return fmt.Errorf("cmdsurface: pkce provider %d: empty Name", i)
		}
		if seen[p.Name] {
			return fmt.Errorf("cmdsurface: pkce provider %s: duplicate Name", p.Name)
		}
		seen[p.Name] = true
		switch {
		case p.Config.ClientID == "" || p.Config.Endpoint.AuthURL == "" || p.Config.Endpoint.TokenURL == "":
			return fmt.Errorf("cmdsurface: pkce provider %s: Config needs ClientID and Endpoint", p.Name)
		case p.SecretRef == "":
			return fmt.Errorf("cmdsurface: pkce provider %s: empty SecretRef", p.Name)
		case (p.Config.ClientSecret != "") != (p.ClientSecretRef != ""):
			return fmt.Errorf("cmdsurface: pkce provider %s: ClientSecret and ClientSecretRef must be set together", p.Name)
		}
		if p.ExpectedIssuer != "" {
			u, err := url.Parse(p.ExpectedIssuer)
			if err != nil || !u.IsAbs() || u.RawQuery != "" || u.Fragment != "" {
				return fmt.Errorf("cmdsurface: pkce provider %s: ExpectedIssuer is not an absolute URL without query or fragment", p.Name)
			}
		}
	}
	for _, p := range providers {
		p.Config.Endpoint.AuthStyle = oauth2.AuthStyleInParams
		if !h.noAuthorize {
			r.Handle(http.MethodGet, cfg.prefix+"/"+p.Name+"/authorize", pkceAuthorize(p, store, cfg, h))
		}
		r.Handle(http.MethodGet, cfg.prefix+"/"+p.Name+"/callback", pkceCallback(p, store, cfg, h))
	}
	return nil
}

func (p PKCEProvider) errProvider() OAuthProvider {
	return OAuthProvider{Name: p.Name, ErrorRedirect: p.ErrorRedirect, SuccessRedirect: p.SuccessRedirect}
}

// pkceCookieName is per provider so concurrent logins do not clobber each
// other. The __Host- prefix needs Secure, hence TLS requests only.
func pkceCookieName(provider string, secure bool) string {
	n := "oauth_state_" + strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, provider)
	if secure {
		return "__Host-" + n
	}
	return n
}

func pkceBindingHash(nonce string) string {
	sum := sha256.Sum256([]byte(nonce))
	return hex.EncodeToString(sum[:])
}

func pkceAuthorize(p PKCEProvider, store PKCEStateStore, cfg oauthConfig, h pkceHooks) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		verifier := oauth2.GenerateVerifier()
		var nb [32]byte
		if _, err := rand.Read(nb[:]); err != nil {
			oauthWriteError(w, p.errProvider(), http.StatusInternalServerError, "internal_error", "state store failure")
			return
		}
		nonce := base64.RawURLEncoding.EncodeToString(nb[:])
		state, err := store.Issue(r.Context(), p.Name, verifier, pkceBindingHash(nonce), cfg.stateTTL)
		if err != nil {
			if errors.Is(err, ErrStateStoreFull) {
				oauthWriteError(w, p.errProvider(), http.StatusServiceUnavailable, "unavailable", "too many pending authorizations")
				return
			}
			oauthWriteError(w, p.errProvider(), http.StatusInternalServerError, "internal_error", "state store failure")
			return
		}
		secure := r.TLS != nil || p.SecureCookie
		maxAge := 0
		if cfg.stateTTL > 0 {
			maxAge = int((cfg.stateTTL + time.Second - 1) / time.Second)
		}
		http.SetCookie(w, &http.Cookie{
			Name: pkceCookieName(p.Name, secure), Value: nonce, Path: "/", MaxAge: maxAge,
			HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
		})
		opts := append([]oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier)}, p.AuthParams...)
		http.Redirect(w, r, p.Config.AuthCodeURL(state, opts...), http.StatusFound)
	}
}

func pkceCallback(p PKCEProvider, store PKCEStateStore, cfg oauthConfig, h pkceHooks) http.HandlerFunc {
	ep := p.errProvider()
	fail := func(w http.ResponseWriter, status int, code, msg string, err error, fatal bool) {
		oauthWriteError(w, ep, status, code, msg)
		if h.done != nil {
			h.done(PKCEResult{}, err, fatal)
		}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		// State first: only a callback carrying a valid state may end the
		// flow. Anything else is a stray request and must not abort it.
		state := q.Get("state")
		if state == "" {
			fail(w, http.StatusBadRequest, "missing_state", "state query parameter required",
				errors.New("cmdsurface: oauth callback missing state"), false)
			return
		}
		var verifier string
		var err error
		if !h.noAuthorize {
			secure := r.TLS != nil || p.SecureCookie
			name := pkceCookieName(p.Name, secure)
			c, cerr := r.Cookie(name)
			if cerr != nil || c.Value == "" {
				fail(w, http.StatusBadRequest, "invalid_state", "state not bound to this browser",
					ErrBindingMismatch, false)
				return
			}
			verifier, err = store.Consume(r.Context(), p.Name, state, pkceBindingHash(c.Value))
			if err == nil {
				http.SetCookie(w, &http.Cookie{
					Name: name, Value: "", Path: "/", MaxAge: -1,
					HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
				})
			}
		} else {
			verifier, err = store.Consume(r.Context(), p.Name, state, "")
		}
		if err != nil {
			msg := "unknown or expired state"
			if errors.Is(err, ErrBindingMismatch) {
				msg = "state not bound to this browser"
			}
			fail(w, http.StatusBadRequest, "invalid_state", msg, err, false)
			return
		}
		if p.ExpectedIssuer != "" {
			iss := q.Get("iss")
			if iss == "" {
				fail(w, http.StatusBadRequest, "missing_iss", "iss query parameter required",
					errors.New("cmdsurface: oauth callback missing iss"), true)
				return
			}
			if iss != p.ExpectedIssuer {
				fail(w, http.StatusBadRequest, "issuer_mismatch", "iss does not match expected issuer",
					errors.New("cmdsurface: oauth callback issuer mismatch"), true)
				return
			}
		}
		if perr := q.Get("error"); perr != "" {
			fail(w, http.StatusBadRequest, "provider_error:"+perr, "",
				fmt.Errorf("%w: %s", ErrOAuthDenied, perr), true)
			return
		}
		code := q.Get("code")
		if code == "" {
			fail(w, http.StatusBadRequest, "missing_code", "code query parameter required",
				errors.New("cmdsurface: oauth callback missing code"), true)
			return
		}
		res, err := pkceExchange(r.Context(), p, code, verifier)
		if err != nil {
			status := http.StatusBadGateway
			if !errors.Is(err, oauth.ErrTransient) {
				status = http.StatusBadRequest
			}
			fail(w, status, "exchange_failed", "token exchange failed", err, true)
			return
		}
		if p.OnResult != nil {
			if err := p.OnResult(r.Context(), res); err != nil {
				fail(w, http.StatusInternalServerError, "internal_error", "result handler failed",
					fmt.Errorf("cmdsurface: oauth result handler: %w", err), true)
				return
			}
		}
		oauthWriteSuccess(w, ep)
		if h.done != nil {
			h.done(res, nil, true)
		}
	}
}

// pkceExchange swaps code for tokens with the stored verifier. Errors wrap
// the oauth package sentinels and never carry token endpoint text.
func pkceExchange(ctx context.Context, p PKCEProvider, code, verifier string) (PKCEResult, error) {
	cfg := p.Config
	cfg.Endpoint.AuthStyle = oauth2.AuthStyleInParams
	// Never follow redirects: Go replays POST bodies on 307/308, which would
	// leak the code, verifier and client secret to the redirect target.
	hc := http.Client{}
	if p.HTTPClient != nil {
		hc = *p.HTTPClient
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ctx = context.WithValue(ctx, oauth2.HTTPClient, &hc)
	tok, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return PKCEResult{}, pkceClassify(err)
	}
	if tok.RefreshToken == "" {
		return PKCEResult{}, fmt.Errorf("cmdsurface: no refresh token issued (request offline access): %w", oauth.ErrNeedsReconsent)
	}
	grant, err := oauth.NewRefreshGrant(cfg.Endpoint.TokenURL, cfg.ClientID, p.SecretRef, p.ClientSecretRef, cfg.Scopes)
	if err != nil {
		return PKCEResult{}, fmt.Errorf("cmdsurface: build refresh grant: %w", err)
	}
	env, err := oauth.EncodeRefreshSecret(tok.RefreshToken)
	if err != nil {
		return PKCEResult{}, err
	}
	var exp time.Time
	if !tok.Expiry.IsZero() {
		exp = tok.Expiry
	}
	return PKCEResult{
		Provider: p.Name, Grant: grant, RefreshSecret: env,
		Token: oauth.Token{AccessToken: tok.AccessToken, ExpiresAt: exp},
	}, nil
}

// pkceClassify aligns with the shared token-endpoint mapping: 5xx, 429, 408
// and network failures are transient; a TLS verification failure is not (a
// retry cannot fix an untrusted certificate).
func pkceClassify(err error) error {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		st := 0
		if re.Response != nil {
			st = re.Response.StatusCode
		}
		switch {
		case st >= 500 || st == http.StatusTooManyRequests || st == http.StatusRequestTimeout:
			return fmt.Errorf("cmdsurface: token endpoint failure: %w", oauth.ErrTransient)
		case re.ErrorCode == "invalid_grant":
			return fmt.Errorf("cmdsurface: authorization code rejected: %w", oauth.ErrNeedsReconsent)
		default:
			return fmt.Errorf("cmdsurface: client rejected by token endpoint: %w", oauth.ErrCredentialRejected)
		}
	}
	var cve *tls.CertificateVerificationError
	var ua x509.UnknownAuthorityError
	var ci x509.CertificateInvalidError
	var he x509.HostnameError
	if errors.As(err, &cve) || errors.As(err, &ua) || errors.As(err, &ci) || errors.As(err, &he) {
		return errors.New("cmdsurface: token endpoint certificate verification failed")
	}
	return fmt.Errorf("cmdsurface: token exchange transport failure: %w", oauth.ErrTransient)
}
