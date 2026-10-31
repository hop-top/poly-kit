package sqlite_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"hop.top/kit/go/storage/kv"
	"hop.top/kit/go/storage/kv/internal/ttlcorpus"
	"hop.top/kit/go/storage/kv/sqlite"
)

// TestPutIfAbsentWithTTLCorpus replays contracts/kv-v1/ttl.json, the
// sequences the Rust SqliteStore replays too.
func TestPutIfAbsentWithTTLCorpus(t *testing.T) {
	ttlcorpus.Run(t, func(t *testing.T) kv.TTLInserter {
		t.Helper()
		s, err := sqlite.New(filepath.Join(t.TempDir(), "ttl.db"))
		require.NoError(t, err)
		t.Cleanup(func() { s.Close() })
		return s
	})
}
