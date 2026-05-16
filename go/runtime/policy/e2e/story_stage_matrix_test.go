package e2e_test

// Smoke matrix for go/core/stage + runtime/policy + runtime/domain.
// Wires the full stack and verifies allow/deny across the 6 stages
// times {feature track, fix task, doc task} mutations end-to-end.

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/runtime/policy"
	"hop.top/kit/go/runtime/policy/withcel"
)

// trackEntity is a tiny test fixture: implements domain.Entity with a
// kind/op/track_type triple so the policy CEL bindings see the data.
type trackEntity struct {
	ID        string
	Kind      string `json:"kind"`
	TrackType string `json:"track_type"`
}

func (t *trackEntity) GetID() string { return t.ID }

// memRepo is a trivial in-memory Repository[trackEntity] that always
// "succeeds" — we only care about the policy gate.
type memRepo struct {
	mu     sync.Mutex
	stored map[string]trackEntity
}

func newMemRepo() *memRepo { return &memRepo{stored: map[string]trackEntity{}} }

func (r *memRepo) Create(_ context.Context, e *trackEntity) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stored[e.ID] = *e
	return nil
}

func (r *memRepo) Get(_ context.Context, id string) (*trackEntity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.stored[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := v
	return &cp, nil
}
func (r *memRepo) List(_ context.Context, _ domain.Query) ([]trackEntity, error) {
	return nil, nil
}
func (r *memRepo) Update(_ context.Context, e *trackEntity) error {
	return r.Create(context.Background(), e)
}
func (r *memRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.stored, id)
	return nil
}

// busPubAdapter adapts bus.Bus to domain.EventPublisher.
type busPubAdapter struct{ b bus.Bus }

func (a *busPubAdapter) Publish(ctx context.Context, topic, source string, payload any) error {
	return a.b.Publish(ctx, bus.NewEvent(bus.Topic(topic), source, payload))
}

// recVPub captures Publish calls (used as the policy violation publisher).
type recVPub struct {
	mu sync.Mutex
	n  int
}

func (r *recVPub) Publish(_ string, _ any) {
	r.mu.Lock()
	r.n++
	r.mu.Unlock()
}

func (r *recVPub) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n
}

// mockEntityWithTrackType is a custom entity whose payload renders
// `track_type` so the policy binding picks it up.
type mockEntityWithTrackType struct {
	ID    string `json:"id"`
	Kind  string `json:"Kind"`
	TType string `json:"track_type"`
}

func (m *mockEntityWithTrackType) GetID() string { return m.ID }

// TestStageMatrix_E2E exercises the 6-stage matrix through the full
// runtime/domain.Service[T] -> bus -> runtime/policy -> stage.Read
// stack. For each stage, attempts {feature track create, fix task
// create, doc task create} and asserts allow/deny matches the default
// stage.yaml ruleset; verifies kit.runtime.stage.violated emits
// exactly when the policy denies.
func TestStageMatrix_E2E(t *testing.T) {
	type op struct {
		kind      string
		op        string
		trackType string
	}
	type cell struct {
		stage   string
		op      op
		allowed bool
	}

	// 6 stages × 3 attempts = 18 cells.
	cells := []cell{
		// active: everything allowed.
		{"active", op{"track", "create", "feature"}, true},
		{"active", op{"task", "create", "fix"}, true},
		{"active", op{"task", "create", "docs"}, true},

		// public_feedback: only feedback tracks may be created.
		{"public_feedback", op{"track", "create", "feature"}, false},
		{"public_feedback", op{"task", "create", "fix"}, true},
		{"public_feedback", op{"task", "create", "docs"}, true},

		// feature_freeze: feature tracks blocked; tasks (fix/docs) ok.
		{"feature_freeze", op{"track", "create", "feature"}, false},
		{"feature_freeze", op{"task", "create", "fix"}, true},
		{"feature_freeze", op{"task", "create", "docs"}, true},

		// maintenance: tracks blocked; tasks ok.
		{"maintenance", op{"track", "create", "feature"}, false},
		{"maintenance", op{"task", "create", "fix"}, true},
		{"maintenance", op{"task", "create", "docs"}, true},

		// sunset: creates blocked.
		{"sunset", op{"track", "create", "feature"}, false},
		{"sunset", op{"task", "create", "fix"}, false},
		{"sunset", op{"task", "create", "docs"}, false},

		// archived: all mutations blocked.
		{"archived", op{"track", "create", "feature"}, false},
		{"archived", op{"task", "create", "fix"}, false},
		{"archived", op{"task", "create", "docs"}, false},
	}

	// Load the shipped stage.yaml ruleset.
	cfg, err := policy.LoadConfig("../stage.yaml")
	require.NoError(t, err)

	for _, c := range cells {
		c := c
		name := c.stage + "_" + c.op.kind + "_" + c.op.trackType
		t.Run(name, func(t *testing.T) {
			b := bus.New()
			vpub := &recVPub{}
			eng, err := withcel.New(cfg,
				policy.WithStageResolver(func(_ string) (map[string]any, error) {
					return map[string]any{"mode": c.stage}, nil
				}),
				policy.WithViolationPublisher(vpub),
			)
			require.NoError(t, err)

			cancel := policy.Wire(b, eng)
			defer cancel()

			repo := newMemRepo()
			svc := domain.NewService[*mockEntityWithTrackType](
				&entityRepoAdapter{repo: repo},
				domain.WithPublisher[*mockEntityWithTrackType](&busPubAdapter{b: b}),
			)

			ent := &mockEntityWithTrackType{
				ID:    "ops",
				Kind:  c.op.kind,
				TType: c.op.trackType,
			}
			err = svc.Create(context.Background(), &ent)

			if c.allowed {
				assert.NoError(t, err, "expected allow for %s", name)
				assert.Equal(t, 0, vpub.count(), "no violated emits when allowed")
				return
			}

			require.Error(t, err, "expected deny for %s", name)
			assert.Equal(t, 1, vpub.count(), "exactly one violated emit on deny")
		})
	}
}

// entityRepoAdapter wraps memRepo to satisfy
// domain.Repository[*mockEntityWithTrackType].
type entityRepoAdapter struct {
	repo *memRepo
}

func (a *entityRepoAdapter) Create(ctx context.Context, e **mockEntityWithTrackType) error {
	te := trackEntity{ID: (*e).ID, Kind: (*e).Kind, TrackType: (*e).TType}
	return a.repo.Create(ctx, &te)
}

func (a *entityRepoAdapter) Get(ctx context.Context, id string) (**mockEntityWithTrackType, error) {
	te, err := a.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	out := &mockEntityWithTrackType{ID: te.ID, Kind: te.Kind, TType: te.TrackType}
	return &out, nil
}

func (a *entityRepoAdapter) List(_ context.Context, _ domain.Query) ([]*mockEntityWithTrackType, error) {
	return nil, nil
}

func (a *entityRepoAdapter) Update(ctx context.Context, e **mockEntityWithTrackType) error {
	te := trackEntity{ID: (*e).ID, Kind: (*e).Kind, TrackType: (*e).TType}
	return a.repo.Update(ctx, &te)
}

func (a *entityRepoAdapter) Delete(ctx context.Context, id string) error {
	return a.repo.Delete(ctx, id)
}
