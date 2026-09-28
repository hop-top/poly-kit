package authn

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"hop.top/kit/go/storage/kv"
	"hop.top/kit/go/transport/api"
)

// API keys are opaque bearer credentials kit issues and checks itself:
// "kit_<id>_<secret>", where id is 8 random bytes and secret 32, both
// hex. The store keeps, per id, the principal, tenant, scopes, expiry,
// revocation and a hash of the secret — never the secret.
//
// The hash is SHA-256 over a domain-separated encoding of the id and
// the secret, compared in constant time. A slow password KDF (argon2id,
// bcrypt) exists to make guessing a low-entropy secret expensive; a
// 256-bit random secret cannot be guessed at any speed, so a KDF would
// only add latency to every request and a CPU cost an unauthenticated
// caller could impose. The id in the domain separation keeps a hash
// from matching under any other id.
const (
	// APIKeyPrefix begins every API key, so secret scanners and people
	// recognize one.
	APIKeyPrefix = "kit"
	// APIKeyHeader is the header an API key may ride in instead of
	// Authorization: Bearer.
	APIKeyHeader = "X-API-Key"
	// apiKeyStorePrefix namespaces the records in the kv store.
	apiKeyStorePrefix = "apikey/"
	apiKeyHashDomain  = "kit-apikey-v1"
	apiKeyIDBytes     = 8
	apiKeySecretBytes = 32
)

// ErrAPIKeyNotFound is a revoke or lookup of an id the store lacks.
var ErrAPIKeyNotFound = errors.New("authn: no such api key")

// APIKey is one stored key: everything but the secret.
type APIKey struct {
	// ID is the key's public identifier, the middle part of the key.
	ID string `json:"id"`
	// Principal, Tenant and Scopes are the caller the key stands for.
	Principal string   `json:"principal"`
	Tenant    string   `json:"tenant,omitempty"`
	Scopes    []string `json:"scopes,omitempty"`
	// CreatedAt is when the key was issued.
	CreatedAt time.Time `json:"created_at"`
	// ExpiresAt, when set, is when the key stops working.
	ExpiresAt time.Time `json:"expires_at,omitzero"`
	// RevokedAt, when set, is when the key was revoked.
	RevokedAt time.Time `json:"revoked_at,omitzero"`
	// Hash is the hex SHA-256 of the domain-separated id and secret.
	Hash string `json:"hash"`
}

// Claims returns the claims value an [api.AuthFunc] hands the
// transports.
func (k *APIKey) Claims() api.Claims {
	return api.Claims{Subject: k.Principal, Tenant: k.Tenant, Scopes: k.Scopes}
}

// NewAPIKey is what [APIKeys.Create] issues a key for.
type NewAPIKey struct {
	Principal string
	Tenant    string
	Scopes    []string
	// TTL, when positive, sets the key's expiry; zero never expires.
	TTL time.Duration
}

// APIKeys issues, lists, revokes and verifies API keys held in a kv
// store. The store is shared with whatever else opened it: `token key
// create` in one process, the serving process in another.
type APIKeys struct {
	store kv.Store
	now   func() time.Time
}

// NewAPIKeys returns API keys over store. now is the clock; nil means
// time.Now.
func NewAPIKeys(store kv.Store, now func() time.Time) *APIKeys {
	if now == nil {
		now = time.Now
	}
	return &APIKeys{store: store, now: now}
}

// Create issues a key and returns it with its record. The key is
// shown once: only its hash is stored.
func (k *APIKeys) Create(ctx context.Context, spec NewAPIKey) (string, *APIKey, error) {
	if strings.TrimSpace(spec.Principal) == "" {
		return "", nil, errors.New("authn: an api key needs a principal")
	}
	for _, s := range spec.Scopes {
		if s == "" || strings.ContainsAny(s, " \t\r\n") {
			return "", nil, fmt.Errorf("authn: scope %q: a scope is one non-empty word with no spaces", s)
		}
	}
	idb := make([]byte, apiKeyIDBytes)
	secret := make([]byte, apiKeySecretBytes)
	if _, err := rand.Read(idb); err != nil {
		return "", nil, err
	}
	if _, err := rand.Read(secret); err != nil {
		return "", nil, err
	}
	id, sec := hex.EncodeToString(idb), hex.EncodeToString(secret)
	now := k.now().UTC()
	rec := &APIKey{
		ID:        id,
		Principal: spec.Principal,
		Tenant:    spec.Tenant,
		Scopes:    spec.Scopes,
		CreatedAt: now,
		Hash:      apiKeyHash(id, sec),
	}
	if spec.TTL > 0 {
		rec.ExpiresAt = now.Add(spec.TTL)
	}
	if err := k.put(ctx, rec); err != nil {
		return "", nil, err
	}
	return APIKeyPrefix + "_" + id + "_" + sec, rec, nil
}

