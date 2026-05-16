package svc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"hop.top/kit/go/storage/sqldb"
	"hop.top/kit/go/transport/api"
)

// ErrClaimNotFound is returned when no claim resolves the given token
// (either by plaintext or by ID).
var ErrClaimNotFound = errors.New("claim: not found")

// ClaimStore is the persistence seam for bearer-token claims. v1
// driver is the SQLite-backed SQLClaimStore.
type ClaimStore interface {
	// Lookup resolves a token plaintext to its claim. Returns
	// ErrClaimNotFound on miss.
	Lookup(ctx context.Context, token string) (*Claim, error)
	// LookupByID returns the claim by TokenID. Returns ErrClaimNotFound on miss.
	LookupByID(ctx context.Context, tokenID string) (*Claim, error)
	// List returns every claim (including revoked) for operator audit.
	List(ctx context.Context) ([]Claim, error)
	// Mint creates a new claim, returning the claim plus the token plaintext
	// that the operator must display ONCE.
	Mint(ctx context.Context, in MintInput) (Claim, string, error)
	// Revoke marks the claim with TokenID as revoked. Idempotent.
	Revoke(ctx context.Context, tokenID string) error
}

// MintInput is the operator-supplied parameters for a new claim.
type MintInput struct {
	Tenant             string
	Scopes             []string
	TierMax            int
	RateQuota          RateQuota
	JudgeTokenCapDaily int
	JudgeCacheTTL      time.Duration
	ExpiresAt          time.Time
	Description        string
}

// SQLClaimStore is the SQLite-backed driver. Schema is created on first
// open via sqldb.Migrate.
type SQLClaimStore struct {
	db *sql.DB
}

// OpenSQLClaimStore opens a SQLite file at path (created if missing)
// and applies the claim schema. Use ":memory:" for tests.
func OpenSQLClaimStore(path string) (*SQLClaimStore, error) {
	db, err := sqldb.Open(sqldb.Options{Path: path})
	if err != nil {
		return nil, fmt.Errorf("sqlclaimstore: open: %w", err)
	}
	if err := sqldb.Migrate(db, "kit_conf_svc_claims_migrations", migrations); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlclaimstore: migrate: %w", err)
	}
	return &SQLClaimStore{db: db}, nil
}

// Close closes the underlying DB.
func (s *SQLClaimStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

var migrations = map[int]string{
	1: `CREATE TABLE claims (
		token_id              TEXT PRIMARY KEY,
		token_sha256          BLOB NOT NULL,
		tenant                TEXT NOT NULL DEFAULT '',
		scopes_json           TEXT NOT NULL,
		tier_max              INTEGER NOT NULL DEFAULT 1,
		rate_quota_json       TEXT NOT NULL,
		judge_cap_daily       INTEGER NOT NULL DEFAULT 0,
		judge_cache_ttl_ns    INTEGER NOT NULL DEFAULT 0,
		created_at            TEXT NOT NULL,
		expires_at            TEXT NOT NULL DEFAULT '',
		revoked               INTEGER NOT NULL DEFAULT 0,
		description           TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX claims_by_sha256 ON claims(token_sha256);`,
}

// hashToken returns the sha256 of the token plaintext.
func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// generateToken mints a fresh opaque token with the kit-conf- prefix.
func generateToken() (string, string, error) {
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", "", err
	}
	tok := TokenPrefix + base64.RawURLEncoding.EncodeToString(entropy[:])
	// TokenID is a 16-hex-char prefix-stable fingerprint usable in
	// operator listings without leaking the token.
	idBytes := sha256.Sum256([]byte(tok))
	id := hex.EncodeToString(idBytes[:8])
	return tok, id, nil
}

