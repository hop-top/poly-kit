// Story: as an ops lead, I want `force=true` on a state transition
// to NOT bypass policy by default, but to be available as
// `payload.Force` inside CEL expressions so that admins can write
// per-policy overrides explicitly.
//
// Background: domain.StateMachine has a `force` argument that skips
// the (from, to) rules check — useful for recovery scenarios. But a
// blanket bypass of policy would defeat the audit guarantees the
// engine exists to enforce. Instead, force=true is just a payload
// field; a policy chooses whether (and for whom) to honor it.
//
// Acceptance:
//
//	Given a policy bundle with admin-only-cancel + a force-override
//	When  a non-admin transitions with force=true
//	Then  the transition is denied (the override only fires for admins)
//	When  an admin transitions with force=true
//	Then  the transition succeeds (the override clears the deny)
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

func TestStory_ForceDoesNotBypass_NonAdminStillDenied(t *testing.T) {
	t.Parallel()

	cfg, err := policy.LoadConfig(filepath.Join("testdata", "force-not-bypass.yaml"))
	require.NoError(t, err)
	eng, err := withcel.New(cfg, policy.WithPrincipalResolver(staticPrincipal(
		policy.Principal{ID: "alice", Role: "engineer", Source: "ctx"},
	)))
	require.NoError(t, err)

	b := bus.New()
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	t.Cleanup(policy.Wire(b, eng))

	// rules left empty so force=true is genuinely needed to skip
	// the rule check — proves we're hitting the policy layer, not
	// a rules-check error.
	sm := domain.NewStateMachine(map[domain.State][]domain.State{}, &busAdapter{b: b})

	err = sm.Transition(context.Background(), "OPEN", "CANCELED", true)
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrConflict),
		"force=true must NOT bypass policy for non-admins")
	assert.Contains(t, err.Error(), "only admin or orchestrator")
}

func TestStory_ForceDoesNotBypass_AdminWithForceAllowed(t *testing.T) {
	t.Parallel()

	cfg, err := policy.LoadConfig(filepath.Join("testdata", "force-not-bypass.yaml"))
	require.NoError(t, err)
	eng, err := withcel.New(cfg, policy.WithPrincipalResolver(staticPrincipal(
		policy.Principal{ID: "ops", Role: "admin", Source: "ctx"},
	)))
	require.NoError(t, err)

	b := bus.New()
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	t.Cleanup(policy.Wire(b, eng))

	sm := domain.NewStateMachine(map[domain.State][]domain.State{
		"OPEN": {"CANCELED"},
	}, &busAdapter{b: b})

	require.NoError(t, sm.Transition(context.Background(), "OPEN", "CANCELED", true))
}