// List returns every stored key, newest first.
func (k *APIKeys) List(ctx context.Context) ([]APIKey, error) {
	ids, err := k.store.List(ctx, apiKeyStorePrefix)
	if err != nil {
		return nil, err
	}
	out := make([]APIKey, 0, len(ids))
	for _, key := range ids {
		rec, err := k.get(ctx, strings.TrimPrefix(key, apiKeyStorePrefix))
		if err != nil {
			return nil, err
		}
		if rec != nil {
			out = append(out, *rec)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Revoke marks the key with id revoked; it stops working at once. The
// record stays, so the audit trail still names whose key it was.
// Revoking a revoked key is not an error.
func (k *APIKeys) Revoke(ctx context.Context, id string) (*APIKey, error) {
	rec, err := k.get(ctx, id)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, fmt.Errorf("%w: %s", ErrAPIKeyNotFound, id)
	}
	if rec.RevokedAt.IsZero() {
		rec.RevokedAt = k.now().UTC()
		if err := k.put(ctx, rec); err != nil {
			return nil, err
		}
	}
	return rec, nil
}

// Verify checks raw, a full API key, and returns its record. Refusals
// wrap [ErrInvalidToken]: a malformed key, an unknown id, a secret that
// does not match, a revoked or expired key.
func (k *APIKeys) Verify(ctx context.Context, raw string) (*APIKey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrNoToken
	}
	parts := strings.Split(raw, "_")
	if len(parts) != 3 || parts[0] != APIKeyPrefix || !isHex(parts[1], apiKeyIDBytes) ||
		!isHex(parts[2], apiKeySecretBytes) {
		return nil, invalid("not a kit api key")
	}
	id, sec := parts[1], parts[2]
	rec, err := k.get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeySetUnavailable, err)
	}
	want := []byte(apiKeyHash(id, sec))
	if rec == nil {
		// Spend the comparison anyway, so an unknown id and a wrong
		// secret take the same time.
		subtle.ConstantTimeCompare(want, want)
		return nil, invalid("unknown api key")
	}
	if subtle.ConstantTimeCompare(want, []byte(rec.Hash)) != 1 {
		return nil, invalid("unknown api key")
	}
	now := k.now()
	if !rec.RevokedAt.IsZero() {
		return nil, invalid("api key %s was revoked", id)
	}
	if !rec.ExpiresAt.IsZero() && !now.Before(rec.ExpiresAt) {
		return nil, invalid("api key %s expired", id)
	}
	return rec, nil
}

// AuthFunc returns the keys as an [api.AuthFunc]: the key is read from
// the X-API-Key header, else from Authorization: Bearer, and the
// caller is its principal, tenant and scopes.
func (k *APIKeys) AuthFunc() api.AuthFunc {
	return func(r *http.Request) (any, error) {
		raw := strings.TrimSpace(r.Header.Get(APIKeyHeader))
		if raw == "" {
			var err error
			if raw, err = BearerToken(r); err != nil {
				return nil, fmt.Errorf("%w: send it as %s or Authorization: Bearer", err, APIKeyHeader)
			}
		}
		rec, err := k.Verify(r.Context(), raw)
		if err != nil {
			return nil, err
		}
		return rec.Claims(), nil
	}
}

func (k *APIKeys) get(ctx context.Context, id string) (*APIKey, error) {
	data, ok, err := k.store.Get(ctx, apiKeyStorePrefix+id)
	if err != nil || !ok {
		return nil, err
	}
	var rec APIKey
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("authn: api key %s: %w", id, err)
	}
	return &rec, nil
}

func (k *APIKeys) put(ctx context.Context, rec *APIKey) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return k.store.Put(ctx, apiKeyStorePrefix+rec.ID, data)
}

// apiKeyHash is the stored hash of a key's secret.
func apiKeyHash(id, secret string) string {
	h := sha256.New()
	h.Write([]byte(apiKeyHashDomain))
	h.Write([]byte{0})
	h.Write([]byte(id))
	h.Write([]byte{0})
	h.Write([]byte(secret))
	return hex.EncodeToString(h.Sum(nil))
}

// isHex reports whether s is n bytes of lowercase hex.
func isHex(s string, n int) bool {
	if len(s) != 2*n {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
