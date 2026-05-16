// Story: as a kit adopter using OPA / Cedar / a hand-rolled DSL, I
// want to plug in my own Evaluator so I'm not forced to take
// cel-go as a transitive dependency.
//
// Background: the core policy package is evaluator-agnostic. The
// CEL backend lives in policy/cel and is opt-in via withcel.New.
// Adopters with existing policy infrastructure (OPA Rego, AWS
// Cedar, internal DSLs) supply their own Evaluator via
// policy.WithEvaluator. This story proves the surface works end-
// to-end without ever importing the CEL package.
//
// Acceptance:
//
//	Given an in-test fakeEvaluator that satisfies policy.Evaluator
//	When  policy.NewEngine is called WITHOUT withcel.New
//	Then  the engine compiles and decides via the fake evaluator
//	      and a denying expression vetoes a transition end-to-end
package e2e_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/runtime/policy"
)

// fakeEvaluator is a deliberately tiny Evaluator implementation —
// just enough to demonstrate the surface. It accepts two pseudo-
// expressions:
//
//   - "always" → true
//   - "role-is:<value>" → principal.role == <value>
//
// Real adopters wire their own engine here (OPA, Cedar, etc.).
type fakeEvaluator struct {
	mu    sync.Mutex
	exprs map[string]string // policy name → raw expression
}

func newFakeEvaluator() *fakeEvaluator {
	return &fakeEvaluator{exprs: map[string]string{}}
}

func (f *fakeEvaluator) Compile(name, expr string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exprs[name] = expr
	return nil
}

func (f *fakeEvaluator) Eval(name string, activation map[string]any) (bool, error) {
	f.mu.Lock()
	expr := f.exprs[name]
	f.mu.Unlock()

	switch {
	case expr == "always":
		return true, nil
	case strings.HasPrefix(expr, "role-is:"):
		want := strings.TrimPrefix(expr, "role-is:")
		princ, _ := activation["principal"].(map[string]any)
		role, _ := princ["role"].(string)
		return role == want, nil
	default:
		return false, nil
	}
}

func TestStory_SwapEvaluatorToCustom_DeniesViaFake(t *testing.T) {
	t.Parallel()

	// Build a Config in-process — no YAML and no CEL syntax. The
	// `when` strings are inputs to fakeEvaluator's mini-DSL.
	cfg := &policy.Config{
		Policies: []policy.Policy{{
			Name:      "fake-admin-only",
			On:        "kit.runtime.state.pre_transitioned",
			When:      "role-is:admin",
			Effect:    policy.EffectAllow,
			Otherwise: policy.EffectDeny,
			Message:   "fake evaluator: only admin allowed",
		}},
	}

	// NewEngine, NOT withcel.New — proves zero CEL coupling at the
	// call site for adopters who supply their own evaluator.
	eng, err := policy.NewEngine(cfg,
		policy.WithEvaluator(newFakeEvaluator()),
		policy.WithPrincipalResolver(staticPrincipal(
			policy.Principal{ID: "alice", Role: "engineer", Source: "ctx"},
		)),
	)
	require.NoError(t, err)

	b := bus.New()
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	t.Cleanup(policy.Wire(b, eng))

	sm := domain.NewStateMachine(map[domain.State][]domain.State{
		"OPEN": {"CANCELED"},
	}, &busAdapter{b: b})

	err = sm.Transition(context.Background(), "OPEN", "CANCELED", false)
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrConflict))
	assert.Contains(t, err.Error(), "fake evaluator: only admin allowed")
}

func TestStory_SwapEvaluatorToCustom_AllowsViaFake(t *testing.T) {
	t.Parallel()

	cfg := &policy.Config{
		Policies: []policy.Policy{{
			Name:      "fake-admin-only",
			On:        "kit.runtime.state.pre_transitioned",
			When:      "role-is:admin",
			Effect:    policy.EffectAllow,
			Otherwise: policy.EffectDeny,
		}},
	}

	eng, err := policy.NewEngine(cfg,
		policy.WithEvaluator(newFakeEvaluator()),
		policy.WithPrincipalResolver(staticPrincipal(
			policy.Principal{ID: "ops", Role: "admin", Source: "ctx"},
		)),
	)
	require.NoError(t, err)

	b := bus.New()
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	t.Cleanup(policy.Wire(b, eng))

	sm := domain.NewStateMachine(map[domain.State][]domain.State{
		"OPEN": {"CANCELED"},
	}, &busAdapter{b: b})

	require.NoError(t, sm.Transition(context.Background(), "OPEN", "CANCELED", false))
}
