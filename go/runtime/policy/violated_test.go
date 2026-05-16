package policy_test

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

// recVPub captures kit.runtime.stage.violated events.
type recVPub struct {
	mu     sync.Mutex
	events []recVEvent
}

type recVEvent struct {
	Topic   string
	Payload any
}

func (r *recVPub) Publish(topic string, payload any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, recVEvent{Topic: topic, Payload: payload})
}

func (r *recVPub) snapshot() []recVEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recVEvent, len(r.events))
	copy(out, r.events)
	return out
}

// staticStage returns a fixed stage map for any scope.
func staticStage(mode string) policy.StageResolver {
	return func(_ string) (map[string]any, error) {
		return map[string]any{
			"mode":   mode,
			"since":  "",
			"until":  nil,
			"reason": "",
			"allow":  []string{},
			"deny":   []string{},
		}, nil
	}
}

func TestDecide_StageDrivenDenyEmitsViolated(t *testing.T) {
	cfg, err := policy.ParseConfig([]byte(`policies:
  - name: archived-blocks-mutations
    on: kit.runtime.entity.pre_validated
    when: 'stage.mode == "archived" && entity.op == "create"'
    effect: deny
    otherwise: allow
    message: "scope archived; mutations denied"
`))
	require.NoError(t, err)

	pub := &recVPub{}
	eng, err := withcel.New(cfg,
		policy.WithStageResolver(staticStage("archived")),
		policy.WithViolationPublisher(pub),
	)
	require.NoError(t, err)

	// Build the activation a real subscriber would produce.
	activation := map[string]any{
		"principal": map[string]any{"id": "alice", "role": "user"},
		"resource":  map[string]any{"id": "ops", "kind": "track", "fields": map[string]any{}},
		"entity":    map[string]any{"kind": "track", "op": "create", "track_type": "feature"},
		"context":   map[string]any{},
		"payload":   map[string]any{"scope": "ops"},
	}

	err = eng.Decide("kit.runtime.entity.pre_validated", activation)
	require.Error(t, err, "stage-driven rule must deny")

	evs := pub.snapshot()
	require.Len(t, evs, 1, "expected exactly one violated emit")
	assert.Equal(t, "kit.runtime.stage.violated", evs[0].Topic)

	vp, ok := evs[0].Payload.(policy.ViolationPayload)
	require.True(t, ok, "expected policy.ViolationPayload, got %T", evs[0].Payload)
	assert.Equal(t, "ops", vp.Scope)
	assert.Equal(t, "archived", vp.Stage)
	assert.Equal(t, "kit.runtime.entity.pre_validated", vp.Topic)
	assert.Equal(t, "track", vp.Entity)
	assert.Equal(t, "alice", vp.Principal)
	assert.Equal(t, "scope archived; mutations denied", vp.Message)
}

func TestDecide_NonStageDeny_NoViolatedEmit(t *testing.T) {
	cfg, err := policy.ParseConfig([]byte(`policies:
  - name: admin-only
    on: kit.runtime.entity.pre_validated
    when: 'principal.role == "admin"'
    effect: allow
    otherwise: deny
    message: "admin only"
`))
	require.NoError(t, err)

	pub := &recVPub{}
	eng, err := withcel.New(cfg,
		policy.WithStageResolver(staticStage("active")),
		policy.WithViolationPublisher(pub),
	)
	require.NoError(t, err)

	activation := map[string]any{
		"principal": map[string]any{"id": "alice", "role": "user"},
		"resource":  map[string]any{"id": "ops", "kind": "track"},
		"entity":    map[string]any{"kind": "track", "op": "create"},
		"context":   map[string]any{},
		"payload":   map[string]any{"scope": "ops"},
	}

	err = eng.Decide("kit.runtime.entity.pre_validated", activation)
	require.Error(t, err, "non-stage rule denies via Otherwise")

	assert.Empty(t, pub.snapshot(), "non-stage deny must NOT emit violated")
}

func TestDecide_NilViolationPublisher_PreservesBehavior(t *testing.T) {
	cfg, err := policy.ParseConfig([]byte(`policies:
  - name: archived-blocks
    on: kit.runtime.entity.pre_validated
    when: 'stage.mode == "archived"'
    effect: deny
    otherwise: allow
    message: "archived"
`))
	require.NoError(t, err)

	eng, err := withcel.New(cfg, policy.WithStageResolver(staticStage("archived")))
	require.NoError(t, err)

	activation := map[string]any{
		"principal": map[string]any{},
		"resource":  map[string]any{"id": "ops"},
		"entity":    map[string]any{},
		"context":   map[string]any{},
		"payload":   map[string]any{"scope": "ops"},
	}

	err = eng.Decide("kit.runtime.entity.pre_validated", activation)
	assert.Error(t, err, "deny still surfaces via PolicyDeniedError when no publisher")
}

