package svc

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *SQLClaimStore {
	t.Helper()
	store, err := OpenSQLClaimStore(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLClaimStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestSQLClaimStore_MintLookupRevoke(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	claim, plaintext, err := store.Mint(ctx, MintInput{
		Tenant:  "acme",
		Scopes:  []string{"grade:acme-corp"},
		TierMax: 2,
	})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if plaintext == "" {
		t.Fatal("Mint: empty token plaintext")
	}
	if claim.TokenID == "" {
		t.Fatal("Mint: empty token ID")
	}
	if claim.TierMax != 2 {
		t.Errorf("TierMax: got %d, want 2", claim.TierMax)
	}
	if claim.RateQuota != DefaultQuota {
		t.Errorf("RateQuota: got %+v, want default", claim.RateQuota)
	}

	// Lookup by plaintext.
	got, err := store.Lookup(ctx, plaintext)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got.TokenID != claim.TokenID {
		t.Errorf("Lookup ID: got %q, want %q", got.TokenID, claim.TokenID)
	}

	// Wrong token -> not found.
	if _, err := store.Lookup(ctx, "kit-conf-nonsense"); !errors.Is(err, ErrClaimNotFound) {
		t.Errorf("Lookup miss: want ErrClaimNotFound, got %v", err)
	}

	// Non-prefixed token short-circuits to not-found.
	if _, err := store.Lookup(ctx, "Bearer foo"); !errors.Is(err, ErrClaimNotFound) {
		t.Errorf("Lookup wrong prefix: want ErrClaimNotFound, got %v", err)
	}

	// Revoke + verify.
	if err := store.Revoke(ctx, claim.TokenID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	got, err = store.Lookup(ctx, plaintext)
	if err != nil {
		t.Fatalf("Lookup after revoke: %v", err)
	}
	if !got.Revoked {
		t.Errorf("claim should be marked revoked after Revoke")
	}
}

func TestClaim_HasScope(t *testing.T) {
	c := &Claim{Scopes: []string{"grade:acme", "meta:*", "list:all"}}
	cases := []struct {
		want string
		ok   bool
	}{
		{"grade:acme", true},
		{"grade:other", false},
		{"meta:anything", true},
		{"list:all", true},
		{"admin", false},
	}
	for _, tc := range cases {
		if got := c.HasScope(tc.want); got != tc.ok {
			t.Errorf("HasScope(%q): got %v, want %v", tc.want, got, tc.ok)
		}
	}
}

func TestClaim_IsExpired(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name      string
		expiresAt time.Time
		want      bool
	}{
		{"never expires", time.Time{}, false},
		{"future", now.Add(time.Hour), false},
		{"past", now.Add(-time.Hour), true},
	}
	for _, tc := range cases {
		c := &Claim{ExpiresAt: tc.expiresAt}
		if got := c.IsExpired(now); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestSQLClaimStore_List(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, _, err := store.Mint(ctx, MintInput{Tenant: "t", Scopes: []string{"grade:n"}}); err != nil {
			t.Fatalf("Mint %d: %v", i, err)
		}
	}
	claims, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(claims) != 3 {
		t.Errorf("List size: got %d want 3", len(claims))
	}
}
