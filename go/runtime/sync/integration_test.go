package sync_test

import (
	"context"
	"encoding/json"
	gosync "sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/runtime/domain/version"
	"hop.top/kit/go/runtime/sync"
)

type syncEntity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (e syncEntity) GetID() string { return e.ID }

// inMemRepo satisfies domain.Repository for syncEntity. The mutex makes it
// safe for concurrent access from a Replicator goroutine plus the test
// goroutine — without it the race detector trips on the underlying map.
type inMemRepo struct {
	mu   gosync.RWMutex
	data map[string]*syncEntity
}

func newInMemRepo() *inMemRepo {
	return &inMemRepo{data: make(map[string]*syncEntity)}
}

func (r *inMemRepo) Create(_ context.Context, e *syncEntity) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[e.GetID()]; ok {
		return domain.ErrConflict
	}
	r.data[e.GetID()] = e
	return nil
}

func (r *inMemRepo) Get(_ context.Context, id string) (*syncEntity, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.data[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return e, nil
}

func (r *inMemRepo) List(_ context.Context, _ domain.Query) ([]syncEntity, error) {
	return nil, nil
}

func (r *inMemRepo) Update(_ context.Context, e *syncEntity) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[e.GetID()]; !ok {
		return domain.ErrNotFound
	}
	r.data[e.GetID()] = e
	return nil
}

func (r *inMemRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, id)
	return nil
}

func TestReplicator_VersionedRepository_Roundtrip(t *testing.T) {
	ctx := context.Background()

	// Two versioned repos, each with own DAG.
	repoA := version.NewVersionedRepository[syncEntity](newInMemRepo())
	repoB := version.NewVersionedRepository[syncEntity](newInMemRepo())

	// Bidirectional transports:
	//   transportAtoB: A pushes diffs here, B pulls from here
	//   transportBtoA: B pushes diffs here, A pulls from here
	transportAtoB := sync.NewMemoryTransport()
	transportBtoA := sync.NewMemoryTransport()

	// --- Step 1: Create entity on A, replicate to B ---
	e := &syncEntity{ID: "e1", Name: "original"}
	require.NoError(t, repoA.Create(ctx, e))

	// Simulate: A pushes create diff to transportAtoB
	after, _ := json.Marshal(e)
	require.NoError(t, transportAtoB.Push(ctx, []sync.Diff{{
		EntityID:  "e1",
		Operation: sync.OpCreate,
		Timestamp: sync.Timestamp{Physical: 100, NodeID: "nodeA"},
		After:     after,
	}}))

	// B pulls from transportAtoB
	repB := sync.NewReplicator[syncEntity](repoB,
		sync.WithRemote[syncEntity](sync.Remote{
			Name: "fromA", Transport: transportAtoB, Mode: sync.PullOnly,
		}),
		sync.WithInterval[syncEntity](30*time.Millisecond),
	)
	require.NoError(t, repB.Start(ctx))
	time.Sleep(80 * time.Millisecond)
	_ = repB.Stop()

	// Verify entity replicated to B with version history
	gotB, err := repoB.Get(ctx, "e1")
	require.NoError(t, err)
	assert.Equal(t, "original", gotB.Name)

	versionsA := repoA.ListVersions("e1")
	require.Len(t, versionsA, 1, "repo A: initial create version")

	// B gets version(s) via VersionedRepository (applyDiff path)
	versionsB := repoB.ListVersions("e1")
	require.NotEmpty(t, versionsB, "repo B: should have at least one version")

	// --- Step 2: Update on B, replicate back to A ---
	updated := &syncEntity{ID: "e1", Name: "modified-by-B"}
	require.NoError(t, repoB.Update(ctx, updated))

	versionsB = repoB.ListVersions("e1")
	require.GreaterOrEqual(t, len(versionsB), 2, "repo B: at least create + update")

	// B pushes update diff to transportBtoA
	afterB, _ := json.Marshal(updated)
	require.NoError(t, transportBtoA.Push(ctx, []sync.Diff{{
		EntityID:  "e1",
		Operation: sync.OpUpdate,
		Timestamp: sync.Timestamp{Physical: 200, NodeID: "nodeB"},
		After:     afterB,
	}}))

	// A pulls from transportBtoA
	repA := sync.NewReplicator[syncEntity](repoA,
		sync.WithRemote[syncEntity](sync.Remote{
			Name: "fromB", Transport: transportBtoA, Mode: sync.PullOnly,
		}),
		sync.WithInterval[syncEntity](30*time.Millisecond),
	)
	require.NoError(t, repA.Start(ctx))
	time.Sleep(80 * time.Millisecond)
	_ = repA.Stop()

	// Verify update arrived at A
	gotA, err := repoA.Get(ctx, "e1")
	require.NoError(t, err)
	assert.Equal(t, "modified-by-B", gotA.Name)

	// A now has create + at least one update version
	versionsA = repoA.ListVersions("e1")
	require.GreaterOrEqual(t, len(versionsA), 2, "repo A: create + replicated update")

	// --- Step 3: Version history convergence ---
	// Both heads reflect the same entity state (same content hash)
	lastA := versionsA[len(versionsA)-1]
	lastB := versionsB[len(versionsB)-1]
	assert.Equal(t, lastA.Hash, lastB.Hash,
		"head versions should have same hash: both reflect modified-by-B state")

	// Both DAGs have a single head (linear history, no branching)
	assert.Len(t, repoA.DAG().Heads(), 1, "repo A DAG: single head")
	assert.Len(t, repoB.DAG().Heads(), 1, "repo B DAG: single head")
}