// Mint implements ClaimStore.
func (s *SQLClaimStore) Mint(ctx context.Context, in MintInput) (Claim, string, error) {
	if in.TierMax <= 0 {
		in.TierMax = 1
	}
	if in.RateQuota == (RateQuota{}) {
		in.RateQuota = DefaultQuota
	}
	if len(in.Scopes) == 0 {
		return Claim{}, "", fmt.Errorf("mint: scopes required")
	}
	for _, sc := range in.Scopes {
		if !validScopeStr(sc) {
			return Claim{}, "", fmt.Errorf("mint: invalid scope %q", sc)
		}
	}
	token, id, err := generateToken()
	if err != nil {
		return Claim{}, "", fmt.Errorf("mint: rand: %w", err)
	}
	hash := hashToken(token)
	scopesJSON, _ := json.Marshal(in.Scopes)
	quotaJSON, _ := json.Marshal(in.RateQuota)
	now := time.Now().UTC()
	var expiresStr string
	if !in.ExpiresAt.IsZero() {
		expiresStr = in.ExpiresAt.UTC().Format(time.RFC3339)
	}

	_, err = s.db.ExecContext(ctx, `INSERT INTO claims
		(token_id, token_sha256, tenant, scopes_json, tier_max,
		 rate_quota_json, judge_cap_daily, judge_cache_ttl_ns,
		 created_at, expires_at, revoked, description)
		VALUES (?,?,?,?,?,?,?,?,?,?,0,?)`,
		id, hash, in.Tenant, string(scopesJSON), in.TierMax,
		string(quotaJSON), in.JudgeTokenCapDaily, int64(in.JudgeCacheTTL),
		now.Format(time.RFC3339), expiresStr, in.Description)
	if err != nil {
		return Claim{}, "", fmt.Errorf("mint: insert: %w", err)
	}
	cl := Claim{
		TokenID:            id,
		TokenSHA256:        hash,
		Tenant:             in.Tenant,
		Scopes:             in.Scopes,
		TierMax:            in.TierMax,
		RateQuota:          in.RateQuota,
		JudgeTokenCapDaily: in.JudgeTokenCapDaily,
		JudgeCacheTTL:      in.JudgeCacheTTL,
		CreatedAt:          now,
		ExpiresAt:          in.ExpiresAt,
		Description:        in.Description,
	}
	return cl, token, nil
}

// Lookup implements ClaimStore.
func (s *SQLClaimStore) Lookup(ctx context.Context, token string) (*Claim, error) {
	if !strings.HasPrefix(token, TokenPrefix) {
		return nil, ErrClaimNotFound
	}
	hash := hashToken(token)
	row := s.db.QueryRowContext(ctx, `SELECT
		token_id, tenant, scopes_json, tier_max, rate_quota_json,
		judge_cap_daily, judge_cache_ttl_ns, created_at, expires_at,
		revoked, description
		FROM claims WHERE token_sha256 = ?`, hash)
	return scanClaim(row, hash)
}

// LookupByID implements ClaimStore.
func (s *SQLClaimStore) LookupByID(ctx context.Context, tokenID string) (*Claim, error) {
	row := s.db.QueryRowContext(ctx, `SELECT
		token_id, tenant, scopes_json, tier_max, rate_quota_json,
		judge_cap_daily, judge_cache_ttl_ns, created_at, expires_at,
		revoked, description, token_sha256
		FROM claims WHERE token_id = ?`, tokenID)
	return scanClaimWithHash(row)
}

