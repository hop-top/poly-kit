package memory_test

import (
	"testing"

	"hop.top/kit/go/storage/kv"
	"hop.top/kit/go/storage/kv/internal/ttlcorpus"
	"hop.top/kit/go/storage/kv/memory"
)

// TestPutIfAbsentWithTTLCorpus replays contracts/kv-v1/ttl.json, the
// sequences the Rust SqliteStore replays too.
func TestPutIfAbsentWithTTLCorpus(t *testing.T) {
	ttlcorpus.Run(t, func(t *testing.T) kv.TTLInserter {
		t.Helper()
		return memory.New()
	})
}
