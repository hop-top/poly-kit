package domain_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/domain"
)

// --- test entity ---

type testEntity struct {
	ID   string
	Name string
}

func (e testEntity) GetID() string { return e.ID }

// --- mock repo ---

type mockRepo struct {
	mu       sync.Mutex
	entities map[string]*testEntity
	createFn func(ctx context.Context, e *testEntity) error
}

func newMockRepo() *mockRepo {
	return &mockRepo{entities: make(map[string]*testEntity)}
}

func (m *mockRepo) Create(_ context.Context, e *testEntity) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createFn != nil {
		return m.createFn(nil, e)
	}
	if _, ok := m.entities[e.GetID()]; ok {
		return domain.ErrConflict
	}
	m.entities[e.GetID()] = e
	return nil
}

func (m *mockRepo) Get(_ context.Context, id string) (*testEntity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entities[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return e, nil
}

func (m *mockRepo) List(_ context.Context, _ domain.Query) ([]testEntity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]testEntity, 0, len(m.entities))
	for _, e := range m.entities {
		out = append(out, *e)
	}
	return out, nil
}

func (m *mockRepo) Update(_ context.Context, e *testEntity) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.entities[e.GetID()]; !ok {
		return domain.ErrNotFound
	}
	m.entities[e.GetID()] = e
	return nil
}

func (m *mockRepo) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.entities[id]; !ok {
		return domain.ErrNotFound
	}
	delete(m.entities, id)
	return nil
}

// --- mock validator ---

type mockValidator struct {
	err error
}

func (v *mockValidator) Validate(_ context.Context, _ testEntity) error {
	return v.err
}

// --- mock audit ---

type mockAudit struct {
	mu      sync.Mutex
	entries []*domain.AuditEntry
}

func (a *mockAudit) AddEntry(_ context.Context, e *domain.AuditEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	return nil
}

func (a *mockAudit) ListEntries(_ context.Context, entityID string) ([]*domain.AuditEntry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []*domain.AuditEntry
	for _, e := range a.entries {
		if e.EntityID == entityID {
			out = append(out, e)
		}
	}
	return out, nil
}

// --- mock publisher ---

type mockPublisher struct {
	mu     sync.Mutex
	events []publishedEvent
}

type publishedEvent struct {
	topic   string
	source  string
	payload any
}

func (p *mockPublisher) Publish(_ context.Context, topic, source string, payload any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, publishedEvent{topic: topic, source: source, payload: payload})
	return nil
}

func (p *mockPublisher) topics() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.events))
	for i, e := range p.events {
		out[i] = e.topic
	}
	return out
}

// --- tests ---

func TestService_CreateGetDelete(t *testing.T) {
	repo := newMockRepo()
	svc := domain.NewService[testEntity](repo)
	ctx := context.Background()

	e := &testEntity{ID: "1", Name: "alpha"}
	require.NoError(t, svc.Create(ctx, e))

	got, err := svc.Get(ctx, "1")
	require.NoError(t, err)
	assert.Equal(t, "alpha", got.Name)

	require.NoError(t, svc.Delete(ctx, "1"))
	_, err = svc.Get(ctx, "1")
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestService_Update(t *testing.T) {
	repo := newMockRepo()
	svc := domain.NewService[testEntity](repo)
	ctx := context.Background()

	e := &testEntity{ID: "1", Name: "alpha"}
	require.NoError(t, svc.Create(ctx, e))

	e.Name = "beta"
	require.NoError(t, svc.Update(ctx, e))

	got, err := svc.Get(ctx, "1")
	require.NoError(t, err)
	assert.Equal(t, "beta", got.Name)
}

func TestService_List(t *testing.T) {
	repo := newMockRepo()
	svc := domain.NewService[testEntity](repo)
	ctx := context.Background()

	require.NoError(t, svc.Create(ctx, &testEntity{ID: "1", Name: "a"}))
	require.NoError(t, svc.Create(ctx, &testEntity{ID: "2", Name: "b"}))

	list, err := svc.List(ctx, domain.Query{})
	require.NoError(t, err)
	assert.Len(t, list, 2)
}

func TestService_ValidationBlocks(t *testing.T) {
	repo := newMockRepo()
	v := &mockValidator{err: domain.ErrValidation}
	svc := domain.NewService[testEntity](repo, domain.WithValidation[testEntity](v))
	ctx := context.Background()

	err := svc.Create(ctx, &testEntity{ID: "1", Name: "x"})
	assert.ErrorIs(t, err, domain.ErrValidation)

	// Repo should be empty — validation blocked persist.
	_, err = repo.Get(ctx, "1")
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestService_ValidationBlocksUpdate(t *testing.T) {
	repo := newMockRepo()
	v := &mockValidator{}
	svc := domain.NewService[testEntity](repo, domain.WithValidation[testEntity](v))
	ctx := context.Background()

	e := &testEntity{ID: "1", Name: "ok"}
	require.NoError(t, svc.Create(ctx, e))

	v.err = errors.New("bad")
	e.Name = "bad"
	err := svc.Update(ctx, e)
	assert.Error(t, err)
}

func TestService_AuditRecordsActions(t *testing.T) {
	repo := newMockRepo()
	audit := &mockAudit{}
	svc := domain.NewService[testEntity](repo, domain.WithAudit[testEntity](audit))
	ctx := context.Background()

	e := &testEntity{ID: "1", Name: "a"}
	require.NoError(t, svc.Create(ctx, e))

	e.Name = "b"
	require.NoError(t, svc.Update(ctx, e))
	require.NoError(t, svc.Delete(ctx, "1"))

	entries, err := audit.ListEntries(ctx, "1")
	require.NoError(t, err)
	require.Len(t, entries, 3)
	assert.Equal(t, "created", entries[0].Action)
	assert.Equal(t, "updated", entries[1].Action)
	assert.Equal(t, "deleted", entries[2].Action)
}

func TestService_PublisherPublishesEvents(t *testing.T) {
	repo := newMockRepo()
	pub := &mockPublisher{}

	svc := domain.NewService[testEntity](repo, domain.WithPublisher[testEntity](pub))
	ctx := context.Background()

	e := &testEntity{ID: "1", Name: "a"}
	require.NoError(t, svc.Create(ctx, e))
	e.Name = "b"
	require.NoError(t, svc.Update(ctx, e))
	require.NoError(t, svc.Delete(ctx, "1"))

	topics := pub.topics()
	// Pre-events fire on every CRUD action.
	assert.Contains(t, topics, "kit.runtime.entity.pre_validated")
	assert.Contains(t, topics, "kit.runtime.entity.pre_persisted")
	// Post-events fire on every CRUD action.
	assert.Contains(t, topics, "kit.runtime.entity.created")
	assert.Contains(t, topics, "kit.runtime.entity.updated")
	assert.Contains(t, topics, "kit.runtime.entity.deleted")
}
