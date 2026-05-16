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

// vetoPublisher returns vetoErr from Publish whenever topic matches
// vetoTopic. All other topics succeed. It records every call so tests
// can assert which seams fired before the veto aborted the operation.
type vetoPublisher struct {
	mu        sync.Mutex
	vetoTopic string
	vetoErr   error
	events    []publishedEvent
}

func (p *vetoPublisher) Publish(_ context.Context, topic, source string, payload any) error {
	p.mu.Lock()
	p.events = append(p.events, publishedEvent{topic: topic, source: source, payload: payload})
	p.mu.Unlock()
	if topic == p.vetoTopic {
		return p.vetoErr
	}
	return nil
}

func (p *vetoPublisher) topics() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.events))
	for i, e := range p.events {
		out[i] = e.topic
	}
	return out
}

// countingValidator records how many times Validate ran so tests can
// assert that pre_validated veto skipped the validator entirely.
type countingValidator struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (v *countingValidator) Validate(_ context.Context, _ testEntity) error {
	v.mu.Lock()
	v.calls++
	v.mu.Unlock()
	return v.err
}

func (v *countingValidator) Calls() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.calls
}

// countingRepo wraps mockRepo to expose call counts on Create / Update /
// Delete. Used to verify a pre-event veto aborted before the repo was
// touched.
type countingRepo struct {
	*mockRepo
	createCalls, updateCalls, deleteCalls int
	mu                                    sync.Mutex
}

func newCountingRepo() *countingRepo {
	return &countingRepo{mockRepo: newMockRepo()}
}

func (r *countingRepo) Create(ctx context.Context, e *testEntity) error {
	r.mu.Lock()
	r.createCalls++
	r.mu.Unlock()
	return r.mockRepo.Create(ctx, e)
}

func (r *countingRepo) Update(ctx context.Context, e *testEntity) error {
	r.mu.Lock()
	r.updateCalls++
	r.mu.Unlock()
	return r.mockRepo.Update(ctx, e)
}

func (r *countingRepo) Delete(ctx context.Context, id string) error {
	r.mu.Lock()
	r.deleteCalls++
	r.mu.Unlock()
	return r.mockRepo.Delete(ctx, id)
}

func (r *countingRepo) counts() (int, int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.createCalls, r.updateCalls, r.deleteCalls
}

// --- pre_validated veto tests ---

func TestService_PreValidatedVeto_BlocksBeforeValidation_OnCreate(t *testing.T) {
	vetoErr := errors.New("intent denied: caller may not create")
	pub := &vetoPublisher{
		vetoTopic: "kit.runtime.entity.pre_validated",
		vetoErr:   vetoErr,
	}
	v := &countingValidator{}
	repo := newCountingRepo()
	svc := domain.NewService[testEntity](repo,
		domain.WithPublisher[testEntity](pub),
		domain.WithValidation[testEntity](v),
	)

	err := svc.Create(context.Background(), &testEntity{ID: "1", Name: "x"})
	require.Error(t, err)
	assert.ErrorIs(t, err, vetoErr,
		"veto error should surface via errors.Is for caller-side handling")
	assert.Contains(t, err.Error(), "pre-validated veto",
		"wrap prefix is part of the contract")

	// Validation never ran.
	assert.Zero(t, v.Calls(), "validator must not run when pre_validated vetoes")

	// Repo never touched.
	c, u, d := repo.counts()
	assert.Zero(t, c, "repo.Create must not run on pre_validated veto")
	assert.Zero(t, u)
	assert.Zero(t, d)

	// Only pre_validated fired; no pre_persisted, no post-event.
	assert.Equal(t, []string{"kit.runtime.entity.pre_validated"}, pub.topics())
}

func TestService_PreValidatedVeto_BlocksBeforeValidation_OnUpdate(t *testing.T) {
	vetoErr := errors.New("intent denied")
	pub := &vetoPublisher{
		vetoTopic: "kit.runtime.entity.pre_validated",
		vetoErr:   vetoErr,
	}
	v := &countingValidator{}
	repo := newCountingRepo()
	// Pre-seed a row so Update would otherwise succeed.
	require.NoError(t, repo.mockRepo.Create(context.Background(), &testEntity{ID: "1", Name: "old"}))

	svc := domain.NewService[testEntity](repo,
		domain.WithPublisher[testEntity](pub),
		domain.WithValidation[testEntity](v),
	)

	err := svc.Update(context.Background(), &testEntity{ID: "1", Name: "new"})
	require.Error(t, err)
	assert.ErrorIs(t, err, vetoErr)
	assert.Contains(t, err.Error(), "pre-validated veto")

	assert.Zero(t, v.Calls())
	_, u, _ := repo.counts()
	assert.Zero(t, u, "repo.Update must not run on pre_validated veto")
}

