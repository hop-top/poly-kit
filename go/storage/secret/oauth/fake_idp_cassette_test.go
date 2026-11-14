package oauth

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Fake IdPs reproduce the response shapes of the real token endpoints, for
// recording only. They check what the real services check (credential match,
// assertion signature, audience, issuer), so a cassette of a failure is a
// failure the provider would give for that input.

const (
	cassAssertionTypeJWT = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"
	cassGrantJWTBearer   = "urn:ietf:params:oauth:grant-type:jwt-bearer"
	// Fixed so a re-recorded cassette is byte-stable. Not credentials.
	cassTraceID       = "00000000-0000-4000-8000-0000000000a1"
	cassCorrelationID = "00000000-0000-4000-8000-0000000000b2"
)

func cassWriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func cassB64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// cassJWTAccessToken is the shape of an Entra v2 access token: a signed JWT.
// The signature is random bytes; nothing verifies it.
func cassJWTAccessToken(t testing.TB, tenant, appID string) string {
	hdr, _ := json.Marshal(map[string]any{"typ": "JWT", "alg": "RS256", "kid": cassRand(t, 22)})
	claims, _ := json.Marshal(map[string]any{
		"aud": "https://graph.microsoft.com", "iss": "https://sts.windows.net/" + tenant + "/",
		"tid": tenant, "appid": appID, "oid": cassUUID(t), "ver": "1.0",
	})
	sig := make([]byte, 64)
	for i := range sig {
		sig[i] = cassRand(t, 2)[0]
	}
	return cassB64(hdr) + "." + cassB64(claims) + "." + cassB64(sig)
}

// cassVerifyRS256 checks a compact RS256 JWT and returns its claims.
func cassVerifyRS256(tok string, pub *rsa.PublicKey) (map[string]any, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("not a JWT")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		return nil, err
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var c map[string]any
	return c, json.Unmarshal(raw, &c)
}

// cassEntra is a fake Entra v2 token endpoint for one tenant and app.
// Exactly one of secret and pub is set.
type cassEntra struct {
	t        testing.TB
	tenant   string
	clientID string
	secret   string
	pub      *rsa.PublicKey
}

func (e *cassEntra) tokenURL() string {
	return "https://login.microsoftonline.com/" + e.tenant + "/oauth2/v2.0/token"
}

func (e *cassEntra) fail(w http.ResponseWriter, status int, code string, aadsts int, msg string) {
	desc := fmt.Sprintf("AADSTS%d: %s\r\nTrace ID: %s\r\nCorrelation ID: %s\r\nTimestamp: 2026-01-02 03:04:05Z",
		aadsts, msg, cassTraceID, cassCorrelationID)
	cassWriteJSON(w, status, map[string]any{
		"error": code, "error_description": desc, "error_codes": []int{aadsts},
		"timestamp": "2026-01-02 03:04:05Z", "trace_id": cassTraceID, "correlation_id": cassCorrelationID,
		"error_uri": fmt.Sprintf("https://login.microsoftonline.com/error?code=%d", aadsts),
	})
}

func (e *cassEntra) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/"+e.tenant+"/oauth2/v2.0/token" || r.Host != "login.microsoftonline.com" {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	f := r.PostForm
	if f.Get("grant_type") != "client_credentials" {
		e.fail(w, 400, "unsupported_grant_type", 70003, "The app requested an unsupported grant type.")
		return
	}
	if f.Get("scope") == "" {
		e.fail(w, 400, "invalid_scope", 70011, "The provided request must include a 'scope' input parameter.")
		return
	}
	if f.Get("client_id") != e.clientID {
		e.fail(w, 400, "unauthorized_client", 700016, fmt.Sprintf(
			"Application with identifier '%s' was not found in the directory '%s'. This can happen if the application has not been installed by the administrator of the tenant or consented to by any user in the tenant.",
			f.Get("client_id"), e.tenant))
		return
	}
	switch {
	case e.pub != nil:
		cassCollect("observed-client-assertion", f.Get("client_assertion"))
		c, err := cassVerifyRS256(f.Get("client_assertion"), e.pub)
		if f.Get("client_assertion_type") != cassAssertionTypeJWT || err != nil ||
			c["aud"] != e.tokenURL() || c["iss"] != e.clientID || c["sub"] != e.clientID {
			e.fail(w, 401, "invalid_client", 700027, "Client assertion contains an invalid signature.")
			return
		}
	default:
		cassCollect("observed-client-secret", f.Get("client_secret"))
		if subtle.ConstantTimeCompare([]byte(f.Get("client_secret")), []byte(e.secret)) != 1 {
			e.fail(w, 401, "invalid_client", 7000215, fmt.Sprintf(
				"Invalid client secret provided. Ensure the secret being sent in the request is the client secret value, not the client secret ID, for a secret added to app '%s'.",
				e.clientID))
			return
		}
	}
	at := cassJWTAccessToken(e.t, e.tenant, e.clientID)
	cassCollect("entra-access-token", at)
	cassWriteJSON(w, 200, map[string]any{
		"token_type": "Bearer", "expires_in": 3599, "ext_expires_in": 3599, "access_token": at,
	})
}

// cassGoogle is a fake oauth2.googleapis.com/token for the jwt-bearer and
// refresh_token grants.
type cassGoogle struct {
	t            testing.TB
	saEmail      string
	saPub        *rsa.PublicKey
	subject      string
	scope        string
	clientID     string
	clientSecret string // optional
	refreshToken string
	rotateTo     string // new refresh token to return, if any
}

const cassGoogleTokenURL = "https://oauth2.googleapis.com/token"

func (g *cassGoogle) fail(w http.ResponseWriter, desc string) {
	cassWriteJSON(w, 400, map[string]string{"error": "invalid_grant", "error_description": desc})
}

func (g *cassGoogle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/token" || r.Host != "oauth2.googleapis.com" {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	f := r.PostForm
	resp := map[string]any{"expires_in": 3599, "token_type": "Bearer"}
	switch f.Get("grant_type") {
	case cassGrantJWTBearer:
		cassCollect("observed-jwt-assertion", f.Get("assertion"))
		c, err := cassVerifyRS256(f.Get("assertion"), g.saPub)
		switch {
		case err != nil:
			g.fail(w, "Invalid JWT Signature.")
			return
		case c["iss"] != g.saEmail || c["aud"] != cassGoogleTokenURL || c["scope"] != g.scope || (g.subject != "" && c["sub"] != g.subject):
			g.fail(w, "Invalid JWT: Token must be a short-lived token (60 minutes) and in a reasonable timeframe.")
			return
		}
	case "refresh_token":
		cassCollect("observed-refresh-token", f.Get("refresh_token"))
		if f.Get("client_id") != g.clientID || (g.clientSecret != "" && f.Get("client_secret") != g.clientSecret) {
			cassWriteJSON(w, 401, map[string]string{"error": "invalid_client", "error_description": "The OAuth client was not found."})
			return
		}
		if f.Get("refresh_token") != g.refreshToken {
			g.fail(w, "Token has been expired or revoked.")
			return
		}
		resp["scope"] = g.scope
		if g.rotateTo != "" {
			resp["refresh_token"] = g.rotateTo
		}
	default:
		cassWriteJSON(w, 400, map[string]string{"error": "unsupported_grant_type", "error_description": "Invalid grant_type: " + f.Get("grant_type")})
		return
	}
	at := "ya29." + cassRand(g.t, 40)
	cassCollect("google-access-token", at)
	resp["access_token"] = at
	cassWriteJSON(w, 200, resp)
}
