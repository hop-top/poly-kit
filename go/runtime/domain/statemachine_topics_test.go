package domain_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/domain"
)

// recordingPublisher captures every (topic, source, payload) tuple it
// observes. Safe for concurrent use because StateMachine fires the
// post-transition event from a goroutine.
type recordingPublisher struct {
	mu     sync.Mutex
	events []recordedEvent
}

type recordedEvent struct {
	Topic   string
	Source  string
	Payload any
}

func (p *recordingPublisher) Publish(_ context.Context, topic, source string, payload any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, recordedEvent{Topic: topic, Source: source, Payload: payload})
	return nil
}

func (p *recordingPublisher) topics() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.events))
	for i, e := range p.events {
		out[i] = e.Topic
	}
	return out
}

func smRules() map[domain.State][]domain.State {
	return map[domain.State][]domain.State{
		"open":   {"closed"},
		"closed": {},
	}
}

// awaitTopic blocks until the publisher records a matching topic or
// the deadline elapses. Necessary because the post-transition event is
// fire-and-forget from a goroutine.
func awaitTopic(t *testing.T, p *recordingPublisher, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, got := range p.topics() {
			if got == want {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for topic %q; saw %v", want, p.topics())
}

func TestDefaultStateMachineTopics(t *testing.T) {
	assert.Equal(t,
		bus.Topic("kit.runtime.state.pre_transitioned"),
		domain.DefaultStateMachineTopics.PreTransitioned,
	)
	assert.Equal(t,
		bus.Topic("kit.runtime.state.post_transitioned"),
		domain.DefaultStateMachineTopics.PostTransitioned,
	)
}

func TestNewStateMachine_DefaultsTopics(t *testing.T) {
	pub := &recordingPublisher{}
	sm := domain.NewStateMachine(smRules(), pub)

	require.NoError(t, sm.Transition(context.Background(), "open", "closed", false))
	awaitTopic(t, pub, "kit.runtime.state.post_transitioned")

	got := pub.topics()
	assert.Contains(t, got, "kit.runtime.state.pre_transitioned")
	assert.Contains(t, got, "kit.runtime.state.post_transitioned")
}

func TestWithSMTopicPrefix_OverridesBoth(t *testing.T) {
	pub := &recordingPublisher{}
	sm := domain.NewStateMachine(smRules(), pub,
		domain.WithSMTopicPrefix("myapp.task.state"),
	)

	require.NoError(t, sm.Transition(context.Background(), "open", "closed", false))
	awaitTopic(t, pub, "myapp.task.state.post_transitioned")

	got := pub.topics()
	assert.Contains(t, got, "myapp.task.state.pre_transitioned")
	assert.Contains(t, got, "myapp.task.state.post_transitioned")
	for _, topic := range got {
		assert.NotContains(t, topic, "kit.runtime.state",
			"prefix override must replace defaults entirely")
	}
}

func TestWithSMTopics_PartialOverrideKeepsDefaults(t *testing.T) {
	pub := &recordingPublisher{}
	sm := domain.NewStateMachine(smRules(), pub,
		domain.WithSMTopics(domain.StateMachineTopics{
			PreTransitioned: "x.y.z.pre_transitioned",
		}),
	)

	require.NoError(t, sm.Transition(context.Background(), "open", "closed", false))
	awaitTopic(t, pub, "kit.runtime.state.post_transitioned")

	got := pub.topics()
	assert.Contains(t, got, "x.y.z.pre_transitioned",
		"Pre override should be honored")
	assert.Contains(t, got, "kit.runtime.state.post_transitioned",
		"empty Post field should fall back to default")
}

func TestWithSMTopics_FullOverride(t *testing.T) {
	pub := &recordingPublisher{}
	sm := domain.NewStateMachine(smRules(), pub,
		domain.WithSMTopics(domain.StateMachineTopics{
			PreTransitioned:  "wsm.runtime.workspace.pre_transitioned",
			PostTransitioned: "wsm.runtime.workspace.post_transitioned",
		}),
	)

	require.NoError(t, sm.Transition(context.Background(), "open", "closed", false))
	awaitTopic(t, pub, "wsm.runtime.workspace.post_transitioned")

	got := pub.topics()
	assert.Contains(t, got, "wsm.runtime.workspace.pre_transitioned")
	assert.Contains(t, got, "wsm.runtime.workspace.post_transitioned")
}

func TestWithSMTopicPrefix_InvalidPanics(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
	}{
		{"empty", ""},
		{"too few segments", "myapp.state"},
		{"too many segments", "a.b.c.d"},
		{"trailing dot", "myapp.task.state."},
		{"uppercase", "MyApp.task.state"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			assert.Panics(t, func() {
				_ = domain.WithSMTopicPrefix(tc.prefix)
			})
		})
	}
}

// TestJobEngineInheritsCustomTopics proves that any consumer wiring a
// *domain.StateMachine — including the 5 job engines (hatchet,
// durabletask, temporal, mock, restate) which all share the same
// constructor — gets topic customization for free. We exercise the
// pending→active transition used by mock.Engine.Claim under a custom
// prefix and assert the prefixed topics fire.
func TestJobEngineInheritsCustomTopics(t *testing.T) {
	// Mirrors job.transitionRules (pending → active is the path
	// exercised by every engine's Claim).
	rules := map[domain.State][]domain.State{
		"pending": {"active", "canceled"},
		"active":  {"succeeded", "failed", "timeout", "pending", "canceled"},
	}

	pub := &recordingPublisher{}
	sm := domain.NewStateMachine(rules, pub,
		domain.WithSMTopicPrefix("hop.runtime.job"),
	)

	require.NoError(t, sm.Transition(context.Background(), "pending", "active", false))
	awaitTopic(t, pub, "hop.runtime.job.post_transitioned")

	got := pub.topics()
	assert.Contains(t, got, "hop.runtime.job.pre_transitioned")
	assert.Contains(t, got, "hop.runtime.job.post_transitioned")
}
