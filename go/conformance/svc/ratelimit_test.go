package svc

import (
	"context"
	"testing"
	"time"
)

func TestMemoryRateLimiter_BurstAndDeny(t *testing.T) {
	quota := RateQuota{Burst: 3, PerMinute: 1000, PerDay: 100000}
	lim := NewMemoryRateLimiter(func(string) RateQuota { return quota })
	frozen := time.Date(2026, 5, 11, 0, 0, 0, 0, time.UTC)
	lim.now = func() time.Time { return frozen }

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		ok, _, _, err := lim.Allow(ctx, "claim", 1)
		if err != nil {
			t.Fatalf("Allow %d: %v", i, err)
		}
		if !ok {
			t.Fatalf("Allow %d: denied within burst", i)
		}
	}
	// 4th hits burst cap.
	ok, retry, _, err := lim.Allow(ctx, "claim", 1)
	if err != nil {
		t.Fatalf("Allow burst+1: %v", err)
	}
	if ok {
		t.Fatalf("expected burst denial")
	}
	if retry <= 0 {
		t.Errorf("expected retry-after > 0, got %v", retry)
	}
}

func TestMemoryRateLimiter_RefillsOverTime(t *testing.T) {
	quota := RateQuota{Burst: 1, PerMinute: 60, PerDay: 1000}
	lim := NewMemoryRateLimiter(func(string) RateQuota { return quota })
	frozen := time.Date(2026, 5, 11, 0, 0, 0, 0, time.UTC)
	lim.now = func() time.Time { return frozen }

	ctx := context.Background()
	// Burn the one burst token.
	if ok, _, _, _ := lim.Allow(ctx, "claim", 1); !ok {
		t.Fatal("first allow should succeed")
	}
	// Immediately retry: denied.
	if ok, _, _, _ := lim.Allow(ctx, "claim", 1); ok {
		t.Fatal("retry should be denied")
	}
	// Advance time by 2 seconds; burst rate is 1/sec so 1+ tokens
	// are restored.
	lim.now = func() time.Time { return frozen.Add(2 * time.Second) }
	if ok, _, _, _ := lim.Allow(ctx, "claim", 1); !ok {
		t.Fatal("after refill should succeed")
	}
}
