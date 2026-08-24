package policy

import (
	"fmt"

	"hop.top/kit/go/console/output"
	"hop.top/kit/go/runtime/domain"
)

// PolicyDeniedError is returned when a policy denies an event. It
// wraps domain.ErrConflict so callers that already map ErrConflict
// (e.g. transport/api → HTTP 409, CLI → exit 4) handle it uniformly.
type PolicyDeniedError struct {
	PolicyName string
	Topic      string
	Message    string
	Decision   Decision
}

// Error implements error.
func (e *PolicyDeniedError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("policy %q denied: %s", e.PolicyName, e.Message)
	}
	return fmt.Sprintf("policy %q denied", e.PolicyName)
}

// Unwrap exposes domain.ErrConflict for errors.Is matching.
func (e *PolicyDeniedError) Unwrap() error { return domain.ErrConflict }

// AsCLIError implements the conversion interface the RunE middleware
// uses (console/cli), so a denial renders as a real envelope instead of
// falling through to the generic bucket. CONFLICT / exit 4 is the code
// Unwrap already promises; rendering anything else would contradict it.
//
// Cause carries the policy + topic that produced the denial: the
// message alone tells an operator they were refused but not by which
// rule, and the denying policy name is the first thing they need to
// find the rule and change it.
func (e *PolicyDeniedError) AsCLIError() *output.Error {
	env := output.ConflictError(e.Error())
	env.Cause = fmt.Sprintf("policy %q denied topic %q", e.PolicyName, e.Topic)
	env.SuggestedFix = fmt.Sprintf("review policy %q in the loaded policy file, or supply input that satisfies it", e.PolicyName)
	return env
}
