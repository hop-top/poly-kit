package withcel_test

import (
	"errors"
	"fmt"

	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/runtime/policy"
	"hop.top/kit/go/runtime/policy/withcel"
)

const rules = `
policies:
  - name: admin-only-cancel
    on: kit.runtime.state.pre_transitioned
    when: 'payload.To != "CANCELED" || principal.role == "admin"'
    effect: allow
    otherwise: deny
    message: only admin may cancel
`

func Example() {
	cfg, err := policy.ParseConfig([]byte(rules))
	if err != nil {
		panic(err)
	}
	eng, err := withcel.New(cfg) // policy.NewEngine + policy.WithEvaluator(cel.New())
	if err != nil {
		panic(err)
	}
	// In production: policy.Wire(b, eng) subscribes eng to the bus.
	err = eng.Decide("kit.runtime.state.pre_transitioned", map[string]any{
		"principal": map[string]any{"role": "engineer"},
		"resource":  map[string]any{},
		"context":   map[string]any{},
		"payload":   map[string]any{"To": "CANCELED"},
	})
	fmt.Println(errors.Is(err, domain.ErrConflict), err)

	// Output:
	// true policy "admin-only-cancel" denied: only admin may cancel
}
