package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
)

// BenchmarkVersionedUpdate measures the Update path of
// VersionedDocumentStore against both VersionStore implementations.
// One subtest per backend; each iteration replaces the document data
// and appends a new version.
//
// The SQLite variant uses an on-disk b.TempDir() file (NOT
// :memory:) so the bench reflects realistic IO — fsync, WAL flush,
// page cache — instead of pure memory pressure. The in-memory
// variant exercises the historic baseline.
//
// Spec acceptance (engine-store-versioned-sqlite §7): SQLite p50
// within 2× of the in-memory baseline for typical doc size.
//
// Run: go test -bench=BenchmarkVersionedUpdate -benchmem -count=5 ./engine/store/
func BenchmarkVersionedUpdate(b *testing.B) {
	// payload is ~256 bytes when serialized — close to a realistic
	// "small note" document. Stable shape so allocs/op is comparable
	// across runs.
	payload := func(i int) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(
			`{"id":"bench-doc","title":"benchmark note %d","author":"jadb","tags":["benchmark","versioned","sqlite"],"body":"versioned update path: docstore + version-store commit in one transaction. iteration counter %d keeps the payload non-trivial without ballooning size.","i":%d}`,
			i, i, i,
		))
	}

	b.Run("in-memory", func(b *testing.B) {
		ds := newBenchTestStore(b)
		vd := NewInMemoryVersionedDocumentStore(ds)
		ctx := context.Background()

		// Seed with version 1; benchmark exercises Update only.
		if _, err := vd.Create(ctx, "note", payload(0)); err != nil {
			b.Fatalf("seed: %v", err)
		}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := vd.Update(ctx, "note", "bench-doc", payload(i+1)); err != nil {
				b.Fatalf("update: %v", err)
			}
		}
	})

	b.Run("sqlite", func(b *testing.B) {
		ds := newBenchTestStore(b)
		vs, err := NewSQLiteVersionStore(ds.DB())
		if err != nil {
			b.Fatalf("new sqlite version store: %v", err)
		}
		vd := NewVersionedDocumentStore(ds, vs)
		ctx := context.Background()

		if _, err := vd.Create(ctx, "note", payload(0)); err != nil {
			b.Fatalf("seed: %v", err)
		}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := vd.Update(ctx, "note", "bench-doc", payload(i+1)); err != nil {
				b.Fatalf("update: %v", err)
			}
		}
	})
}

// newBenchTestStore opens an on-disk DocumentStore in b.TempDir().
// Mirrors newTestStore but takes *testing.B; lives here rather than
// in document_test.go to keep the bench self-contained.
func newBenchTestStore(b *testing.B) *DocumentStore {
	b.Helper()
	dbPath := filepath.Join(b.TempDir(), "bench.db")
	s, err := NewDocumentStore(dbPath)
	if err != nil {
		b.Fatalf("open document store: %v", err)
	}
	b.Cleanup(func() { _ = s.Close() })
	return s
}
