package breaker

import "errors"

// ErrBrokenCircuit is the single sentinel callers check after Allow.
// Returned when the breaker is Open (manually tripped, circuit
// breaker tripped, or a Volume/Count threshold was breached).
var ErrBrokenCircuit = errors.New("breaker: circuit open")

// ErrThresholdExceeded is returned by custom Volume/Count policies
// when their cumulative counter exceeds the configured cap. Callers
// usually only check ErrBrokenCircuit; this exists for callers that
// want to distinguish threshold breach from generic open-circuit.
var ErrThresholdExceeded = errors.New("breaker: threshold exceeded")
