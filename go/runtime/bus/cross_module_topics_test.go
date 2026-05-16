package bus_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/core/breaker"
	"hop.top/kit/go/core/upgrade"
	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/domain"
)

// busPublisher adapts bus.Bus to the 3-arg domain.EventPublisher /
// transport-style EventPublisher interface used by Service[T],
// StateMachine, breaker, and upgrade. One adapter, all consumers.
type busPublisher struct{ b bus.Bus }

func (p *busPublisher) Publish(ctx context.Context, topic, source string, payload any) error {
	return p.b.Publish(ctx, bus.NewEvent(bus.Topic(topic), source, payload))
}

// crossModEntity is a tiny entity for Service[T] in the smoke test.
type crossModEntity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (e crossModEntity) GetID() string { return e.ID }

// crossModRepo is an in-memory Repository[crossModEntity].
type crossModRepo struct {
	mu   sync.RWMutex
	data map[string]*crossModEntity
}

func newCrossModRepo() *crossModRepo {
	return &crossModRepo{data: make(map[string]*crossModEntity)}
}

func (r *crossModRepo) Create(_ context.Context, e *crossModEntity) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[e.GetID()]; ok {
		return domain.ErrConflict
	}
	r.data[e.GetID()] = e
	return nil
}
func (r *crossModRepo) Get(_ context.Context, id string) (*crossModEntity, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.data[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return e, nil
}
func (r *crossModRepo) List(_ context.Context, _ domain.Query) ([]crossModEntity, error) {
	return nil, nil
}
func (r *crossModRepo) Update(_ context.Context, e *crossModEntity) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[e.GetID()]; !ok {
		return domain.ErrNotFound
	}
	r.data[e.GetID()] = e
	return nil
}
func (r *crossModRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, id)
	return nil
}

// stubProvider is a minimal llm.Provider+Completer for the llm.Client
// happy-path smoke. Returns a fixed Response so Complete fires
// RequestStart + RequestEnd on the bus.
type stubProvider struct{}

func (stubProvider) Close() error { return nil }
func (stubProvider) Complete(_ context.Context, _ llm.Request) (llm.Response, error) {
	return llm.Response{Content: "ok"}, nil
}

// recorder collects topic strings observed under a wildcard pattern.
type recorder struct {
	mu     sync.Mutex
	topics []string
}

func (r *recorder) record(topic string) {
	r.mu.Lock()
	r.topics = append(r.topics, topic)
	r.mu.Unlock()
}

func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.topics))
	copy(out, r.topics)
	return out
}

