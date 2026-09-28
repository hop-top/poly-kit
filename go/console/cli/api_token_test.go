package cli_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/core/identity"
	"hop.top/kit/go/transport/authn"
)

// tokenRoot is a root with the api service and, when withIdentity, an
// identity keypair in a directory of its own.
func tokenRoot(t *testing.T, withIdentity bool, keys map[string]any) *cli.Root {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	opts := []func(*cli.Root){cli.WithAPI(cli.APIConfig{})}
	if withIdentity {
		// Before WithAPI on purpose: the order must not matter.
		opts = append([]func(*cli.Root){cli.WithIdentity(cli.IdentityConfig{Dir: filepath.Join(home, "id")})}, opts...)
	}
	r := cli.New(cli.Config{Name: "tool", Version: "1.0.0", DisableValidate: true}, opts...)
	for k, v := range keys {
		r.Viper.Set(k, v)
	}
	return r
}

// runToken executes `token <args>` and returns stdout and the error.
func runToken(t *testing.T, r *cli.Root, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	r.Cmd.SetOut(&out)
	r.Cmd.SetErr(io.Discard)
	r.SetArgs(append([]string{"token"}, args...))
	err := r.Execute(context.Background())
	return out.String(), err
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var oe *output.Error
	require.ErrorAs(t, err, &oe, err.Error())
	return oe.ExitCode
}

func payloadOf(t *testing.T, raw string) map[string]any {
	t.Helper()
	parts := strings.Split(raw, ".")
	require.Len(t, parts, 3)
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	return m
}

func headerOf(t *testing.T, raw string) map[string]any {
	t.Helper()
	data, err := base64.RawURLEncoding.DecodeString(strings.Split(raw, ".")[0])
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	return m
}

// TestTokenCreateSignsWithTheIdentityKeypair pins token create: an
// EdDSA JWT the identity keypair signed, its kid in the header, with
// the subject, tenant, audience, issuer, expiry, and the scopes both as
// the standard space-delimited scope claim and as the scopes list.
func TestTokenCreateSignsWithTheIdentityKeypair(t *testing.T) {
	r := tokenRoot(t, true, nil)
	require.NotNil(t, r.Identity)
	out, err := runToken(t, r, "create", "--sub", "alice", "--scopes", "items:read,items:write",
		"--tenant", "acme", "--audience", "kit-api", "--issuer", "tool", "--expires", "1h")
	require.NoError(t, err)
	raw := strings.TrimSpace(out)

	hdr := headerOf(t, raw)
	assert.Equal(t, "EdDSA", hdr["alg"])
	assert.Equal(t, r.Identity.PublicKeyID(), hdr["kid"])

	v, err := authn.NewJWT([]authn.Key{authn.IdentityKey(r.Identity)},
		authn.Options{Issuer: "tool", Audience: []string{"kit-api"}})
	require.NoError(t, err)
	tok, err := v.Verify(t.Context(), raw)
	require.NoError(t, err)
	assert.Equal(t, "alice", tok.Subject)
	assert.Equal(t, "acme", tok.Tenant)
	assert.Equal(t, []string{"items:read", "items:write"}, tok.Scopes)
	assert.WithinDuration(t, time.Now().Add(time.Hour), tok.Expiry, 5*time.Second)

	p := payloadOf(t, raw)
	assert.Equal(t, "items:read items:write", p["scope"], "the standard claim")
	assert.Equal(t, []any{"items:read", "items:write"}, p["scopes"], "the list, for older consumers")
}

// TestTokenCreateRefusals pins the usage errors, exit 2.
func TestTokenCreateRefusals(t *testing.T) {
	for name, args := range map[string][]string{
		"no subject":         {"create"},
		"no lifetime":        {"create", "--sub", "a", "--expires", "0s"},
		"a scope with space": {"create", "--sub", "a", "--scopes", "items read"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := runToken(t, tokenRoot(t, true, nil), args...)
			assert.Equal(t, 2, exitCode(t, err), "%v", err)
		})
	}
}

// TestTokenCommandMounting pins where token and its leaves appear:
// with Auth, claims, decode and verify; with an identity keypair,
// create too, with or without Auth; with neither, no token command.
func TestTokenCommandMounting(t *testing.T) {
	leaves := func(r *cli.Root) map[string]bool {
		for _, c := range r.Cmd.Commands() {
			if c.Name() == "token" {
				out := map[string]bool{}
				for _, l := range c.Commands() {
					out[l.Name()] = true
				}
				return out
			}
		}
		return nil
	}
	auth := cli.WithAPI(cli.APIConfig{Auth: func(*http.Request) (any, error) { return nil, nil }})

	got := leaves(cli.New(cli.Config{Name: "t", Version: "1", DisableValidate: true}, auth))
	assert.Equal(t, map[string]bool{"claims": true, "decode": true, "verify": true}, got)

	dir := t.TempDir()
	got = leaves(cli.New(cli.Config{Name: "t", Version: "1", DisableValidate: true},
		cli.WithAPI(cli.APIConfig{}), cli.WithIdentity(cli.IdentityConfig{Dir: dir})))
	assert.Equal(t, map[string]bool{"claims": true, "decode": true, "verify": true, "create": true}, got,
		"WithIdentity after WithAPI still mounts create")

	got = leaves(cli.New(cli.Config{Name: "t", Version: "1", DisableValidate: true},
		cli.WithIdentity(cli.IdentityConfig{Dir: dir}), auth))
	assert.True(t, got["create"])

	assert.Nil(t, leaves(cli.New(cli.Config{Name: "t", Version: "1", DisableValidate: true},
		cli.WithAPI(cli.APIConfig{}))))
}

