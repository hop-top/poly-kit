package policy

import (
	"fmt"

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
