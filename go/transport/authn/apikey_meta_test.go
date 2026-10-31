package authn_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/storage/kv/memory"
	"hop.top/kit/go/transport/authn"
)

func TestAPIKeyGet(t *testing.T) {
	keys := authn.NewAPIKeys(memory.New(), nil)
	_, rec, err := keys.Create(t.Context(), authn.NewAPIKey{Principal: "p"})
	require.NoError(t, err)

	got, err := keys.Get(t.Context(), rec.ID)
	require.NoError(t, err)
	assert.Equal(t, rec.ID, got.ID)
	assert.Equal(t, rec.Hash, got.Hash)

	_, err = keys.Get(t.Context(), "0000000000000000")
	require.ErrorIs(t, err, authn.ErrAPIKeyNotFound)
}

func TestAPIKeyMetadata(t *testing.T) {
	store := memory.New()
	keys := authn.NewAPIKeys(store, nil)
	labels := map[string]string{"env": "prod"}
	_, rec, err := keys.Create(t.Context(), authn.NewAPIKey{Principal: "p", Name: "deploy", Labels: labels})
	require.NoError(t, err)
	assert.Equal(t, "deploy", rec.Name)
	labels["env"] = "changed"
	assert.Equal(t, map[string]string{"env": "prod"}, rec.Labels, "labels are copied, not aliased")

	got, err := keys.Get(t.Context(), rec.ID)
	require.NoError(t, err)
	assert.Equal(t, "deploy", got.Name)
	assert.Equal(t, map[string]string{"env": "prod"}, got.Labels)

	// Revoke rewrites the record; the metadata must ride along.
	rv, err := keys.Revoke(t.Context(), rec.ID)
	require.NoError(t, err)
	assert.Equal(t, "deploy", rv.Name)

	// Without metadata the stored JSON carries no empty fields.
	_, plain, err := keys.Create(t.Context(), authn.NewAPIKey{Principal: "q"})
	require.NoError(t, err)
	data, _, err := store.Get(t.Context(), "apikey/"+plain.ID)
	require.NoError(t, err)
	for _, k := range []string{"name", "labels", "last_used_at"} {
		assert.NotContains(t, string(data), `"`+k+`"`)
	}
}

func TestAPIKeyBrandedPrefixAndDomain(t *testing.T) {
	store := memory.New()
	branded, err := authn.NewAPIKeys(store, nil).WithPrefix("acme")
	require.NoError(t, err)
	branded = branded.WithHashDomain("acme-apikey-v1")

	raw, rec, err := branded.Create(t.Context(), authn.NewAPIKey{Principal: "p"})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(raw, "acme_"), raw)
	_, err = branded.Verify(t.Context(), raw)
	require.NoError(t, err)

	// The default prefix refuses it; so does the right prefix under the
	// wrong hash domain.
	_, err = authn.NewAPIKeys(store, nil).Verify(t.Context(), raw)
	require.ErrorIs(t, err, authn.ErrInvalidToken)
	wrongDomain, err := authn.NewAPIKeys(store, nil).WithPrefix("acme")
	require.NoError(t, err)
	_, err = wrongDomain.Verify(t.Context(), raw)
	require.ErrorIs(t, err, authn.ErrInvalidToken)
	assert.Contains(t, err.Error(), "unknown api key")
	assert.NotEmpty(t, rec.Hash)

	for _, bad := range []string{"", "a_b", "a b", "kit "} {
		_, err := authn.NewAPIKeys(store, nil).WithPrefix(bad)
		assert.Error(t, err, "prefix %q", bad)
	}
}

