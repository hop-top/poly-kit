package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/domain"
)

func TestNoteServiceCRUD(t *testing.T) {
	// In-memory versioned repo via the noteService adapter.
	repo := &memRepo{items: make(map[string]Note)}
	ns := &noteService{vr: nil}
	// Use memService directly for unit test (no SQLite needed).
	ms := &memService{repo: repo}

	ctx := context.Background()

	// Create
	n, err := ms.Create(ctx, Note{Title: "Test", Body: "Hello"})
	require.NoError(t, err)
	assert.NotEmpty(t, n.ID)
	assert.Equal(t, "Test", n.Title)

	// Get
	got, err := ms.Get(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, n.ID, got.ID)

	// List
	items, err := ms.List(ctx, domain.Query{})
	require.NoError(t, err)
	assert.Len(t, items, 1)

	// Update
	n.Body = "Updated"
	updated, err := ms.Update(ctx, n)
	require.NoError(t, err)
	assert.Equal(t, "Updated", updated.Body)

	// Delete
	err = ms.Delete(ctx, n.ID)
	require.NoError(t, err)

	_, err = ms.Get(ctx, n.ID)
	assert.Error(t, err)

	_ = ns // ensure noteService compiles with expected shape
}

func TestCommandsExist(t *testing.T) {
	// Verify the CLI has all expected subcommands.
	// We can't call cli.New without identity/db, so just verify
	// command constructors return non-nil.
	ms := &memService{repo: &memRepo{items: make(map[string]Note)}}

	newC := newCmd(ms)
	assert.Equal(t, "new", newC.Name())

	editC := editCmd(ms)
	assert.Equal(t, "edit", editC.Name())

	listC := listCmd(ms)
	assert.Equal(t, "list", listC.Name())

	getC := getCmd(ms)
	assert.Equal(t, "get", getC.Name())

	deleteC := deleteCmd(ms)
	assert.Equal(t, "delete", deleteC.Name())
}

// memRepo is a minimal in-memory repository for testing.
type memRepo struct {
	items map[string]Note
}

func (r *memRepo) Create(_ context.Context, entity *Note) error {
	r.items[entity.ID] = *entity
	return nil
}

func (r *memRepo) Get(_ context.Context, id string) (*Note, error) {
	n, ok := r.items[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &n, nil
}

func (r *memRepo) List(_ context.Context, _ domain.Query) ([]Note, error) {
	out := make([]Note, 0, len(r.items))
	for _, n := range r.items {
		out = append(out, n)
	}
	return out, nil
}

func (r *memRepo) Update(_ context.Context, entity *Note) error {
	if _, ok := r.items[entity.ID]; !ok {
		return domain.ErrNotFound
	}
	r.items[entity.ID] = *entity
	return nil
}

func (r *memRepo) Delete(_ context.Context, id string) error {
	delete(r.items, id)
	return nil
}

// memService implements api.Service[Note] for tests without SQLite.
type memService struct {
	repo *memRepo
}

func (s *memService) Create(_ context.Context, n Note) (Note, error) {
	if n.ID == "" {
		n.ID = "test-id"
	}
	if err := s.repo.Create(context.Background(), &n); err != nil {
		return Note{}, err
	}
	return n, nil
}

func (s *memService) Get(_ context.Context, id string) (Note, error) {
	n, err := s.repo.Get(context.Background(), id)
	if err != nil {
		return Note{}, err
	}
	return *n, nil
}

func (s *memService) List(_ context.Context, q domain.Query) ([]Note, error) {
	return s.repo.List(context.Background(), q)
}

func (s *memService) Update(_ context.Context, n Note) (Note, error) {
	if err := s.repo.Update(context.Background(), &n); err != nil {
		return Note{}, err
	}
	return n, nil
}

func (s *memService) Delete(_ context.Context, id string) error {
	return s.repo.Delete(context.Background(), id)
}
