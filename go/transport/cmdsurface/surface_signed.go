package cmdsurface

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"hop.top/kit/go/transport/api"
)

// SignedToken is the structured payload baked into a signed URL. The
// Issuer marshals SignedToken to JSON, HMAC-SHA256 signs the bytes
// with the configured key, and joins base64url(payload)."."base64url(tag)
// to form the URL's token segment.
type SignedToken struct {
	// Path is the cobra path of the leaf to invoke on visit.
	Path []string `json:"path"`
	// Args are positional arguments forwarded to the leaf.
	Args []string `json:"args,omitempty"`
	// Flags is the parsed flag set forwarded to the leaf.
	Flags map[string]any `json:"flags,omitempty"`
	// Nonce is the single-use identifier consumed on first visit.
	// When empty at Issue time the issuer generates one.
	Nonce string `json:"nonce"`
	// Iat is the issued-at unix timestamp (seconds).
	Iat int64 `json:"iat"`
	// Exp is the expiry unix timestamp (seconds). The verifier
	// refuses tokens whose Exp is in the past.
	Exp int64 `json:"exp"`
	// Caller, when set, is forwarded to Meta.Caller on the resulting
	// Invocation so audit sinks see who the URL was issued for.
	Caller string `json:"caller,omitempty"`
}

// SignedIssuer issues signed URLs for one-shot command exec.
//
// Key is the HMAC-SHA256 secret shared with the verifier. Store is
// the NonceStore the verifier consults; the issuer references it so
// IssueViaBridge can pre-check a path is invokable but does not write
// to it (Consume happens at verify time). URLPrefix is the
// public-facing base path prepended to the token; the verifier
// mounts at "{prefix}/{token}".
type SignedIssuer struct {
	Key       []byte
	Store     NonceStore
	URLPrefix string
}

// Issue produces a one-shot exec URL for t, valid for ttl. The
// returned URL embeds an opaque token that encodes the SignedToken +
// HMAC tag. Issue does NOT touch the NonceStore — the nonce is
// recorded only when the URL is visited (or pre-revoked by the
// caller via store.Revoke).
//
// When t.Nonce is empty Issue generates a 16-byte random nonce.
// Iat / Exp on t are overwritten with the current time and now+ttl.
func (s *SignedIssuer) Issue(_ context.Context, t SignedToken, ttl time.Duration) (string, error) {
	if len(s.Key) == 0 {
		return "", errors.New("cmdsurface: SignedIssuer.Key is empty")
	}
	if ttl <= 0 {
		return "", errors.New("cmdsurface: SignedIssuer.Issue ttl must be positive")
	}
	if t.Nonce == "" {
		n, err := randomNonce()
		if err != nil {
			return "", fmt.Errorf("cmdsurface: nonce: %w", err)
		}
		t.Nonce = n
	}
	now := time.Now()
	t.Iat = now.Unix()
	t.Exp = now.Add(ttl).Unix()

	payload, err := json.Marshal(t)
	if err != nil {
		return "", fmt.Errorf("cmdsurface: marshal token: %w", err)
	}
	tag := signedHMAC(s.Key, payload)
	token := base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(tag)

	prefix := strings.TrimRight(s.URLPrefix, "/")
	return prefix + "/" + token, nil
}

// IssueViaBridge is a convenience that pre-checks t.Path against b
// before issuing the URL. Returns ErrUnknownCommand /
// ErrSurfaceNotEnabled / ErrDestructiveBlocked if the URL would
// never be invokable on visit. This is the recommended issue path.
func (s *SignedIssuer) IssueViaBridge(ctx context.Context, b *Bridge, t SignedToken, ttl time.Duration) (string, error) {
	if b == nil {
		return "", errors.New("cmdsurface: IssueViaBridge: nil Bridge")
	}
	leaf, err := b.resolveLeaf(t.Path)
	if err != nil {
		return "", err
	}
	if !leaf.Enabled[SurfaceSigned] {
		return "", fmt.Errorf("%w: %s on %s",
			ErrSurfaceNotEnabled, leaf.PathKey(), SurfaceSigned)
	}
	if !b.cfg.policy.Allowed(leaf.Class, SurfaceSigned) {
		return "", fmt.Errorf("%w: %s on %s",
			ErrDestructiveBlocked, leaf.PathKey(), SurfaceSigned)
	}
	return s.Issue(ctx, t, ttl)
}