// verdict is what token verify prints.
type verdict struct {
	Valid  bool           `json:"valid"`
	Error  string         `json:"error"`
	Mode   string         `json:"mode"`
	Sub    string         `json:"sub"`
	Scopes []string       `json:"scopes"`
	Claims map[string]any `json:"claims"`
}

func decodeVerdict(t *testing.T, out string) verdict {
	t.Helper()
	var v verdict
	require.NoError(t, json.Unmarshal([]byte(out), &v), out)
	return v
}

// TestTokenVerify pins token verify against the tool's own keypair: a
// token create made is valid, exit 0; expired, tampered, another
// signer's and garbage are refused, exit 5, with the reason and the
// unverified claims printed.
func TestTokenVerify(t *testing.T) {
	r := tokenRoot(t, true, nil)
	out, err := runToken(t, r, "create", "--sub", "alice", "--scopes", "items:read")
	require.NoError(t, err)
	good := strings.TrimSpace(out)

	out, err = runToken(t, r, "verify", good)
	require.NoError(t, err)
	v := decodeVerdict(t, out)
	assert.True(t, v.Valid)
	assert.Equal(t, "jwt", v.Mode)
	assert.Equal(t, "alice", v.Sub)
	assert.Equal(t, []string{"items:read"}, v.Scopes)

	now := time.Now()
	expired, err := r.Identity.SignJWT(identityClaims("alice", now.Add(-2*time.Hour), now.Add(-time.Hour)))
	require.NoError(t, err)
	parts := strings.Split(good, ".")
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"root","exp":9999999999}`)) + "." + parts[2]
	other := tokenRoot(t, true, nil)
	foreign, err := other.Identity.SignJWT(identityClaims("mallory", now, now.Add(time.Hour)))
	require.NoError(t, err)

	for name, c := range map[string]struct{ token, want string }{
		"expired":        {expired, "expired"},
		"tampered":       {tampered, "signature"},
		"another signer": {foreign, "signature"},
		"garbage":        {"not-a-jwt", "invalid token"},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := runToken(t, r, "verify", c.token)
			assert.Equal(t, 5, exitCode(t, err))
			v := decodeVerdict(t, out)
			assert.False(t, v.Valid)
			assert.Contains(t, v.Error, c.want)
		})
	}
	out, _ = runToken(t, r, "verify", expired)
	assert.Equal(t, "alice", decodeVerdict(t, out).Claims["sub"], "the unverified claims show why")
}

// TestTokenVerifyUsesTheServiceMode pins that token verify checks a
// token as the named service would: under auth.mode jwks it verifies
// against the key set, and a service with no bearer mode and a tool
// with no keypair has nothing to verify with, exit 2.
func TestTokenVerifyUsesTheServiceMode(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
			{Key: &priv.PublicKey, KeyID: "r1", Algorithm: "RS256", Use: "sig"},
		}})
	}))
	defer srv.Close()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: priv},
		(&jose.SignerOptions{}).WithHeader("kid", "r1"))
	require.NoError(t, err)
	raw, err := jwt.Signed(signer).Claims(map[string]any{
		"sub": "svc-a", "aud": "kit-api", "exp": time.Now().Add(time.Hour).Unix(),
	}).Serialize()
	require.NoError(t, err)

	r := tokenRoot(t, true, map[string]any{
		"services.rpc.auth.mode":          "jwks",
		"services.rpc.auth.jwks.url":      srv.URL,
		"services.rpc.auth.jwks.audience": "kit-api",
	})
	out, err := runToken(t, r, "verify", "--service", "rpc", raw)
	require.NoError(t, err, out)
	v := decodeVerdict(t, out)
	assert.True(t, v.Valid)
	assert.Equal(t, "jwks", v.Mode)

	_, err = runToken(t, r, "verify", raw)
	assert.Equal(t, 5, exitCode(t, err), "the api service has no jwks mode: the identity key does not verify it")

	bare := cli.New(cli.Config{Name: "tool", Version: "1.0.0", DisableValidate: true},
		cli.WithAPI(cli.APIConfig{Auth: func(*http.Request) (any, error) { return nil, nil }}))
	_, err = runToken(t, bare, "verify", raw)
	assert.Equal(t, 2, exitCode(t, err))
}

func identityClaims(sub string, iat, exp time.Time) identity.Claims {
	return identity.Claims{Subject: sub, IssuedAt: iat.Unix(), ExpiresAt: exp.Unix()}
}
