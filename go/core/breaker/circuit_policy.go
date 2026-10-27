package breaker

import (
	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/circuitbreaker"
	"github.com/failsafe-go/failsafe-go/common"
	"github.com/failsafe-go/failsafe-go/policy"
)

// errOpen is what the circuit policy fails an execution with. It
// matches both ErrBrokenCircuit and failsafe's circuitbreaker.ErrOpen,
// which Executor() callers saw before the circuit moved kit-side.
type errOpen struct{}

func (errOpen) Error() string { return ErrBrokenCircuit.Error() }

func (errOpen) Is(target error) bool {
	return target == ErrBrokenCircuit || target == circuitbreaker.ErrOpen
}

// circuitPolicy exposes the breaker's kit-side circuit as a failsafe
// policy, so Executor() still composes it with the failsafe policies.
type circuitPolicy struct {
	b *breakerImpl
}

type circuitPolicyExecutor struct {
	policy.BaseExecutor[any]
	b *breakerImpl
}

var _ policy.Executor[any] = (*circuitPolicyExecutor)(nil)

func (p *circuitPolicy) ToExecutor(_ any) any {
	e := &circuitPolicyExecutor{b: p.b}
	e.Executor = e
	return e
}

func (e *circuitPolicyExecutor) PreExecute(_ policy.ExecutionInternal[any]) *common.PolicyResult[any] {
	if !e.b.acquire() {
		return &common.PolicyResult[any]{Error: errOpen{}, Done: true}
	}
	return nil
}

func (e *circuitPolicyExecutor) OnSuccess(exec policy.ExecutionInternal[any], result *common.PolicyResult[any]) {
	e.BaseExecutor.OnSuccess(exec, result)
	e.b.recordCircuit(true)
}

func (e *circuitPolicyExecutor) OnFailure(exec policy.ExecutionInternal[any], result *common.PolicyResult[any]) *common.PolicyResult[any] {
	e.BaseExecutor.OnFailure(exec, result)
	e.b.recordCircuit(false)
	return result
}

var _ failsafe.Policy[any] = (*circuitPolicy)(nil)