// SignedOption configures MountSigned.
type SignedOption func(*signedConfig)

type signedConfig struct {
	prefix          string
	successRedirect string
	errorRedirect   string
}

func defaultSignedConfig() signedConfig {
	return signedConfig{prefix: "/x"}
}

// WithSignedPrefix sets the URL prefix the verifier mounts under.
// Default is "/x". Trailing "/" is stripped.
func WithSignedPrefix(prefix string) SignedOption {
	return func(c *signedConfig) {
		c.prefix = strings.TrimRight(prefix, "/")
		if c.prefix == "" {
			c.prefix = "/x"
		}
	}
}

// WithSignedSuccessRedirect, when set, causes a successful visit to
// respond with 302 to the given URL instead of 200 + Result JSON.
// Useful for browser-facing magic links.
func WithSignedSuccessRedirect(url string) SignedOption {
	return func(c *signedConfig) { c.successRedirect = url }
}

// WithSignedErrorRedirect, when set, causes a failed visit to respond
// with 302 to the given URL with "?error=<code>" appended instead of
// the default plain-text error page.
func WithSignedErrorRedirect(url string) SignedOption {
	return func(c *signedConfig) { c.errorRedirect = url }
}

// MountSigned registers the verifier route at "{prefix}/{token}".
// On visit the handler:
//
//  1. Decodes + verifies the token (HMAC + expiry).
//  2. Consumes the nonce via store.
//  3. Builds an Invocation; Meta.Surface = SurfaceSigned, Meta.Caller
//     = token.Caller.
//  4. Calls bridge.Invoke; maps errors per the standard table.
//  5. Responds per ResponseMode (200 + Result JSON, or 302 to a
//     success page).
//
// The signed URL IS the auth (a bearer token effectively); the
// verifier skips Class.AuthRequired and Class.RequiresConfirmation
// gates. Destructive leaves still require Policy.AllowDestructiveOn
// to include SurfaceSigned — otherwise MountSigned refuses to mount.
//
// MountSigned exposes the Verify+Invoke endpoint. The issuer is
// constructed separately (callers may issue from a different process
// — e.g. a job worker — and only the verifier needs to be mounted on
// the public router).
func MountSigned(b *Bridge, r *api.Router, key []byte, store NonceStore, opts ...SignedOption) error {
	if b == nil {
		return errors.New("cmdsurface: nil Bridge")
	}
	if r == nil {
		return errors.New("cmdsurface: nil api.Router")
	}
	if len(key) == 0 {
		return errors.New("cmdsurface: empty signing key")
	}
	if store == nil {
		return errors.New("cmdsurface: nil NonceStore")
	}
	cfg := defaultSignedConfig()
	for _, o := range opts {
		o(&cfg)
	}

	// Refuse to mount when a SurfaceSigned-enabled leaf is
	// destructive but the policy does not opt in.
	policy := b.Policy()
	for _, leaf := range b.Leaves() {
		if !leaf.Enabled[SurfaceSigned] {
			continue
		}
		if leaf.Class.Destructive && !policy.Allowed(leaf.Class, SurfaceSigned) {
			return fmt.Errorf("%w: %s on signed (add SurfaceSigned to Policy.AllowDestructiveOn)",
				ErrDestructiveBlocked, leaf.PathKey())
		}
	}

	handler := newSignedHandler(b, key, store, cfg)
	r.Handle(http.MethodGet, cfg.prefix+"/{token}", handler)
	return nil
}

