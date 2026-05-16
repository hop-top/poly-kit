package env_test

import (
	"context"
	"errors"
	"testing"

	"hop.top/kit/go/storage/secret"
	"hop.top/kit/go/storage/secret/env"
)

func TestGet(t *testing.T) {
	t.Setenv("APP_DB_PASSWORD", "hunter2")
	s := env.New("APP_")

	got, err := s.Get(context.Background(), "db_password")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got.Value) != "hunter2" {
		t.Fatalf("got %q, want %q", got.Value, "hunter2")
	}
	if got.Key != "db_password" {
		t.Fatalf("key = %q, want %q", got.Key, "db_password")
	}
}

func TestGet_NotFound(t *testing.T) {
	s := env.New("APP_")

	_, err := s.Get(context.Background(), "missing")
	if !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestExists(t *testing.T) {
	t.Setenv("TST_KEY", "val")
	s := env.New("TST_")

	ok, err := s.Exists(context.Background(), "key")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected true")
	}

	ok, err = s.Exists(context.Background(), "nope")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected false")
	}
}

func TestList(t *testing.T) {
	t.Setenv("PFX_DB_HOST", "localhost")
	t.Setenv("PFX_DB_PORT", "5432")
	t.Setenv("PFX_CACHE_TTL", "60")
	t.Setenv("OTHER_KEY", "x")

	s := env.New("PFX_")

	keys, err := s.List(context.Background(), "db")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("got %d keys, want 2: %v", len(keys), keys)
	}
	for _, k := range keys {
		if !hasPrefix(k, "db_") {
			t.Errorf("key %q doesn't have prefix db_", k)
		}
	}
}

func TestListGetRoundtrip(t *testing.T) {
	t.Setenv("RT_API_KEY", "secret123")
	t.Setenv("RT_DB_HOST", "localhost")

	s := env.New("RT_")
	ctx := context.Background()

	keys, err := s.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		got, err := s.Get(ctx, k)
		if err != nil {
			t.Fatalf("Get(%q) after List: %v", k, err)
		}
		if got.Key != k {
			t.Fatalf("Get(%q).Key = %q, want %q", k, got.Key, k)
		}
	}
}

func hasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}
