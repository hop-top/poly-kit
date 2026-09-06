package policy_test

import (
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/failsafe-go/failsafe-go"

	bpolicy "hop.top/kit/go/core/breaker/policy"
)

// The policy reads the counter through Reader on every PreExecute.
func ExampleNewCount() {
	var ops atomic.Int64
	p := bpolicy.NewCount[any]().
		WithMaxOps(2).
		WithReader(ops.Load).
		Build()
	exec := failsafe.With[any](p)

	for i := 0; i < 3; i++ {
		err := exec.Run(func() error { ops.Add(1); return nil })
		fmt.Println(i, errors.Is(err, bpolicy.ErrThresholdExceeded))
	}
	// Output:
	// 0 false
	// 1 false
	// 2 true
}
