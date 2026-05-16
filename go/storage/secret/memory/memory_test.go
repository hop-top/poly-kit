package memory_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"hop.top/kit/go/storage/secret"
	"hop.top/kit/go/storage/secret/memory"
)

func TestRoundtrip(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

	if err := s.Set(ctx, "db/pass", []byte("hunter2")); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, "db/pass")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Value) != "hunter2" {
		t.Fatalf("got %q, want %q", got.Value, "hunter2")
	}
	if got.Key != "db/pass" {
		t.Fatalf("got key %q, want %q", got.Key, "db/pass")
	}
}

func TestExists(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

	ok, err := s.Exists(ctx, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected false for missing key")
	}

	_ = s.Set(ctx, "present", []byte("v"))
	ok, err = s.Exists(ctx, "present")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected true for present key")
	}
}

func TestList(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

	_ = s.Set(ctx, "app/key1", []byte("a"))
	_ = s.Set(ctx, "app/key2", []byte("b"))
	_ = s.Set(ctx, "other/key", []byte("c"))

	keys, err := s.List(ctx, "app/")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("got %d keys, want 2", len(keys))
	}
	if keys[0] != "app/key1" || keys[1] != "app/key2" {
		t.Fatalf("unexpected keys: %v", keys)
	}
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

	_ = s.Set(ctx, "k", []byte("v"))
	if err := s.Delete(ctx, "k"); err != nil {
		t.Fatal(err)
	}

	_, err := s.Get(ctx, "k")
	if !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestDeleteMissing(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

	err := s.Delete(ctx, "nope")
	if !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestGetMissing(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

	_, err := s.Get(ctx, "nope")
	if !errors.Is(err, secret.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := "key"
			_ = s.Set(ctx, key, []byte{byte(n)})
			_, _ = s.Get(ctx, key)
			_, _ = s.Exists(ctx, key)
			_, _ = s.List(ctx, "")
		}(i)
	}
	wg.Wait()
}
