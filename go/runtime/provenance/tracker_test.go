package provenance_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/runtime/provenance"
)

func TestTrack_NilContextReturnsNoopTracker(t *testing.T) {
	tr := provenance.Track(context.TODO())
	require.NotNil(t, tr)
	// no-op tracker should accept writes without panic
	err := tr.Synthesize("/foo", provenance.Provenance{
		Source: provenance.SourceInferred, Version: "v1",
	})
	assert.NoError(t, err)
}

func TestWithTracker_Roundtrip(t *testing.T) {
	tr := provenance.NewTracker()
	ctx := provenance.WithTracker(context.Background(), tr)
	got := provenance.Track(ctx)
	assert.Same(t, tr, got)
}

func TestTracker_Synthesize_RecordsAndLooksUp(t *testing.T) {
	tr := provenance.NewTracker()
	require.NoError(t, tr.Synthesize("/email", provenance.Provenance{
		Source: provenance.SourceInferred, Version: "algo-v1",
	}))
	p, ok := tr.Lookup("/email")
	require.True(t, ok)
	assert.Equal(t, provenance.SourceInferred, p.Source)
	assert.Equal(t, "1", p.SchemaVersion)
}

func TestTracker_Cache_RejectsDefaulted(t *testing.T) {
	tr := provenance.NewTracker()
	err := tr.Cache("/x", provenance.Provenance{Source: provenance.SourceDefaulted})
	assert.Error(t, err)
	assert.Contains(t, tr.InvalidPaths(), "/x")
}

func TestTracker_RejectsInvalidProvenance(t *testing.T) {
	tr := provenance.NewTracker()
	err := tr.Synthesize("/bad", provenance.Provenance{
		Source: provenance.SourceCached, // requires URL + FetchedAt
	})
	require.Error(t, err)
	assert.Contains(t, tr.InvalidPaths(), "/bad")
	// And no good entry got recorded.
	_, ok := tr.Lookup("/bad")
	assert.False(t, ok)
}

func TestTracker_EmptyPathRejected(t *testing.T) {
	tr := provenance.NewTracker()
	err := tr.Synthesize("", provenance.Provenance{
		Source: provenance.SourceInferred, Version: "v1",
	})
	assert.Error(t, err)
}

func TestTracker_Concurrent_Synthesize(t *testing.T) {
	tr := provenance.NewTracker()
	now := time.Now().UTC()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := "/i/" + itoa(i)
			_ = tr.Cache(path, provenance.Provenance{
				Source: provenance.SourceCached, URL: "https://x", FetchedAt: now,
			})
		}(i)
	}
	wg.Wait()
	assert.Len(t, tr.Paths(), 100)
}

func TestTracker_LastWriteWins(t *testing.T) {
	tr := provenance.NewTracker()
	now := time.Now().UTC()
	require.NoError(t, tr.Cache("/a", provenance.Provenance{
		Source: provenance.SourceCached, URL: "https://v1", FetchedAt: now,
	}))
	require.NoError(t, tr.Cache("/a", provenance.Provenance{
		Source: provenance.SourceCached, URL: "https://v2", FetchedAt: now,
	}))
	p, _ := tr.Lookup("/a")
	assert.Equal(t, "https://v2", p.URL)
}

func TestTracker_Snapshot_DefensiveCopy(t *testing.T) {
	tr := provenance.NewTracker()
	now := time.Now().UTC()
	require.NoError(t, tr.Cache("/k", provenance.Provenance{
		Source: provenance.SourceCached, URL: "https://x", FetchedAt: now,
	}))
	snap := tr.Snapshot()
	snap["/k"] = provenance.Provenance{} // mutate caller copy
	p, _ := tr.Lookup("/k")
	assert.Equal(t, "https://x", p.URL, "internal state should not be affected")
}

// itoa avoids strconv import noise in the test
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
