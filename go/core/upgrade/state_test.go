package upgrade

import (
	"testing"
	"time"
)

func TestSnooze(t *testing.T) {
	dir := t.TempDir()

	ok, err := isSnoozed(dir, "test")
	if err != nil || ok {
		t.Fatalf("fresh: snoozed=%v err=%v", ok, err)
	}

	if err := writeSnooze(dir, "test", time.Hour); err != nil {
		t.Fatal(err)
	}

	ok, err = isSnoozed(dir, "test")
	if err != nil || !ok {
		t.Fatalf("after snooze: snoozed=%v err=%v", ok, err)
	}
}

func TestSnoozeExpired(t *testing.T) {
	dir := t.TempDir()
	if err := writeSnooze(dir, "test", -time.Second); err != nil {
		t.Fatal(err)
	}
	ok, err := isSnoozed(dir, "test")
	if err != nil || ok {
		t.Fatalf("expired snooze should not be active: snoozed=%v err=%v", ok, err)
	}
}

func TestCacheRoundtrip(t *testing.T) {
	dir := t.TempDir()
	r := &Result{
		Current:     "1.0.0",
		Latest:      "1.1.0",
		URL:         "http://example.com/v1.1.0",
		Notes:       "Bug fixes",
		CheckedAt:   time.Now().Truncate(time.Second),
		UpdateAvail: true,
	}
	if err := saveCachedResult(dir, "tool", r); err != nil {
		t.Fatal(err)
	}
	got, err := loadCachedResult(dir, "tool")
	if err != nil {
		t.Fatal(err)
	}
	if got.Latest != r.Latest || got.UpdateAvail != r.UpdateAvail {
		t.Errorf("cache mismatch: %+v", got)
	}
}

func TestCacheMissing(t *testing.T) {
	dir := t.TempDir()
	got, err := loadCachedResult(dir, "missing")
	if err != nil || got != nil {
		t.Errorf("expected nil,nil; got %v,%v", got, err)
	}
}
