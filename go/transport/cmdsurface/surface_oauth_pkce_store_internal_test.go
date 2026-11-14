package cmdsurface

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInMemoryPKCEStore_PrunesExpiredOnIssue(t *testing.T) {
	s := NewInMemoryPKCEStateStore()
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := s.Issue(ctx, "p", "v", "", time.Millisecond); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := s.Issue(ctx, "p", "v", "", time.Minute); err != nil {
		t.Fatal(err)
	}
	if n := len(s.items); n != 1 {
		t.Fatalf("items = %d, want 1 (expired pruned)", n)
	}
}

func TestInMemoryPKCEStore_CapEnforced(t *testing.T) {
	s := NewInMemoryPKCEStateStore()
	s.cap = 3
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := s.Issue(ctx, "p", "v", "", time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Issue(ctx, "p", "v", "", time.Minute); !errors.Is(err, ErrStateStoreFull) {
		t.Fatalf("err = %v, want ErrStateStoreFull", err)
	}
}

func TestInMemoryPKCEStore_BoundMismatchKeepsState(t *testing.T) {
	s := NewInMemoryPKCEStateStore()
	ctx := context.Background()
	st, _ := s.Issue(ctx, "p", "v", "b1", time.Minute)
	if _, err := s.Consume(ctx, "p", st, "b2"); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("err = %v", err)
	}
	if v, err := s.Consume(ctx, "p", st, "b1"); err != nil || v != "v" {
		t.Fatalf("genuine: %q %v", v, err)
	}
}
