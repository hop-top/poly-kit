package durabletask

import (
	"context"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/id"
	"hop.top/kit/go/runtime/job"
)

func TestGenerateID_Format(t *testing.T) {
	got := generateID()
	assert.Regexp(t, regexp.MustCompile(`^dtj_[0-9a-z]{26}$`), got)
	p, err := id.Parse(got)
	require.NoError(t, err)
	assert.Equal(t, "dtj", p.Prefix)
}

func TestGenerateID_NoDuplicates(t *testing.T) {
	const n = 1_000_000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		s := generateID()
		if _, dup := seen[s]; dup {
			t.Fatalf("duplicate id %q after %d ids", s, i)
		}
		seen[s] = struct{}{}
	}
}

// Rows minted before the TypeID switch carry "dtj_" + 8 hex. They must
// keep loading and moving through the lifecycle.
func TestLegacyIDStillWorks(t *testing.T) {
	ctx := context.Background()
	e, err := New("")
	require.NoError(t, err)
	t.Cleanup(func() { e.Close() })

	const legacy = "dtj_1a2b3c4d"
	_, err = e.db.ExecContext(ctx,
		`INSERT INTO jobs (id, queue, type, status, created_at, updated_at)
		 VALUES (?, 'q', 't', 'pending', 1, 1)`, legacy)
	require.NoError(t, err)

	got, err := e.Get(ctx, legacy)
	require.NoError(t, err)
	assert.Equal(t, legacy, got.ID)

	c, err := e.Claim(ctx, "q", "w")
	require.NoError(t, err)
	require.NotNil(t, c)
	assert.Equal(t, legacy, c.ID)
	require.NoError(t, e.Complete(ctx, legacy, nil))

	n, err := e.Prune(ctx, e.nowFunc().Add(1))
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	var _ job.Pruner = e
}