func TestDecide_StageBindingPopulated(t *testing.T) {
	// Verify the stage binding is populated from the resolver and
	// reaches CEL — the deny only fires if `stage.mode` is "archived".
	cfg, err := policy.ParseConfig([]byte(`policies:
  - name: archived-blocks
    on: kit.runtime.entity.pre_validated
    when: 'stage.mode == "archived"'
    effect: deny
    otherwise: allow
    message: "archived"
`))
	require.NoError(t, err)

	// Resolver returns "active" — deny should NOT fire.
	eng, err := withcel.New(cfg, policy.WithStageResolver(staticStage("active")))
	require.NoError(t, err)

	activation := map[string]any{
		"principal": map[string]any{},
		"resource":  map[string]any{"id": "ops"},
		"entity":    map[string]any{},
		"context":   map[string]any{},
		"payload":   map[string]any{"scope": "ops"},
	}

	err = eng.Decide("kit.runtime.entity.pre_validated", activation)
	assert.NoError(t, err, "stage=active should not match the archived rule")
}

func TestDecide_NoStageResolver_StageBindingEmpty(t *testing.T) {
	cfg, err := policy.ParseConfig([]byte(`policies:
  - name: archived-blocks
    on: kit.runtime.entity.pre_validated
    when: 'has(stage.mode) && stage.mode == "archived"'
    effect: deny
    otherwise: allow
    message: "archived"
`))
	require.NoError(t, err)

	// No resolver — stage is an empty map; rule should not fire.
	eng, err := withcel.New(cfg)
	require.NoError(t, err)

	activation := map[string]any{
		"principal": map[string]any{},
		"resource":  map[string]any{"id": "ops"},
		"entity":    map[string]any{},
		"context":   map[string]any{},
		"payload":   map[string]any{"scope": "ops"},
	}

	err = eng.Decide("kit.runtime.entity.pre_validated", activation)
	assert.NoError(t, err, "no resolver = empty stage map = no match")
}

// TestDecide_NonStageGatedTopic_NoEmit guards the topic gate: even a
// stage-driven policy whose On is NOT one of the three stage-gated
// pre-* seams must not emit (defensive — should never happen because
// allowedTopics also rejects, but keep the guard explicit).
func TestDecide_StageGatedTopicGuard(t *testing.T) {
	// Use one of the allowed pre-* seams but flip it to test the gate.
	cfg, err := policy.ParseConfig([]byte(`policies:
  - name: archived-blocks-on-pre-persisted
    on: kit.runtime.entity.pre_persisted
    when: 'stage.mode == "archived"'
    effect: deny
    otherwise: allow
    message: "archived"
`))
	require.NoError(t, err)

	pub := &recVPub{}
	eng, err := withcel.New(cfg,
		policy.WithStageResolver(staticStage("archived")),
		policy.WithViolationPublisher(pub),
	)
	require.NoError(t, err)

	activation := map[string]any{
		"principal": map[string]any{},
		"resource":  map[string]any{"id": "ops"},
		"entity":    map[string]any{},
		"context":   map[string]any{},
		"payload":   map[string]any{"scope": "ops"},
	}
	err = eng.Decide("kit.runtime.entity.pre_persisted", activation)
	require.Error(t, err)
	require.Len(t, pub.snapshot(), 1, "pre_persisted is stage-gated; emit expected")
}

// TestSubscriberWiring_EndToEndViolated runs the full path: domain
// pre-event → policy subscriber → engine → violated emit.
func TestSubscriberWiring_EndToEndViolated(t *testing.T) {
	cfg, err := policy.ParseConfig([]byte(`policies:
  - name: archived-blocks-create
    on: kit.runtime.entity.pre_validated
    when: 'stage.mode == "archived" && entity.op == "create"'
    effect: deny
    otherwise: allow
    message: "archived"
`))
	require.NoError(t, err)

	pub := &recVPub{}
	eng, err := withcel.New(cfg,
		policy.WithStageResolver(staticStage("archived")),
		policy.WithViolationPublisher(pub),
	)
	require.NoError(t, err)

	b := bus.New()
	cancel := policy.Wire(b, eng)
	defer cancel()

	pload := domain.PreEntityPayload{
		Op:       domain.OpCreate,
		Phase:    domain.PhasePreValidated,
		EntityID: "ops",
		Entity:   nil,
	}
	err = b.Publish(context.Background(), bus.NewEvent(
		"kit.runtime.entity.pre_validated", "domain.service", pload,
	))
	require.Error(t, err, "subscriber must veto on stage-driven deny")

	evs := pub.snapshot()
	require.Len(t, evs, 1)
	assert.Equal(t, "kit.runtime.stage.violated", evs[0].Topic)
}
