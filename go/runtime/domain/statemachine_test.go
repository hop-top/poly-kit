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

// --- mock publisher for statemachine tests ---

type smPublisher struct {
	handler func(ctx context.Context, topic, source string, payload any) error
}

func (p *smPublisher) Publish(ctx context.Context, topic, source string, payload any) error {
	if p.handler != nil {
		return p.handler(ctx, topic, source, payload)
	}
	return nil
}

func testRules() map[domain.State][]domain.State {
	return map[domain.State][]domain.State{
		"open":        {"in_progress", "closed"},
		"in_progress": {"open", "closed"},
		"closed":      {},
	}
}

func TestStateMachine_ValidTransition(t *testing.T) {
	sm := domain.NewStateMachine(testRules(), nil)
	err := sm.Transition(context.Background(), "open", "in_progress", false)
	require.NoError(t, err)
}

func TestStateMachine_InvalidTransition(t *testing.T) {
	sm := domain.NewStateMachine(testRules(), nil)
	err := sm.Transition(context.Background(), "open", "open", false)
	assert.ErrorIs(t, err, domain.ErrInvalidTransition)

	var te *domain.TransitionError
	require.ErrorAs(t, err, &te)
	assert.Equal(t, domain.State("open"), te.From)
	assert.Equal(t, domain.State("open"), te.To)
	assert.Equal(t, []domain.State{"in_progress", "closed"}, te.Allowed)
}

func TestStateMachine_NoRulesForState(t *testing.T) {
	sm := domain.NewStateMachine(testRules(), nil)
	err := sm.Transition(context.Background(), "unknown", "open", false)
	assert.ErrorIs(t, err, domain.ErrInvalidTransition)

	var te *domain.TransitionError
	require.ErrorAs(t, err, &te)
	assert.Nil(t, te.Allowed)
}

func TestStateMachine_ClosedHasNoTransitions(t *testing.T) {
	sm := domain.NewStateMachine(testRules(), nil)
	err := sm.Transition(context.Background(), "closed", "open", false)
	assert.ErrorIs(t, err, domain.ErrInvalidTransition)
}

func TestStateMachine_ForceBypassesRules(t *testing.T) {
	sm := domain.NewStateMachine(testRules(), nil)
	err := sm.Transition(context.Background(), "closed", "open", true)
	require.NoError(t, err)
}

func TestStateMachine_NilPublisher(t *testing.T) {
	sm := domain.NewStateMachine(testRules(), nil)
	err := sm.Transition(context.Background(), "open", "closed", false)
	require.NoError(t, err)
}

func TestStateMachine_PreTransitionVeto(t *testing.T) {
	vetoErr := errors.New("vetoed")
	pub := &smPublisher{handler: func(_ context.Context, topic, _ string, _ any) error {
		if topic == "kit.runtime.state.pre_transitioned" {
			return vetoErr
		}
		return nil
	}}

	sm := domain.NewStateMachine(testRules(), pub)
	err := sm.Transition(context.Background(), "open", "in_progress", false)
	require.Error(t, err)
	assert.ErrorIs(t, err, vetoErr)
}

func TestStateMachine_PostTransitionFires(t *testing.T) {
	fired := make(chan domain.TransitionPayload, 1)
	pub := &smPublisher{handler: func(_ context.Context, topic, _ string, payload any) error {
		if topic == "kit.runtime.state.post_transitioned" {
			if p, ok := payload.(domain.TransitionPayload); ok {
				fired <- p
			}
		}
		return nil
	}}

	sm := domain.NewStateMachine(testRules(), pub)
	err := sm.Transition(context.Background(), "open", "closed", false)
	require.NoError(t, err)

	select {
	case <-fired:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for post-transition event")
	}
}

func TestStateMachine_AllowedFrom(t *testing.T) {
	sm := domain.NewStateMachine(testRules(), nil)

	got := sm.AllowedFrom("open")
	assert.Equal(t, []domain.State{"in_progress", "closed"}, got)

	got = sm.AllowedFrom("closed")
	assert.Empty(t, got)

	got = sm.AllowedFrom("nonexistent")
	assert.Nil(t, got)

	// Verify returned slice is a copy (mutating doesn't affect rules).
	orig := sm.AllowedFrom("open")
	orig[0] = "mutated"
	fresh := sm.AllowedFrom("open")
	assert.Equal(t, domain.State("in_progress"), fresh[0])
}

func TestTransitionError_Message(t *testing.T) {
	te := &domain.TransitionError{
		From:    "draft",
		To:      "published",
		Allowed: []domain.State{"review", "archived"},
	}
	assert.Contains(t, te.Error(), `"draft" → "published"`)
	assert.Contains(t, te.Error(), "review archived")

	// nil Allowed = no rules for state
	noRules := &domain.TransitionError{From: "unknown", To: "open", Allowed: nil}
	assert.Contains(t, noRules.Error(), "no rules for state")
	assert.Contains(t, noRules.Error(), `"unknown"`)
}

func TestTransitionError_AllowedIsCopy(t *testing.T) {
	sm := domain.NewStateMachine(testRules(), nil)
	err := sm.Transition(context.Background(), "open", "open", false)

	var te *domain.TransitionError
	require.ErrorAs(t, err, &te)
	te.Allowed[0] = "mutated"

	// Original rules unaffected.
	got := sm.AllowedFrom("open")
	assert.Equal(t, domain.State("in_progress"), got[0])
}
