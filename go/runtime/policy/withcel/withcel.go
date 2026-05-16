// Package withcel constructs a policy.Engine wired to the CEL
// evaluator backend. It exists as a separate package so the parent
// policy package's transitive imports stay free of github.com/google/
// cel-go: adopters wiring Cedar/OPA/etc. import policy directly and
// never reach this subtree.
//
// Usage:
//
//	cfg, _ := policy.LoadConfig("policies.yaml")
//	eng, _ := withcel.New(cfg)
//	policy.Wire(b, eng)
package withcel

import (
	"fmt"

	"hop.top/kit/go/runtime/policy"
	"hop.top/kit/go/runtime/policy/cel"
)

// New constructs a policy.Engine using the CEL evaluator. Equivalent
// to policy.NewEngine(cfg, policy.WithEvaluator(<cel evaluator>),
// opts...). Use policy.NewEngine + policy.WithEvaluator directly to
// plug a different backend (Cedar, OPA, etc.).
func New(cfg *policy.Config, opts ...policy.EngineOption) (*policy.Engine, error) {
	ev, err := cel.New()
	if err != nil {
		return nil, fmt.Errorf("policy/withcel: %w", err)
	}
	all := append([]policy.EngineOption{policy.WithEvaluator(ev)}, opts...)
	return policy.NewEngine(cfg, all...)
}