// List implements ClaimStore. Returns all claims sorted by created_at desc.
func (s *SQLClaimStore) List(ctx context.Context) ([]Claim, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		token_id, tenant, scopes_json, tier_max, rate_quota_json,
		judge_cap_daily, judge_cache_ttl_ns, created_at, expires_at,
		revoked, description, token_sha256
		FROM claims ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Claim
	for rows.Next() {
		c, err := scanClaimWithHashRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// Revoke implements ClaimStore.
func (s *SQLClaimStore) Revoke(ctx context.Context, tokenID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE claims SET revoked = 1 WHERE token_id = ?`, tokenID)
	return err
}

// scanClaim reads a row with the canonical column order minus
// token_sha256 (looked up via the hash parameter).
func scanClaim(row *sql.Row, hash []byte) (*Claim, error) {
	var c Claim
	var scopesJSON, quotaJSON, createdAt, expiresAt string
	var ttlNS int64
	var revoked int
	err := row.Scan(&c.TokenID, &c.Tenant, &scopesJSON, &c.TierMax, &quotaJSON,
		&c.JudgeTokenCapDaily, &ttlNS, &createdAt, &expiresAt, &revoked, &c.Description)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrClaimNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(scopesJSON), &c.Scopes); err != nil {
		return nil, fmt.Errorf("scan scopes: %w", err)
	}
	if err := json.Unmarshal([]byte(quotaJSON), &c.RateQuota); err != nil {
		return nil, fmt.Errorf("scan quota: %w", err)
	}
	c.JudgeCacheTTL = time.Duration(ttlNS)
	c.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	if expiresAt != "" {
		c.ExpiresAt, _ = time.Parse(time.RFC3339, expiresAt)
	}
	c.Revoked = revoked != 0
	c.TokenSHA256 = hash
	return &c, nil
}

// scanClaimWithHash mirrors scanClaim but reads token_sha256 from the
// row instead of taking it as input.
func scanClaimWithHash(row *sql.Row) (*Claim, error) {
	var c Claim
	var scopesJSON, quotaJSON, createdAt, expiresAt string
	var ttlNS int64
	var revoked int
	var hash []byte
	err := row.Scan(&c.TokenID, &c.Tenant, &scopesJSON, &c.TierMax, &quotaJSON,
		&c.JudgeTokenCapDaily, &ttlNS, &createdAt, &expiresAt, &revoked, &c.Description, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrClaimNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(scopesJSON), &c.Scopes)
	_ = json.Unmarshal([]byte(quotaJSON), &c.RateQuota)
	c.JudgeCacheTTL = time.Duration(ttlNS)
	c.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	if expiresAt != "" {
		c.ExpiresAt, _ = time.Parse(time.RFC3339, expiresAt)
	}
	c.Revoked = revoked != 0
	c.TokenSHA256 = hash
	return &c, nil
}

// scanClaimWithHashRow is the *sql.Rows variant of scanClaimWithHash.
func scanClaimWithHashRow(rows *sql.Rows) (*Claim, error) {
	var c Claim
	var scopesJSON, quotaJSON, createdAt, expiresAt string
	var ttlNS int64
	var revoked int
	var hash []byte
	err := rows.Scan(&c.TokenID, &c.Tenant, &scopesJSON, &c.TierMax, &quotaJSON,
		&c.JudgeTokenCapDaily, &ttlNS, &createdAt, &expiresAt, &revoked, &c.Description, &hash)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(scopesJSON), &c.Scopes)
	_ = json.Unmarshal([]byte(quotaJSON), &c.RateQuota)
	c.JudgeCacheTTL = time.Duration(ttlNS)
	c.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	if expiresAt != "" {
		c.ExpiresAt, _ = time.Parse(time.RFC3339, expiresAt)
	}
	c.Revoked = revoked != 0
	c.TokenSHA256 = hash
	return &c, nil
}

// validScopeStr checks the basic scope grammar: <verb>:<ns>|*
func validScopeStr(s string) bool {
	if s == "admin" || s == "list:all" {
		return true
	}
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return false
	}
	verb := parts[0]
	target := parts[1]
	switch verb {
	case "grade", "meta":
	default:
		return false
	}
	if target == "*" {
		return true
	}
	return ValidNamespace(target)
}

// Auth returns a middleware that authenticates each request via the
// claim store and stashes the resolved *Claim on context.
func Auth(store ClaimStore) api.Middleware {
	return api.Auth(func(r *http.Request) (any, error) {
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			return nil, fmt.Errorf("%s: missing bearer", CodeMissingBearer)
		}
		token := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		if token == "" {
			return nil, fmt.Errorf("%s: empty bearer", CodeMissingBearer)
		}
		claim, err := store.Lookup(r.Context(), token)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid token", CodeInvalidBearer)
		}
		if claim.Revoked {
			return nil, fmt.Errorf("%s: token revoked", CodeInvalidBearer)
		}
		if claim.IsExpired(time.Now()) {
			return nil, fmt.Errorf("%s: token expired", CodeInvalidBearer)
		}
		return claim, nil
	})
}

// ClaimFromContext returns the resolved *Claim or nil.
func ClaimFromContext(ctx context.Context) *Claim {
	v := api.ClaimsFromContext(ctx)
	if v == nil {
		return nil
	}
	c, _ := v.(*Claim)
	return c
}