// TestCrossModule_TopicsSmoke wires Service[T], StateMachine,
// llm.Client, core/breaker, and core/upgrade onto a single in-memory
// bus, applies WithTopicPrefix variants on each, and asserts the
// adopter-prefixed topics actually reach subscribers.
//
// One bus, five emitters, one assertion pass per emitter — verifies
// the contract that adopters can rebrand without forking, and that
// every emitter routes its events through the same bus surface.
func TestCrossModule_TopicsSmoke(t *testing.T) {
	ctx := context.Background()
	b := bus.New()
	t.Cleanup(func() { _ = b.Close(ctx) })

	rec := &recorder{}
	// One subscription per adopter prefix; each filters down to its
	// own namespace via wildcard.
	patterns := []string{
		"myapp.runtime.foo.*",
		"myapp.task.state.*",
		"myapp.ai.#",
		"myapp.core.breaker.*",
		"myapp.core.upgrade.*",
	}
	for _, p := range patterns {
		b.Subscribe(p, func(_ context.Context, e bus.Event) error {
			rec.record(string(e.Topic))
			return nil
		})
	}

	pub := &busPublisher{b: b}

	// --- Service[crossModEntity] with custom prefix --------------------
	repo := newCrossModRepo()
	svc := domain.NewService[crossModEntity](repo,
		domain.WithPublisher[crossModEntity](pub),
		domain.WithTopicPrefix[crossModEntity]("myapp.runtime.foo"),
	)
	require.NoError(t, svc.Create(ctx, &crossModEntity{ID: "f1", Name: "n1"}))
	require.NoError(t, svc.Update(ctx, &crossModEntity{ID: "f1", Name: "n2"}))
	require.NoError(t, svc.Delete(ctx, "f1"))

	// --- StateMachine with custom prefix -------------------------------
	rules := map[domain.State][]domain.State{"open": {"closed"}}
	sm := domain.NewStateMachine(rules, pub,
		domain.WithSMTopicPrefix("myapp.task.state"),
	)
	require.NoError(t, sm.Transition(ctx, "open", "closed", false))

	// --- llm.Client with custom prefix ---------------------------------
	c := llm.NewClient(stubProvider{},
		llm.WithBus(b),
		llm.WithTopicPrefix("myapp.ai"),
	)
	_, err := c.Complete(ctx, llm.Request{})
	require.NoError(t, err)

	// --- breaker with custom prefix ------------------------------------
	br := breaker.New("smoke-breaker",
		breaker.WithPublisher(pub),
		breaker.WithTopicPrefix("myapp.core.breaker"),
	)
	t.Cleanup(func() { breaker.Unregister("smoke-breaker") })
	br.Trip("smoke-test")

	// --- upgrade with custom prefix ------------------------------------
	uc := upgrade.New(
		upgrade.WithBinary("smoke", "0.0.0"),
		upgrade.WithStateDir(t.TempDir()),
		upgrade.WithSnoozeDuration(time.Hour),
		upgrade.WithPublisher(pub),
		upgrade.WithTopicPrefix("myapp.core.upgrade"),
	)
	require.NoError(t, uc.Snooze())

	// Give async dispatchers + post-transition goroutines a moment.
	require.Eventually(t, func() bool {
		got := rec.snapshot()
		return contains(got, "myapp.runtime.foo.created") &&
			contains(got, "myapp.runtime.foo.updated") &&
			contains(got, "myapp.runtime.foo.deleted") &&
			contains(got, "myapp.task.state.pre_transitioned") &&
			contains(got, "myapp.task.state.post_transitioned") &&
			contains(got, "myapp.ai.request.started") &&
			contains(got, "myapp.ai.response.received") &&
			contains(got, "myapp.core.breaker.tripped") &&
			contains(got, "myapp.core.upgrade.snoozed")
	}, 2*time.Second, 20*time.Millisecond, "expected topics not all observed: %v", rec.snapshot())

	got := rec.snapshot()

	// Service[T] — 3 CRUD events under the custom prefix.
	assert.Contains(t, got, "myapp.runtime.foo.created")
	assert.Contains(t, got, "myapp.runtime.foo.updated")
	assert.Contains(t, got, "myapp.runtime.foo.deleted")

	// StateMachine — pre + post under the custom prefix.
	assert.Contains(t, got, "myapp.task.state.pre_transitioned")
	assert.Contains(t, got, "myapp.task.state.post_transitioned")

	// llm.Client — both happy-path topics under the rebrand.
	// (request.errored, fallback.applied, route.selected, eva.evaluated
	// require failures / fallbacks / routing / eva contracts to fire
	// — out of scope for the cross-module smoke; resolveTopics covers
	// the prefix construction for all 6.)
	assert.Contains(t, got, "myapp.ai.request.started")
	assert.Contains(t, got, "myapp.ai.response.received")

	// All 6 llm topics carry the configured prefix even though only 2
	// fired in this happy path.
	tops := c.Topics()
	assert.Equal(t, bus.Topic("myapp.ai.request.started"), tops.RequestStart)
	assert.Equal(t, bus.Topic("myapp.ai.response.received"), tops.RequestEnd)
	assert.Equal(t, bus.Topic("myapp.ai.request.errored"), tops.RequestError)
	assert.Equal(t, bus.Topic("myapp.ai.fallback.applied"), tops.Fallback)
	assert.Equal(t, bus.Topic("myapp.ai.route.selected"), tops.Route)
	assert.Equal(t, bus.Topic("myapp.ai.eva.evaluated"), tops.EvaResult)

	// breaker — manual Trip() under the custom prefix.
	assert.Contains(t, got, "myapp.core.breaker.tripped")

	// upgrade — Snooze under the custom prefix.
	assert.Contains(t, got, "myapp.core.upgrade.snoozed")

	// Negative: no kit-default topics leaked through the custom-prefixed
	// emitters' subscriptions.
	for _, leak := range []string{
		"kit.runtime.entity.created",
		"kit.runtime.state.pre_transitioned",
		"kit.ai.request.started",
		"kit.core.breaker.tripped",
		"kit.core.upgrade.snoozed",
	} {
		assert.NotContains(t, got, leak)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
