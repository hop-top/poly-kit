package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"hop.top/kit/go/core/netpolicy"
	"hop.top/kit/go/storage/kv/sqlite"
)

// SQLite is a local file. --offline means "do not talk to the network",
// and a file is not the network, so an offline context must not restrict
// the open at all — a guard that refused here would break every offline
// run that keeps a local cache.
func TestNewContext_OfflineOpensLocalFile(t *testing.T) {
	ctx := netpolicy.WithOffline(t.Context(), true)
	path := filepath.Join(t.TempDir(), "kv.db")

	store, err := sqlite.NewContext(ctx, path)
	if err != nil {
		t.Fatalf("offline open of a local file failed: %v", err)
	}
	defer store.Close()

	if err := store.Put(ctx, "k", []byte("v")); err != nil {
		t.Fatalf("offline put: %v", err)
	}
	got, ok, err := store.Get(ctx, "k")
	if err != nil || !ok || string(got) != "v" {
		t.Fatalf("offline round-trip: got %q ok=%v err=%v", got, ok, err)
	}
}

// The context is threaded into the migration, so a canceled caller does
// not silently get a usable Store.
func TestNewContext_CancelledContextRefusesOpen(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	store, err := sqlite.NewContext(ctx, filepath.Join(t.TempDir(), "kv.db"))
	if err == nil {
		store.Close()
		t.Fatal("canceled context produced a usable Store")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}
