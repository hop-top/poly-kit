package badger_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"hop.top/kit/go/storage/kv"
	"hop.top/kit/go/storage/kv/badger"
	"hop.top/kit/go/storage/kv/internal/ttlcorpus"
)

// TestPutIfAbsentWithTTLCorpus replays contracts/kv-v1/ttl.json, the
// sequences the Rust SqliteStore replays too.
func TestPutIfAbsentWithTTLCorpus(t *testing.T) {
	ttlcorpus.Run(t, func(t *testing.T) kv.TTLInserter {
		t.Helper()
		s, err := badger.New(t.TempDir())
		require.NoError(t, err)
		t.Cleanup(func() { s.Close() })
		return s
	})
}
