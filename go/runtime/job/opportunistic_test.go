package job_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/job"
	"hop.top/kit/go/runtime/job/mock"
)

func runOneHandlers(n *int) job.HandlerMap {
	return job.HandlerMap{
		"t": func(context.Context, job.Job) error { *n++; return nil },
	}
}

func TestRunOneCooldownDirIsInjected(t *testing.T) {
	// A fresh marker in the OS temp dir must not throttle RunOne when
	// the marker dir is injected elsewhere.
	t.Setenv("TMPDIR", t.TempDir())
	ctx := context.Background()
	svc := mock.New()
	var n int
	dir := t.TempDir()

	for i := 0; i < 2; i++ {
		_, err := svc.Enqueue(ctx, job.EnqueueOpts{Queue: "q", Type: "t"})
		require.NoError(t, err)
	}
	require.NoError(t, job.RunOne(ctx, svc, "q", "w", runOneHandlers(&n),
		job.WithCooldownDir(dir)))
	assert.Equal(t, 1, n)

	// marker written in the injected dir, not TMPDIR
	ents, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, ents, 1)
	tmp, err := os.ReadDir(os.TempDir())
	require.NoError(t, err)
	assert.Empty(t, tmp)

	// second call inside the TTL is throttled by the marker
	require.NoError(t, job.RunOne(ctx, svc, "q", "w", runOneHandlers(&n),
		job.WithCooldownDir(dir)))
	assert.Equal(t, 1, n)

	// a different dir has no marker: runs
	require.NoError(t, job.RunOne(ctx, svc, "q", "w", runOneHandlers(&n),
		job.WithCooldownDir(filepath.Join(t.TempDir(), "sub"))))
	assert.Equal(t, 2, n)
}

func TestRunOneDefaultsToOSTempDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	ctx := context.Background()
	svc := mock.New()
	_, err := svc.Enqueue(ctx, job.EnqueueOpts{Queue: "q", Type: "t"})
	require.NoError(t, err)
	var n int
	require.NoError(t, job.RunOne(ctx, svc, "q", "w", runOneHandlers(&n)))
	assert.Equal(t, 1, n)
	ents, _ := os.ReadDir(tmp)
	assert.Len(t, ents, 1)
}
