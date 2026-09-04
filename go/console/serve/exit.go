package serve

import "hop.top/kit/go/console/output"

// Outcome kinds a serve run can end in (contract §"Exit behavior").
// Every kind maps onto an existing code in
// hop.top/kit/go/console/output; this package allocates no new
// numbers.
type LifecycleOutcome string

const (
	// OutcomeCleanStop is a signal-initiated shutdown that completed
	// within budget. SIGTERM is how a supervisor asks for an orderly
	// exit; answering it non-zero makes every rolling restart look
	// like a crash.
	OutcomeCleanStop LifecycleOutcome = "clean-stop"

	// OutcomeInvalidSelection is two or more positional arguments, or
	// a reserved selector word used as a service name.
	OutcomeInvalidSelection LifecycleOutcome = "invalid-selection"

	// OutcomeConfigInvalid is a failure of the configuration gate.
	OutcomeConfigInvalid LifecycleOutcome = "config-invalid"

	// OutcomeNoServices is a supervisor invocation that resolved to
	// zero services.
	OutcomeNoServices LifecycleOutcome = "no-services"

	// OutcomeUnknownService is a selector naming an unregistered
	// service.
	OutcomeUnknownService LifecycleOutcome = "unknown-service"

	// OutcomePolicyDenied is a failure of the policy gate.
	OutcomePolicyDenied LifecycleOutcome = "policy-denied"

	// OutcomeStartFailed is a service that failed before reporting
	// ready, including one that exhausted its readiness timeout.
	OutcomeStartFailed LifecycleOutcome = "start-failed"

	// OutcomeRuntimeCrash is a service that failed after reporting
	// ready.
	OutcomeRuntimeCrash LifecycleOutcome = "runtime-crash"

	// OutcomeShutdownTimeout is a shutdown that exceeded the
	// supervisor's total budget.
	OutcomeShutdownTimeout LifecycleOutcome = "shutdown-timeout"
)

// exitTable is the contract's exit-behavior table, verbatim.
//
// OutcomeStartFailed and OutcomeRuntimeCrash share exit 1
// deliberately: they differ in when, not in what an operator does
// next, and the distinguishing detail belongs in the message and the
// failed event rather than in a second numeric code.
var exitTable = map[LifecycleOutcome]struct {
	code string
	exit int
}{
	OutcomeCleanStop:        {output.CodeOK, 0},
	OutcomeInvalidSelection: {output.CodeUsage, 2},
	OutcomeConfigInvalid:    {output.CodeUsage, 2},
	OutcomeNoServices:       {output.CodeUsage, 2},
	OutcomeUnknownService:   {output.CodeNotFound, 3},
	OutcomePolicyDenied:     {output.CodeUnauthorized, 5},
	OutcomeStartFailed:      {output.CodeGeneric, 1},
	OutcomeRuntimeCrash:     {output.CodeGeneric, 1},
	OutcomeShutdownTimeout:  {output.CodeGeneric, 1},
}

// ExitCodeFor returns the process exit code for o. An outcome not in
// the table is treated as a generic failure (exit 1) rather than a
// success, so a kind added without a table row fails loudly instead
// of silently exiting 0.
func ExitCodeFor(o LifecycleOutcome) int {
	row, ok := exitTable[o]
	if !ok {
		return 1
	}
	return row.exit
}

// CodeFor returns the output.Code* string for o, for the rendered
// error envelope. An unknown outcome maps to output.CodeGeneric,
// matching [ExitCodeFor].
func CodeFor(o LifecycleOutcome) string {
	row, ok := exitTable[o]
	if !ok {
		return output.CodeGeneric
	}
	return row.code
}

// IsFailure reports whether o exits non-zero.
func IsFailure(o LifecycleOutcome) bool { return ExitCodeFor(o) != 0 }

// WorstOutcome returns the outcome the process should exit on given
// everything observed during a run. Under FailurePolicy Isolate the
// process may survive several service failures, and the exit code
// must reflect the worst outcome across the whole run rather than the
// last one (contract §"Exit behavior").
//
// "Worst" is severity, not exit-code magnitude: any failure beats a
// clean stop, and among failures the first one observed wins, because
// it is the one that explains the rest. An empty slice is a clean
// stop.
func WorstOutcome(observed []LifecycleOutcome) LifecycleOutcome {
	worst := OutcomeCleanStop
	for _, o := range observed {
		if IsFailure(o) && !IsFailure(worst) {
			worst = o
		}
	}
	return worst
}
