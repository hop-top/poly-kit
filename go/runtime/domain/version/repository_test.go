package version

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/runtime/domain"
)

// testEntity implements domain.Entity for testing.
type testEntity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (e testEntity) GetID() string { return e.ID }

// memRepo is a simple in-memory repository for testing.
type memRepo struct {
	data map[string]*testEntity
}

func newMemRepo() *memRepo {
	return &memRepo{data: make(map[string]*testEntity)}
}

func (r *memRepo) Create(_ context.Context, e *testEntity) error {
	if _, ok := r.data[e.GetID()]; ok {
		return domain.ErrConflict
	}
	r.data[e.GetID()] = e
	return nil
}

func (r *memRepo) Get(_ context.Context, id string) (*testEntity, error) {
	e, ok := r.data[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return e, nil
}

func (r *memRepo) List(_ context.Context, _ domain.Query) ([]testEntity, error) {
	var out []testEntity
	for _, e := range r.data {
		out = append(out, *e)
	}
	return out, nil
}

func (r *memRepo) Update(_ context.Context, e *testEntity) error {
	if _, ok := r.data[e.GetID()]; !ok {
		return domain.ErrNotFound
	}
	r.data[e.GetID()] = e
	return nil
}

func (r *memRepo) Delete(_ context.Context, id string) error {
	if _, ok := r.data[id]; !ok {
		return domain.ErrNotFound
	}
	delete(r.data, id)
	return nil
}

func TestVersionedRepository_CreateAddsVersion(t *testing.T) {
	repo := newMemRepo()
	vr := NewVersionedRepository[testEntity](repo)
	ctx := context.Background()

	e := &testEntity{ID: "e1", Name: "first"}
	require.NoError(t, vr.Create(ctx, e))

	versions := vr.ListVersions("e1")
	assert.Len(t, versions, 1)
	assert.NotEmpty(t, versions[0].Hash)
}

func TestVersionedRepository_UpdateAddsVersion(t *testing.T) {
	repo := newMemRepo()
	vr := NewVersionedRepository[testEntity](repo)
	ctx := context.Background()

	e := &testEntity{ID: "e1", Name: "first"}
	require.NoError(t, vr.Create(ctx, e))

	e.Name = "updated"
	require.NoError(t, vr.Update(ctx, e))

	versions := vr.ListVersions("e1")
	assert.Len(t, versions, 2)
	// second version's parent is the first
	assert.Equal(t, []string{versions[0].ID}, versions[1].ParentIDs)
}

func TestVersionedRepository_ListVersions(t *testing.T) {
	repo := newMemRepo()
	vr := NewVersionedRepository[testEntity](repo)
	ctx := context.Background()

	e := &testEntity{ID: "e1", Name: "v1"}
	require.NoError(t, vr.Create(ctx, e))
	e.Name = "v2"
	require.NoError(t, vr.Update(ctx, e))
	e.Name = "v3"
	require.NoError(t, vr.Update(ctx, e))

	versions := vr.ListVersions("e1")
	assert.Len(t, versions, 3)
}

func TestVersionedRepository_Revert(t *testing.T) {
	repo := newMemRepo()
	vr := NewVersionedRepository[testEntity](repo)
	ctx := context.Background()

	e := &testEntity{ID: "e1", Name: "v1"}
	require.NoError(t, vr.Create(ctx, e))
	firstVID := vr.ListVersions("e1")[0].ID

	e.Name = "v2"
	require.NoError(t, vr.Update(ctx, e))

	require.NoError(t, vr.Revert(ctx, "e1", firstVID))

	versions := vr.ListVersions("e1")
	assert.Len(t, versions, 3) // create + update + revert
	// revert version has two parents (current head + target)
	last := versions[len(versions)-1]
	assert.Len(t, last.ParentIDs, 2)
}

func TestVersionedRepository_Revert_UnknownEntity(t *testing.T) {
	repo := newMemRepo()
	vr := NewVersionedRepository[testEntity](repo)
	ctx := context.Background()

	err := vr.Revert(ctx, "missing", "v1")
	assert.Error(t, err)
}
