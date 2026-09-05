package badger_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"hop.top/kit/go/core/netpolicy"
	"hop.top/kit/go/storage/kv/badger"
)

// BadgerDB is a local directory. --offline means "do not talk to the
// network", and a directory is not the network, so an offline context must
// not restrict the open at all.
func TestNewContext_OfflineOpensLocalDir(t *testing.T) {
	ctx := netpolicy.WithOffline(t.Context(), true)
	dir := filepath.Join(t.TempDir(), "badger")

	store, err := badger.NewContext(ctx, dir)
	if err != nil {
		t.Fatalf("offline open of a local dir failed: %v", err)
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

// A canceled caller must not have a database created on its behalf.
func TestNewContext_CancelledContextRefusesOpen(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	store, err := badger.NewContext(ctx, filepath.Join(t.TempDir(), "badger"))
	if err == nil {
		store.Close()
		t.Fatal("canceled context produced a usable Store")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}