func TestAPIKeyLastUsed(t *testing.T) {
	store := memory.New()
	clk := &fakeClock{t: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	off := authn.NewAPIKeys(store, clk.now)
	raw, rec, err := off.Create(t.Context(), authn.NewAPIKey{Principal: "p"})
	require.NoError(t, err)

	clk.advance(time.Hour)
	_, err = off.Verify(t.Context(), raw)
	require.NoError(t, err)
	got, err := off.Get(t.Context(), rec.ID)
	require.NoError(t, err)
	assert.True(t, got.LastUsedAt.IsZero(), "off by default")

	on := off.WithLastUsed(true)
	seen, err := on.Verify(t.Context(), raw)
	require.NoError(t, err)
	assert.True(t, seen.LastUsedAt.Equal(clk.t))
	got, err = on.Get(t.Context(), rec.ID)
	require.NoError(t, err)
	assert.True(t, got.LastUsedAt.Equal(clk.t))

	// A revoke that landed is not overwritten by a later verify.
	_, err = on.Revoke(t.Context(), rec.ID)
	require.NoError(t, err)
	_, err = on.Verify(t.Context(), raw)
	require.ErrorIs(t, err, authn.ErrInvalidToken)
}

// afterGet runs once after the first Get, so a test can land a write
// between a verify's read and its last-used write.
type afterGet struct {
	*memory.Store
	hook func()
}

func (s *afterGet) Get(ctx context.Context, key string) ([]byte, bool, error) {
	data, ok, err := s.Store.Get(ctx, key)
	if h := s.hook; h != nil {
		s.hook = nil
		h()
	}
	return data, ok, err
}

// A revoke landing between a verify's read and its last-used write
// must survive: the stamp never rewrites the authorization record.
func TestAPIKeyLastUsedCannotUndoAConcurrentRevoke(t *testing.T) {
	inner := memory.New()
	store := &afterGet{Store: inner}
	keys := authn.NewAPIKeys(store, nil).WithLastUsed(true)
	raw, rec, err := keys.Create(t.Context(), authn.NewAPIKey{Principal: "p"})
	require.NoError(t, err)
	store.hook = func() {
		_, err := authn.NewAPIKeys(inner, nil).Revoke(t.Context(), rec.ID)
		require.NoError(t, err)
	}
	_, err = keys.Verify(t.Context(), raw)
	require.NoError(t, err, "the verdict was reached before the revoke landed")
	_, err = keys.Verify(t.Context(), raw)
	require.ErrorIs(t, err, authn.ErrInvalidToken, "the revoke survives the stamp")
	got, err := keys.Get(t.Context(), rec.ID)
	require.NoError(t, err)
	assert.False(t, got.RevokedAt.IsZero())
	assert.False(t, got.LastUsedAt.IsZero(), "the stamp is still readable")
}

type recordsFile struct {
	HashDomains []struct {
		Name, Key, Prefix, Domain, Hash string
	} `json:"hash_domains"`
	Records []struct {
		Name        string
		Record      map[string]any
		RevokeAt    string         `json:"revoke_at"`
		AfterRevoke map[string]any `json:"after_revoke"`
	}
	LastUsed []struct {
		Name       string
		Key        string
		Prefix     string
		Domain     string
		Record     map[string]any
		VerifyAt   string `json:"verify_at"`
		StoredLast string `json:"stored_last_used_at"`
	} `json:"last_used"`
}

func loadRecords(t *testing.T) recordsFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash("../../../contracts/authn-v1/apikey-records.json"))
	require.NoError(t, err)
	var f recordsFile
	require.NoError(t, json.Unmarshal(raw, &f))
	require.NotEmpty(t, f.HashDomains)
	require.NotEmpty(t, f.Records)
	require.NotEmpty(t, f.LastUsed)
	return f
}

func seed(t *testing.T, store *memory.Store, rec map[string]any) {
	t.Helper()
	data, err := json.Marshal(rec)
	require.NoError(t, err)
	require.NoError(t, store.Put(t.Context(), "apikey/"+rec["id"].(string), data))
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339Nano, s)
	require.NoError(t, err)
	return v
}

func asValue(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	return m
}

// TestAuthnContractVectorsAPIKeyRecords replays contracts/authn-v1/apikey-records.json,
// which the Rust port replays too.
func TestAuthnContractVectorsAPIKeyRecords(t *testing.T) {
	f := loadRecords(t)

	for _, v := range f.HashDomains {
		t.Run("domain/"+v.Name, func(t *testing.T) {
			parts := strings.Split(v.Key, "_")
			store := memory.New()
			seed(t, store, map[string]any{"id": parts[1], "principal": "p", "created_at": "2026-09-28T12:00:00Z", "hash": v.Hash})
			keys, err := authn.NewAPIKeys(store, nil).WithPrefix(v.Prefix)
			require.NoError(t, err)
			_, err = keys.WithHashDomain(v.Domain).Verify(t.Context(), v.Key)
			require.NoError(t, err)
			_, err = keys.WithHashDomain("other-domain").Verify(t.Context(), v.Key)
			require.ErrorIs(t, err, authn.ErrInvalidToken)
		})
	}

	for _, v := range f.Records {
		t.Run("record/"+v.Name, func(t *testing.T) {
			store := memory.New()
			seed(t, store, v.Record)
			at := mustTime(t, v.RevokeAt)
			keys := authn.NewAPIKeys(store, func() time.Time { return at })
			_, err := keys.Revoke(t.Context(), v.Record["id"].(string))
			require.NoError(t, err)
			data, ok, err := store.Get(t.Context(), "apikey/"+v.Record["id"].(string))
			require.NoError(t, err)
			require.True(t, ok)
			assert.Equal(t, v.AfterRevoke, asValue(t, data))
		})
	}

	for _, v := range f.LastUsed {
		t.Run("last_used/"+v.Name, func(t *testing.T) {
			store := memory.New()
			seed(t, store, v.Record)
			at := mustTime(t, v.VerifyAt)
			keys, err := authn.NewAPIKeys(store, func() time.Time { return at }).WithPrefix(v.Prefix)
			require.NoError(t, err)
			keys = keys.WithHashDomain(v.Domain)
			_, err = keys.Verify(t.Context(), v.Key)
			require.NoError(t, err)
			id := v.Record["id"].(string)
			data, _, _ := store.Get(t.Context(), "apikey/"+id)
			assert.NotContains(t, asValue(t, data), "last_used_at", "not rewritten without tracking")
			_, err = keys.WithLastUsed(true).Verify(t.Context(), v.Key)
			require.NoError(t, err)
			data, _, _ = store.Get(t.Context(), "apikey/"+id)
			assert.Equal(t, v.Record, asValue(t, data), "the record is not rewritten")
			got, err := keys.Get(t.Context(), id)
			require.NoError(t, err)
			assert.True(t, mustTime(t, v.StoredLast).Equal(got.LastUsedAt))
		})
	}
}
