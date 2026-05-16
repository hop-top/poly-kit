package cli

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestNoAuth_InspectReturnsAnonymous(t *testing.T) {
	n := NoAuth{}
	cred, err := n.Inspect(context.Background())
	if err != nil {
		t.Fatalf("Inspect error: %v", err)
	}
	if cred.Source != "none" {
		t.Errorf("Source = %q, want none", cred.Source)
	}
	if cred.Identity != "anonymous" {
		t.Errorf("Identity = %q, want anonymous", cred.Identity)
	}
}

func TestNoAuth_RefreshErrors(t *testing.T) {
	n := NoAuth{}
	if err := n.Refresh(context.Background()); err == nil {
		t.Error("Refresh should return error for NoAuth")
	}
}

func TestCredential_JSON(t *testing.T) {
	exp := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	c := Credential{
		Source:    "keyring",
		Identity:  "alice@example.com",
		Scopes:    []string{"read", "write"},
		ExpiresAt: &exp,
		Renewable: true,
	}

	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got Credential
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Source != "keyring" {
		t.Errorf("Source = %q", got.Source)
	}
	if got.Identity != "alice@example.com" {
		t.Errorf("Identity = %q", got.Identity)
	}
	if len(got.Scopes) != 2 {
		t.Errorf("Scopes len = %d", len(got.Scopes))
	}
	if got.ExpiresAt == nil || got.ExpiresAt.Year() != 2026 {
		t.Errorf("ExpiresAt = %v", got.ExpiresAt)
	}
	if !got.Renewable {
		t.Error("Renewable should be true")
	}
}
