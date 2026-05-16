// Story: as a kit adopter, I want a one-line CEL constructor
// (withcel.New) so I get a working policy engine without ceremony.
//
// Background: this is the adoption-quickstart. Reading this test
// should leave a new adopter able to copy ~10 lines into their own
// program and have a working guard engine. Everything else in this
// directory builds on this baseline.
//
// Acceptance:
//
//	Given a single-policy YAML and a real bus
//	When  withcel.New(cfg) is called and policy.Wire attaches the engine
//	Then  a state-machine transition that violates the policy is vetoed
//	      and the error wraps domain.ErrConflict
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

func TestStory_UseCELDefault_MinimalWiring(t *testing.T) {
	t.Parallel()

	// 1. Load policy config from YAML.
	cfg, err := policy.LoadConfig(filepath.Join("testdata", "admin-only-cancel.yaml"))
	require.NoError(t, err)

	// 2. Build the engine using the CEL backend (one-liner).
	eng, err := withcel.New(cfg, policy.WithPrincipalResolver(staticPrincipal(
		policy.Principal{ID: "u1", Role: "engineer", Source: "ctx"},
	)))
	require.NoError(t, err)

	// 3. Wire the engine to a real bus.
	b := bus.New()
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	t.Cleanup(policy.Wire(b, eng))

	// 4. Build a real state machine that publishes pre-events to
	//    the bus. The engine subscribes and vetoes by returning a
	//    PolicyDeniedError, which the bus surfaces as the Publish
	//    return value, which the SM wraps and returns to us.
	sm := domain.NewStateMachine(map[domain.State][]domain.State{
		"OPEN": {"CANCELED", "DONE"},
	}, &busAdapter{b: b})

	// 5. An engineer trying to CANCEL is rejected.
	err = sm.Transition(context.Background(), "OPEN", "CANCELED", false)
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrConflict),
		"PolicyDeniedError must unwrap to domain.ErrConflict")
	assert.Contains(t, err.Error(), "only admin or orchestrator")
}