func TestService_PreValidatedVeto_BlocksBeforeValidation_OnDelete(t *testing.T) {
	vetoErr := errors.New("intent denied")
	pub := &vetoPublisher{
		vetoTopic: "kit.runtime.entity.pre_validated",
		vetoErr:   vetoErr,
	}
	repo := newCountingRepo()
	require.NoError(t, repo.mockRepo.Create(context.Background(), &testEntity{ID: "1", Name: "doomed"}))

	svc := domain.NewService[testEntity](repo, domain.WithPublisher[testEntity](pub))

	err := svc.Delete(context.Background(), "1")
	require.Error(t, err)
	assert.ErrorIs(t, err, vetoErr)
	assert.Contains(t, err.Error(), "pre-validated veto")

	_, _, d := repo.counts()
	assert.Zero(t, d, "repo.Delete must not run on pre_validated veto")
}

// --- pre_persisted veto tests ---

func TestService_PrePersistedVeto_BlocksAfterValidation_OnCreate(t *testing.T) {
	vetoErr := errors.New("business rule: missing approval")
	pub := &vetoPublisher{
		vetoTopic: "kit.runtime.entity.pre_persisted",
		vetoErr:   vetoErr,
	}
	v := &countingValidator{}
	repo := newCountingRepo()

	svc := domain.NewService[testEntity](repo,
		domain.WithPublisher[testEntity](pub),
		domain.WithValidation[testEntity](v),
	)

	err := svc.Create(context.Background(), &testEntity{ID: "1", Name: "x"})
	require.Error(t, err)
	assert.ErrorIs(t, err, vetoErr)
	assert.Contains(t, err.Error(), "pre-persisted veto")

	// Validation ran (pre_persisted fires AFTER it).
	assert.Equal(t, 1, v.Calls(),
		"validator must run before pre_persisted seam")

	// Repo never touched.
	c, _, _ := repo.counts()
	assert.Zero(t, c, "repo.Create must not run on pre_persisted veto")

	// Both pre topics fired; no post-event.
	assert.Equal(t, []string{
		"kit.runtime.entity.pre_validated",
		"kit.runtime.entity.pre_persisted",
	}, pub.topics())
}

func TestService_PrePersistedVeto_BlocksAfterValidation_OnUpdate(t *testing.T) {
	vetoErr := errors.New("business rule")
	pub := &vetoPublisher{
		vetoTopic: "kit.runtime.entity.pre_persisted",
		vetoErr:   vetoErr,
	}
	v := &countingValidator{}
	repo := newCountingRepo()
	require.NoError(t, repo.mockRepo.Create(context.Background(), &testEntity{ID: "1", Name: "old"}))

	svc := domain.NewService[testEntity](repo,
		domain.WithPublisher[testEntity](pub),
		domain.WithValidation[testEntity](v),
	)

	err := svc.Update(context.Background(), &testEntity{ID: "1", Name: "new"})
	require.Error(t, err)
	assert.ErrorIs(t, err, vetoErr)

	assert.Equal(t, 1, v.Calls())
	_, u, _ := repo.counts()
	assert.Zero(t, u, "repo.Update must not run on pre_persisted veto")
}

func TestService_PrePersistedVeto_BlocksAfterValidation_OnDelete(t *testing.T) {
	vetoErr := errors.New("business rule")
	pub := &vetoPublisher{
		vetoTopic: "kit.runtime.entity.pre_persisted",
		vetoErr:   vetoErr,
	}
	repo := newCountingRepo()
	require.NoError(t, repo.mockRepo.Create(context.Background(), &testEntity{ID: "1", Name: "doomed"}))

	svc := domain.NewService[testEntity](repo, domain.WithPublisher[testEntity](pub))

	err := svc.Delete(context.Background(), "1")
	require.Error(t, err)
	assert.ErrorIs(t, err, vetoErr)

	_, _, d := repo.counts()
	assert.Zero(t, d, "repo.Delete must not run on pre_persisted veto")
}

// --- mixed: pre_validated allows, pre_persisted denies ---

