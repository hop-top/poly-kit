// Story: as an ops lead, I want only admin or orchestrator roles to
// be able to transition a task to CANCELED so that line engineers
// can't accidentally end work in progress.
//
// Background: canceling work mid-flight is destructive in the same
// way a delete is — it forfeits accumulated state and notifies
// downstream consumers that the task ended without a resolution. We
// want the system to push back when an engineer fat-fingers a cancel.
//
// Acceptance:
//
//	Given a policy gating CANCELED transitions to admin/orchestrator
//	When  a principal with role=engineer attempts the transition
//	Then  the transition is denied with a clear message
//	When  a principal with role=admin attempts the same transition
//	Then  the transition succeeds
package e2e_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/runtime/policy"
	"hop.top/kit/go/runtime/policy/withcel"
)

func TestStory_AdminOnlyCancel_DeniesNonAdmin(t *testing.T) {
	t.Parallel()

	cfg, err := policy.LoadConfig(filepath.Join("testdata", "admin-only-cancel.yaml"))
	require.NoError(t, err)
	eng, err := withcel.New(cfg, policy.WithPrincipalResolver(staticPrincipal(
		policy.Principal{ID: "alice", Role: "engineer", Source: "ctx"},
	)))
	require.NoError(t, err)

	b := bus.New()
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	t.Cleanup(policy.Wire(b, eng))

	sm := domain.NewStateMachine(map[domain.State][]domain.State{
		"OPEN": {"CANCELED", "DONE"},
	}, &busAdapter{b: b})

	err = sm.Transition(context.Background(), "OPEN", "CANCELED", false)
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrConflict))
	assert.Contains(t, err.Error(), "only admin or orchestrator")

	var pde *policy.PolicyDeniedError
	require.True(t, errors.As(err, &pde))
	assert.Equal(t, "admin-only-cancel", pde.PolicyName)
	assert.Equal(t, "kit.runtime.state.pre_transitioned", pde.Topic)
}

func TestStory_AdminOnlyCancel_AllowsAdmin(t *testing.T) {
	t.Parallel()

	cfg, err := policy.LoadConfig(filepath.Join("testdata", "admin-only-cancel.yaml"))
	require.NoError(t, err)
	eng, err := withcel.New(cfg, policy.WithPrincipalResolver(staticPrincipal(
		policy.Principal{ID: "ops", Role: "admin", Source: "ctx"},
	)))
	require.NoError(t, err)

	b := bus.New()
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	t.Cleanup(policy.Wire(b, eng))

	sm := domain.NewStateMachine(map[domain.State][]domain.State{
		"OPEN": {"CANCELED", "DONE"},
	}, &busAdapter{b: b})

	require.NoError(t, sm.Transition(context.Background(), "OPEN", "CANCELED", false))
}

func TestStory_AdminOnlyCancel_AllowsOrchestrator(t *testing.T) {
	t.Parallel()

	cfg, err := policy.LoadConfig(filepath.Join("testdata", "admin-only-cancel.yaml"))
	require.NoError(t, err)
	eng, err := withcel.New(cfg, policy.WithPrincipalResolver(staticPrincipal(
		policy.Principal{ID: "scheduler", Role: "orchestrator", Source: "ctx"},
	)))
	require.NoError(t, err)

	b := bus.New()
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	t.Cleanup(policy.Wire(b, eng))

	sm := domain.NewStateMachine(map[domain.State][]domain.State{
		"OPEN": {"CANCELED", "DONE"},
	}, &busAdapter{b: b})

	require.NoError(t, sm.Transition(context.Background(), "OPEN", "CANCELED", false))
}
