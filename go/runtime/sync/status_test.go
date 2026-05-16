package sync

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/runtime/domain"
)

// testEntity for status tests
type statusTestEntity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (e statusTestEntity) GetID() string { return e.ID }

type statusMemRepo struct {
	data map[string]*statusTestEntity
}

func newStatusMemRepo() *statusMemRepo {
	return &statusMemRepo{data: make(map[string]*statusTestEntity)}
}

func (r *statusMemRepo) Create(_ context.Context, e *statusTestEntity) error {
	r.data[e.GetID()] = e
	return nil
}
func (r *statusMemRepo) Get(_ context.Context, id string) (*statusTestEntity, error) {
	e, ok := r.data[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return e, nil
}
func (r *statusMemRepo) List(_ context.Context, _ domain.Query) ([]statusTestEntity, error) {
	return nil, nil
}
func (r *statusMemRepo) Update(_ context.Context, e *statusTestEntity) error {
	r.data[e.GetID()] = e
	return nil
}
func (r *statusMemRepo) Delete(_ context.Context, id string) error {
	delete(r.data, id)
	return nil
}

func TestStatus_Connected(t *testing.T) {
	repo := newStatusMemRepo()
	mt := NewMemoryTransport()
	rem := Remote{Name: "origin", Transport: mt, Mode: Bidirectional}

	rep := NewReplicator[statusTestEntity](repo,
		WithRemote[statusTestEntity](rem),
		WithInterval[statusTestEntity](50*time.Millisecond),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, rep.Start(ctx))
	defer rep.Stop()

	// Wait for at least one sync cycle
	time.Sleep(100 * time.Millisecond)

	statuses := rep.Status()
	require.Len(t, statuses, 1)
	assert.Equal(t, "origin", statuses[0].Name)
	assert.True(t, statuses[0].Connected)
	assert.Nil(t, statuses[0].LastError)
}

func TestStatus_PendingCount(t *testing.T) {
	repo := newStatusMemRepo()
	mt := NewMemoryTransport()
	rem := Remote{Name: "origin", Transport: mt, Mode: PushOnly}

	rep := NewReplicator[statusTestEntity](repo,
		WithRemote[statusTestEntity](rem),
		WithInterval[statusTestEntity](5*time.Second), // long interval so we control timing
	)

	// Enqueue diffs before starting
	rep.Enqueue(Diff{EntityID: "e1", Operation: OpCreate, Timestamp: Timestamp{Physical: 1, NodeID: "n1"}})
	rep.Enqueue(Diff{EntityID: "e2", Operation: OpCreate, Timestamp: Timestamp{Physical: 2, NodeID: "n1"}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, rep.Start(ctx))
	defer rep.Stop()

	statuses := rep.Status()
	require.Len(t, statuses, 1)
	assert.Equal(t, 2, statuses[0].PendingDiffs)
}

func TestStatus_ErrorTracking(t *testing.T) {
	repo := newStatusMemRepo()
	mt := NewMemoryTransport()
	mt.SetAlive(false) // simulate unreachable
	rem := Remote{Name: "origin", Transport: mt, Mode: Bidirectional}

	rep := NewReplicator[statusTestEntity](repo,
		WithRemote[statusTestEntity](rem),
		WithInterval[statusTestEntity](50*time.Millisecond),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, rep.Start(ctx))
	defer rep.Stop()

	time.Sleep(100 * time.Millisecond)

	statuses := rep.Status()
	require.Len(t, statuses, 1)
	assert.False(t, statuses[0].Connected)
	assert.NotNil(t, statuses[0].LastError)
}
