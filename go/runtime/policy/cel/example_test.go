package cel_test

import (
	"fmt"

	"hop.top/kit/go/runtime/policy/cel"
)

func Example() {
	ev, err := cel.New()
	if err != nil {
		panic(err)
	}
	if err := ev.Compile("admin-only", `principal.role == "admin"`); err != nil {
		panic(err)
	}
	for _, role := range []string{"admin", "engineer"} {
		ok, err := ev.Eval("admin-only", map[string]any{
			"principal": map[string]any{"role": role},
			"resource":  map[string]any{},
			"context":   map[string]any{},
			"payload":   map[string]any{},
		})
		fmt.Println(role, ok, err)
	}

	// Output:
	// admin true <nil>
	// engineer false <nil>
}
