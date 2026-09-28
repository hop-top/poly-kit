package authn_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/storage/kv/memory"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/authn"
)

// TestAPIKeyLifecycle pins issue, verify, list and revoke: the key
// verifies to its principal, tenant and scopes; the store holds its
// hash, never the secret; a revoked key stops at once and stays listed.
func TestAPIKeyLifecycle(t *testing.T) {
	store := memory.New()
	clk := &fakeClock{t: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	keys := authn.NewAPIKeys(store, clk.now)
	ctx := t.Context()

	raw, rec, err := keys.Create(ctx, authn.NewAPIKey{Principal: "ci-bot", Tenant: "acme", Scopes: []string{"items:read"}})
	require.NoError(t, err)
	parts := strings.Split(raw, "_")
	require.Len(t, parts, 3)
	assert.Equal(t, "kit", parts[0])
	assert.Equal(t, rec.ID, parts[1])
	assert.Len(t, parts[2], 64, "a 256-bit secret")

	stored, ok, err := store.Get(ctx, "apikey/"+rec.ID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.NotContains(t, string(stored), parts[2], "the secret is never stored")

	got, err := keys.Verify(ctx, raw)
	require.NoError(t, err)
	assert.Equal(t, api.Claims{Subject: "ci-bot", Tenant: "acme", Scopes: []string{"items:read"}}, got.Claims())

	clk.advance(time.Second)
	raw2, _, err := keys.Create(ctx, authn.NewAPIKey{Principal: "alice"})
	require.NoError(t, err)
	list, err := keys.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, "alice", list[0].Principal, "newest first")

	revoked, err := keys.Revoke(ctx, rec.ID)
	require.NoError(t, err)
	assert.False(t, revoked.RevokedAt.IsZero())
	_, err = keys.Verify(ctx, raw)
	require.ErrorIs(t, err, authn.ErrInvalidToken)
	assert.Contains(t, err.Error(), "revoked")
	_, err = keys.Verify(ctx, raw2)
	assert.NoError(t, err, "revoking one key leaves the others")
	list, err = keys.List(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 2, "a revoked key stays listed")

	_, err = keys.Revoke(ctx, "0000000000000000")
	assert.ErrorIs(t, err, authn.ErrAPIKeyNotFound)
}

// TestAPIKeyRefusals pins every refusal: expiry, a wrong secret, an
// unknown id, a malformed key.
func TestAPIKeyRefusals(t *testing.T) {
	clk := &fakeClock{t: time.Now()}
	keys := authn.NewAPIKeys(memory.New(), clk.now)
	ctx := t.Context()
	raw, rec, err := keys.Create(ctx, authn.NewAPIKey{Principal: "p", TTL: time.Hour})
	require.NoError(t, err)

	_, err = keys.Verify(ctx, raw)
	require.NoError(t, err)

	wrongSecret := "kit_" + rec.ID + "_" + strings.Repeat("0", 64)
	unknownID := "kit_" + strings.Repeat("a", 16) + "_" + strings.Split(raw, "_")[2]
	for name, c := range map[string]struct{ key, want string }{
		"wrong secret": {wrongSecret, "unknown api key"},
		"unknown id":   {unknownID, "unknown api key"},
		"malformed":    {"kit_nothex_x", "not a kit api key"},
		"a jwt":        {"eyJ.eyJ.sig", "not a kit api key"},
		"uppercase":    {strings.ToUpper(raw), "not a kit api key"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := keys.Verify(ctx, c.key)
			require.ErrorIs(t, err, authn.ErrInvalidToken)
			assert.Contains(t, err.Error(), c.want)
		})
	}

	clk.advance(2 * time.Hour)
	_, err = keys.Verify(ctx, raw)
	require.ErrorIs(t, err, authn.ErrInvalidToken)
	assert.Contains(t, err.Error(), "expired")

	_, _, err = keys.Create(ctx, authn.NewAPIKey{})
	assert.Error(t, err, "a key names a principal")
	_, _, err = keys.Create(ctx, authn.NewAPIKey{Principal: "p", Scopes: []string{"a b"}})
	assert.Error(t, err)
}

// TestAPIKeyAuthFunc pins where the key is read from — X-API-Key, else
// Authorization: Bearer — and that the scopes reach api.ScopesOf.
func TestAPIKeyAuthFunc(t *testing.T) {
	keys := authn.NewAPIKeys(memory.New(), nil)
	raw, _, err := keys.Create(t.Context(), authn.NewAPIKey{Principal: "svc", Scopes: []string{"items:export", "other"}})
	require.NoError(t, err)
	h := api.Auth(keys.AuthFunc())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := api.ClaimsFromContext(r.Context())
		p, _ := api.IdentityOf(c)
		_, _ = w.Write([]byte(p + " " + strings.Join(api.ScopesOf(c), ",")))
	}))
	for name, c := range map[string]struct {
		hdr    map[string]string
		status int
		body   string
	}{
		"x-api-key":      {map[string]string{"X-API-Key": raw}, http.StatusOK, "svc items:export,other"},
		"bearer":         {map[string]string{"Authorization": "Bearer " + raw}, http.StatusOK, "svc items:export,other"},
		"none":           {nil, http.StatusUnauthorized, "X-API-Key or Authorization: Bearer"},
		"wrong":          {map[string]string{"X-API-Key": "kit_x_y"}, http.StatusUnauthorized, "not a kit api key"},
		"x-api-key wins": {map[string]string{"X-API-Key": "nope", "Authorization": "Bearer " + raw}, http.StatusUnauthorized, "not a kit api key"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			for k, v := range c.hdr {
				req.Header.Set(k, v)
			}
			h.ServeHTTP(rec, req)
			assert.Equal(t, c.status, rec.Code)
			assert.Contains(t, rec.Body.String(), c.body)
		})
	}
}

// failingStore is a kv store whose reads fail.
type failingStore struct{ *memory.Store }

func (failingStore) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, errors.New("disk on fire")
}

// TestAPIKeyStoreFailureIsNotAVerdict pins that a store that cannot be
// read answers ErrKeySetUnavailable, not a verdict on the key.
func TestAPIKeyStoreFailureIsNotAVerdict(t *testing.T) {
	mem := memory.New()
	raw, _, err := authn.NewAPIKeys(mem, nil).Create(t.Context(), authn.NewAPIKey{Principal: "p"})
	require.NoError(t, err)
	_, err = authn.NewAPIKeys(failingStore{mem}, nil).Verify(t.Context(), raw)
	require.ErrorIs(t, err, authn.ErrKeySetUnavailable)
}
