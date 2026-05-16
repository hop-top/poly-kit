package domain_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/domain"
)

func e2eRules() map[domain.State][]domain.State {
	return map[domain.State][]domain.State{
		"draft":     {"review"},
		"review":    {"approved", "draft"},
		"approved":  {"published"},
		"published": {},
	}
}

// e2ePublisher records published events for assertion.
type e2ePublisher struct {
	handler func(ctx context.Context, topic, source string, payload any) error
}

func (p *e2ePublisher) Publish(ctx context.Context, topic, source string, payload any) error {
	if p.handler != nil {
		return p.handler(ctx, topic, source, payload)
	}
	return nil
}

func TestE2E_StateMachine_ValidTransition(t *testing.T) {
	sm := domain.NewStateMachine(e2eRules(), nil)
	err := sm.Transition(context.Background(), "draft", "review", false)
	require.NoError(t, err)
}

func TestE2E_StateMachine_InvalidTransition(t *testing.T) {
	sm := domain.NewStateMachine(e2eRules(), nil)
	err := sm.Transition(context.Background(), "review", "published", false)
	assert.ErrorIs(t, err, domain.ErrInvalidTransition)
}

func TestE2E_StateMachine_SyncSubscriberVeto(t *testing.T) {
	vetoErr := errors.New("policy: blocked by reviewer")
	pub := &e2ePublisher{handler: func(_ context.Context, topic, _ string, _ any) error {
		if topic == "kit.runtime.state.pre_transitioned" {
			return vetoErr
		}
		return nil
	}}

	sm := domain.NewStateMachine(e2eRules(), pub)
	err := sm.Transition(context.Background(), "draft", "review", false)
	require.Error(t, err)
	assert.ErrorIs(t, err, vetoErr)
}

func TestE2E_StateMachine_ForceBypassesRules(t *testing.T) {
	sm := domain.NewStateMachine(e2eRules(), nil)
	// published has no allowed transitions; force bypasses
	err := sm.Transition(context.Background(), "published", "draft", true)
	require.NoError(t, err)
}

func TestE2E_StateMachine_PostTransitionAsyncEvent(t *testing.T) {
	fired := make(chan domain.TransitionPayload, 1)
	pub := &e2ePublisher{handler: func(_ context.Context, topic, _ string, payload any) error {
		if topic == "kit.runtime.state.post_transitioned" {
			if p, ok := payload.(domain.TransitionPayload); ok {
				fired <- p
			}
		}
		return nil
	}}

	sm := domain.NewStateMachine(e2eRules(), pub)
	require.NoError(t, sm.Transition(context.Background(), "draft", "review", false))

	select {
	case p := <-fired:
		assert.Equal(t, domain.State("draft"), p.From)
		assert.Equal(t, domain.State("review"), p.To)
		assert.False(t, p.Force)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for post-transition event")
	}
}

func TestE2E_StateMachine_NilPublisherTransitionsWork(t *testing.T) {
	sm := domain.NewStateMachine(e2eRules(), nil)

	require.NoError(t, sm.Transition(context.Background(), "draft", "review", false))
	require.NoError(t, sm.Transition(context.Background(), "review", "approved", false))
	require.NoError(t, sm.Transition(context.Background(), "approved", "published", false))
}