func TestService_PreValidatedAllowsButPrePersistedDenies(t *testing.T) {
	vetoErr := errors.New("validated but not allowed to persist")
	pub := &vetoPublisher{
		vetoTopic: "kit.runtime.entity.pre_persisted",
		vetoErr:   vetoErr,
	}
	v := &countingValidator{}
	repo := newCountingRepo()
	svc := domain.NewService[testEntity](repo,
		domain.WithPublisher[testEntity](pub),
		domain.WithValidation[testEntity](v),
	)

	err := svc.Create(context.Background(), &testEntity{ID: "1", Name: "x"})
	require.Error(t, err)
	assert.ErrorIs(t, err, vetoErr)
	assert.Equal(t, 1, v.Calls())

	c, _, _ := repo.counts()
	assert.Zero(t, c)
}

// --- payload shape ---

func TestService_PreEventPayload_OpAndPhase(t *testing.T) {
	pub := &vetoPublisher{} // no veto; record only
	repo := newCountingRepo()
	svc := domain.NewService[testEntity](repo, domain.WithPublisher[testEntity](pub))
	ctx := context.Background()

	require.NoError(t, svc.Create(ctx, &testEntity{ID: "1", Name: "a"}))
	require.NoError(t, svc.Update(ctx, &testEntity{ID: "1", Name: "b"}))
	require.NoError(t, svc.Delete(ctx, "1"))

	pub.mu.Lock()
	defer pub.mu.Unlock()

	// Filter to pre-events only and assert shape.
	var preEvents []publishedEvent
	for _, e := range pub.events {
		if e.topic == "kit.runtime.entity.pre_validated" ||
			e.topic == "kit.runtime.entity.pre_persisted" {
			preEvents = append(preEvents, e)
		}
	}
	require.Len(t, preEvents, 6, "two pre-events per CRUD action × three actions")

	wantPairs := []struct {
		topic string
		op    domain.Op
		phase domain.Phase
	}{
		{"kit.runtime.entity.pre_validated", domain.OpCreate, domain.PhasePreValidated},
		{"kit.runtime.entity.pre_persisted", domain.OpCreate, domain.PhasePrePersisted},
		{"kit.runtime.entity.pre_validated", domain.OpUpdate, domain.PhasePreValidated},
		{"kit.runtime.entity.pre_persisted", domain.OpUpdate, domain.PhasePrePersisted},
		{"kit.runtime.entity.pre_validated", domain.OpDelete, domain.PhasePreValidated},
		{"kit.runtime.entity.pre_persisted", domain.OpDelete, domain.PhasePrePersisted},
	}

	for i, want := range wantPairs {
		got := preEvents[i]
		assert.Equal(t, want.topic, got.topic, "event[%d] topic", i)
		payload, ok := got.payload.(domain.PreEntityPayload)
		require.True(t, ok, "event[%d] payload type", i)
		assert.Equal(t, want.op, payload.Op, "event[%d] op", i)
		assert.Equal(t, want.phase, payload.Phase, "event[%d] phase", i)
		assert.Equal(t, "1", payload.EntityID, "event[%d] entity id", i)
		// Delete carries no entity body; create/update do.
		if want.op == domain.OpDelete {
			assert.Nil(t, payload.Entity, "delete pre-events have nil Entity")
		} else {
			assert.NotNil(t, payload.Entity, "create/update pre-events carry the entity")
		}
	}
}

// --- regression: nil publisher and no-veto publisher both work ---

func TestService_NoPublisher_FullCRUDWorks(t *testing.T) {
	repo := newCountingRepo()
	svc := domain.NewService[testEntity](repo)
	ctx := context.Background()

	require.NoError(t, svc.Create(ctx, &testEntity{ID: "1", Name: "a"}))
	require.NoError(t, svc.Update(ctx, &testEntity{ID: "1", Name: "b"}))
	require.NoError(t, svc.Delete(ctx, "1"))

	c, u, d := repo.counts()
	assert.Equal(t, 1, c)
	assert.Equal(t, 1, u)
	assert.Equal(t, 1, d)
}

func TestService_NoVetoSubscriber_PostEventsStillFire(t *testing.T) {
	pub := &vetoPublisher{} // empty vetoTopic = nothing vetoes
	repo := newCountingRepo()
	svc := domain.NewService[testEntity](repo, domain.WithPublisher[testEntity](pub))
	ctx := context.Background()

	require.NoError(t, svc.Create(ctx, &testEntity{ID: "1", Name: "a"}))

	// All three Create-phase events fired in order.
	assert.Equal(t, []string{
		"kit.runtime.entity.pre_validated",
		"kit.runtime.entity.pre_persisted",
		"kit.runtime.entity.created",
	}, pub.topics())
}
