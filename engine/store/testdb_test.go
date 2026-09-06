package store

import (
	"testing"

	"hop.top/kit/go/storage/sqldb"
)

// newTestDocumentStore opens a throwaway on-disk DocumentStore with
// SQLite durability turned off.
//
// Why the tests need their own opener: the four cross-backend property
// tests are fsync-bound, not CPU-bound. Each iteration builds a fresh
// store under t.TempDir() and commits a few dozen small transactions,
// so at the full 1000-iteration count almost all wall-clock is SQLite
// flushing a scratch file nothing will ever read again. Measured in a
// 4-CPU Linux container, the package ran ~226s parallel on overlayfs
// against ~43s on tmpfs — the gap is durability, and dropping it is
// what lets pull requests run the full count instead of a trimmed one.
//
// synchronous=OFF is safe here precisely because the database is
// disposable: it costs crash durability, and a t.TempDir() database
// does not outlive the test. Journal mode stays WAL — the same mode
// production runs — so cross-connection and concurrency semantics the
// property tests assert on are the ones real stores have.
// [NewDocumentStore] keeps the durable defaults; nothing outside this
// package can reach this path.
func newTestDocumentStore(t *testing.T, dbPath string) *DocumentStore {
	t.Helper()
	ds, err := newDocumentStore(sqldb.Options{Path: dbPath, Synchronous: "OFF"})
	if err != nil {
		t.Fatalf("open test document store at %s: %v", dbPath, err)
	}
	t.Cleanup(func() { _ = ds.Close() })
	return ds
}
