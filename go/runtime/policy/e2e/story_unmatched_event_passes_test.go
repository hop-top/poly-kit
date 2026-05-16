// Story: as a kit adopter, I want events with no matching policy to
// pass through unchanged so that adding the engine to an existing
// system doesn't break working code.
//
// Background: backwards-compatible adoption is a hard requirement —
// teams roll the engine out incrementally, starting with one or
// two policies and expanding as confidence grows. The default for
// "no matching policy on this topic" must therefore be ALLOW. A
// default-deny would force every team to author exhaustive
// allow-lists from day one.
//
// Acceptance:
//
//	Given a policy that targets ONLY pre_persisted
//	When  a pre_transitioned event fires (different topic, no match)
//	Then  the event passes — the engine returns nil
package e2e_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/runtime/policy"
	"hop.top/kit/go/runtime/policy/withcel"
)

func TestStory_UnmatchedEventPasses_StateMachineUnaffectedByEntityPolicy(t *testing.T) {
	t.Parallel()

	// Policy targets entity.pre_persisted only. State-machine
	// transitions publish on state.pre_transitioned, which has zero
	// matching policies, so the engine must default-allow.
	cfg, err := policy.LoadConfig(filepath.Join("testdata", "delete-requires-note.yaml"))
	require.NoError(t, err)
	eng, err := withcel.New(cfg, policy.WithPrincipalResolver(staticPrincipal(
		policy.Principal{ID: "alice", Role: "engineer", Source: "ctx"},
	)))
	require.NoError(t, err)

	b := bus.New()
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	t.Cleanup(policy.Wire(b, eng))

	sm := domain.NewStateMachine(map[domain.State][]domain.State{
		"OPEN": {"DONE"},
	}, &busAdapter{b: b})

	// No --note in ctx, no admin role — but the state machine's
	// topic doesn't match the delete policy, so it passes.
	require.NoError(t, sm.Transition(context.Background(), "OPEN", "DONE", false))
}