// newSignedHandler returns the GET handler that decodes, verifies,
// and dispatches a signed token from r.PathValue("token").
func newSignedHandler(b *Bridge, key []byte, store NonceStore, cfg signedConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := r.PathValue("token")
		token, payload, err := verifySigned(key, tok)
		if err != nil {
			writeSignedErr(w, r, cfg, err)
			return
		}
		_ = payload // future audit hook

		exp := time.Unix(token.Exp, 0)
		if err := store.Consume(r.Context(), token.Nonce, exp); err != nil {
			if errors.Is(err, ErrNonceUsed) {
				writeSignedErr(w, r, cfg, errSignedNonceUsed)
				return
			}
			writeSignedErr(w, r, cfg, fmt.Errorf("nonce store: %w", err))
			return
		}

		inv := Invocation{
			Path:  append([]string(nil), token.Path...),
			Args:  append([]string(nil), token.Args...),
			Flags: token.Flags,
			Meta: Meta{
				Surface:     SurfaceSigned,
				Caller:      token.Caller,
				RequestedAt: time.Now(),
			},
		}
		res, err := b.Invoke(r.Context(), inv)
		if err != nil {
			writeBridgeError(w, err)
			return
		}
		if cfg.successRedirect != "" {
			http.Redirect(w, r, cfg.successRedirect, http.StatusFound)
			return
		}
		api.JSON(w, http.StatusOK, res)
	}
}

// signedErr is a tagged error type so the response writer can map
// known verifier failures to stable APIError codes.
type signedErr struct {
	status int
	code   string
	msg    string
}

func (e *signedErr) Error() string { return e.msg }

var (
	errSignedMalformed    = &signedErr{status: http.StatusBadRequest, code: "malformed", msg: "malformed signed token"}
	errSignedBadSignature = &signedErr{status: http.StatusUnauthorized, code: "bad_signature", msg: "signed token signature mismatch"}
	errSignedExpired      = &signedErr{status: http.StatusUnauthorized, code: "expired", msg: "signed token expired"}
	errSignedNonceUsed    = &signedErr{status: http.StatusUnauthorized, code: "nonce_used", msg: "signed token nonce already used or revoked"}
)

// verifySigned splits, base64-decodes, HMAC-validates, JSON-decodes,
// and expiry-checks the supplied token string. Returns the decoded
// SignedToken plus the raw payload bytes (for downstream audit
// hooks). Sentinel signedErr instances on each failure mode.
func verifySigned(key []byte, raw string) (SignedToken, []byte, error) {
	var zero SignedToken
	parts := strings.Split(raw, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return zero, nil, errSignedMalformed
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return zero, nil, errSignedMalformed
	}
	tag, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return zero, nil, errSignedMalformed
	}
	want := signedHMAC(key, payload)
	if !hmac.Equal(want, tag) {
		return zero, nil, errSignedBadSignature
	}
	var t SignedToken
	if err := json.Unmarshal(payload, &t); err != nil {
		return zero, nil, errSignedMalformed
	}
	if time.Unix(t.Exp, 0).Before(time.Now()) {
		return zero, nil, errSignedExpired
	}
	return t, payload, nil
}

// writeSignedErr renders err either as a 302 to the configured error
// redirect (with ?error=<code>) or as a plain-text default page. The
// non-redirect path uses APIError for known signedErrs and the
// standard api.MapError for everything else so kit error contracts
// stay uniform across surfaces.
func writeSignedErr(w http.ResponseWriter, r *http.Request, cfg signedConfig, err error) {
	var se *signedErr
	if !errors.As(err, &se) {
		// Unknown — try mapping through api.MapError so downstream
		// domain errors render consistently.
		ae := api.MapError(err)
		writePlainSignedErr(w, cfg, ae.Status, ae.Code, ae.Message)
		return
	}
	if cfg.errorRedirect != "" {
		http.Redirect(w, r, appendErrorParam(cfg.errorRedirect, se.code), http.StatusFound)
		return
	}
	writePlainSignedErr(w, cfg, se.status, se.code, se.msg)
}

// writePlainSignedErr renders an APIError as JSON when no error
// redirect is configured. We do NOT fall back to a plain-text HTML
// page — JSON keeps the response machine-parseable for clients that
// programmatically follow the link.
func writePlainSignedErr(w http.ResponseWriter, _ signedConfig, status int, code, msg string) {
	api.Error(w, status, &api.APIError{Status: status, Code: code, Message: msg})
}

// appendErrorParam appends ?error=<code> (or &error=<code> when the
// URL already has a query) to base.
func appendErrorParam(base, code string) string {
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + "error=" + code
}

// signedHMAC returns the HMAC-SHA256 tag for payload under key.
func signedHMAC(key, payload []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	return mac.Sum(nil)
}

// randomNonce returns a 16-byte cryptographically-random nonce
// encoded as raw-url base64.
func randomNonce() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}
