package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
)

// TestSelfSignedClientTokenIsAcceptedByAServedTool pins the
// self-signed client trust model end to end: tool A, an identity and no
// serve plane, mints a token with its cli-side `token create`; tool B
// serves the api under auth.mode jwt with A's public key in
// auth.jwt.public_key_files and accepts it as an authenticated caller.
// The same token tampered, and one another key signed, are refused.
func TestSelfSignedClientTokenIsAcceptedByAServedTool(t *testing.T) {
	dirA := t.TempDir()
	a := cli.New(cli.Config{Name: "a", Version: "1.0.0", DisableValidate: true},
		cli.WithIdentity(cli.IdentityConfig{Dir: filepath.Join(dirA, "id")}))
	_, served := lookup(a)
	require.False(t, served, "A has no serve plane")

	var out bytes.Buffer
	a.Cmd.SetOut(&out)
	a.Cmd.SetErr(io.Discard)
	a.SetArgs([]string{"token", "create", "--sub", "tool-a", "--audience", "kit-api", "--scopes", "items:read"})
	require.NoError(t, a.Execute(context.Background()))
	minted := strings.TrimSpace(out.String())
	require.NotNil(t, a.Identity)

	pub, err := a.Identity.MarshalPublicKey()
	require.NoError(t, err)
	pubFile := filepath.Join(dirA, "a.pub")
	require.NoError(t, os.WriteFile(pubFile, pub, 0o600))

	b := bearerRoot(t, map[string]any{
		"services.api.auth.mode":                 "jwt",
		"services.api.auth.jwt.audience":         "kit-api",
		"services.api.auth.jwt.public_key_files": []string{pubFile},
	}, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	require.NotEqual(t, a.Identity.PublicKeyID(), b.Identity.PublicKeyID(), "B trusts A only through the file")
	base, stop := serveAPI(t, b)
	defer stop()

	resp, body := get(t, base+"/v1/commands/secret", bearerHdr(minted))
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	assert.Contains(t, string(body), "unlocked")

	for name, tok := range map[string]string{
		"tampered payload": tamperSubject(t, minted, "mallory"),
		"another key":      otherIdentityToken(t),
	} {
		t.Run(name, func(t *testing.T) {
			resp, body := get(t, base+"/v1/commands/secret", bearerHdr(tok))
			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, string(body))
		})
	}
}

// tamperSubject rewrites raw's sub claim and keeps its signature.
func tamperSubject(t *testing.T, raw, sub string) string {
	t.Helper()
	parts := strings.Split(raw, ".")
	require.Len(t, parts, 3)
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	var claims map[string]any
	require.NoError(t, json.Unmarshal(data, &claims))
	claims["sub"] = sub
	data, err = json.Marshal(claims)
	require.NoError(t, err)
	parts[1] = base64.RawURLEncoding.EncodeToString(data)
	return strings.Join(parts, ".")
}
