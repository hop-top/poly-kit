package data

import (
	"fmt"
	"testing"
)

func TestPick_EmptyPool(t *testing.T) {
	if got := Pick(nil); got != "" {
		t.Fatalf("Pick(nil) = %q, want empty", got)
	}
}

func TestPick_SingleElement(t *testing.T) {
	got := Pick([]string{"only"})
	if got != "only" {
		t.Fatalf("Pick single = %q, want 'only'", got)
	}
}

func TestPick_ReturnsMember(t *testing.T) {
	pool := []string{"a", "b", "c"}
	got := Pick(pool)
	for _, v := range pool {
		if got == v {
			return
		}
	}
	t.Fatalf("Pick returned %q which is not in pool", got)
}

func TestPick_StableAcrossRapidCalls(t *testing.T) {
	// Contract: same pool → same result within a second window.
	// With 10 elements, rapid calls (sub-microsecond apart) must all agree.
	pool := make([]string, 10)
	for i := range pool {
		pool[i] = fmt.Sprintf("item-%d", i)
	}
	first := Pick(pool)
	for range 1000 {
		if got := Pick(pool); got != first {
			t.Fatalf("Pick unstable: got %q then %q in rapid succession", first, got)
		}
	}
}
