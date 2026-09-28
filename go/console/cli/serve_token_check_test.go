package cli

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/identity"
	"hop.top/kit/go/transport/authn"
)

// denySubject is a revocation check refusing every credential issued
// to sub.
func denySubject(sub string, seen *[]*authn.Token) func(context.Context, *authn.Token) error {
	return func(_ context.Context, t *authn.Token) error {
		*seen = append(*seen, t)
		if t.Subject == sub {
			return errors.New("subject " + sub + " is revoked")
		}
		return nil
	}
}

// TestTokenCheckAppliesToConfiguredBearerMode pins WithTokenCheck on a
// verifier chosen in configuration (services.api.auth.mode: jwt): a
// token the check refuses is 401 like any invalid token; one it
// passes runs.
func TestTokenCheckAppliesToConfiguredBearerMode(t *testing.T) {
	var seen []*authn.Token
	r := bearerRoot(t, map[string]any{
		"services.api.auth.mode":         "jwt",
		"services.api.auth.jwt.audience": "kit-api",
	}, WithAPI(APIConfig{Addr: "127.0.0.1:0"}), WithTokenCheck(denySubject("mallory", &seen)))
	base, stop := serveAPI(t, r)
	defer stop()

	alice := identityToken(t, r, identity.Claims{Audience: identity.Audience{"kit-api"}})
	resp, body := get(t, base+"/v1/commands/secret", bearerHdr(alice))
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))

	mallory := identityToken(t, r, identity.Claims{Subject: "mallory", Audience: identity.Audience{"kit-api"}})
	resp, body = get(t, base+"/v1/commands/list", bearerHdr(mallory))
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, string(body))
	assert.Contains(t, string(body), "subject mallory is revoked")
	require.Len(t, seen, 2, "the check runs once per verified token")
	assert.Equal(t, "alice", seen[0].Subject)
}

// TestTokenCheckAppliesToAPIKeys pins WithTokenCheck under
// auth.mode: apikey: the key is checked as a token naming its
// principal, tenant, scopes and key id.
func TestTokenCheckAppliesToAPIKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "apikeys.db")
	out, err := runKey(t, path, "create", "--sub", "ci-bot", "--tenant", "acme", "--scopes", "items:read")
	require.NoError(t, err)
	key := strings.TrimSpace(out)

	var seen []*authn.Token
	r := apiKeyRoot(t, path, WithTokenCheck(denySubject("ci-bot", &seen)))
	base, stop := serveAPI(t, r)
	defer stop()

	resp, body := get(t, base+"/v1/commands/list", map[string]string{"X-API-Key": key})
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, string(body))
	assert.Contains(t, string(body), "subject ci-bot is revoked")
	require.Len(t, seen, 1)
	assert.Equal(t, "acme", seen[0].Tenant)
	assert.Equal(t, []string{"items:read"}, seen[0].Scopes)
	assert.Equal(t, strings.Split(key, "_")[1], seen[0].ID)
}
