package wizard

import "fmt"

// ValidationError is returned when a step value fails validation.
type ValidationError struct {
	StepKey string
	Err     error
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation failed for %q: %v", e.StepKey, e.Err)
}

func (e *ValidationError) Unwrap() error { return e.Err }

// ActionError is returned when an action step fails and the policy is abort.
type ActionError struct {
	StepKey string
	Err     error
	Action  ErrorAction
}

func (e *ActionError) Error() string {
	return fmt.Sprintf("action %q failed: %v", e.StepKey, e.Err)
}

func (e *ActionError) Unwrap() error { return e.Err }

// AbortError signals that the wizard was aborted.
type AbortError struct{}

func (e *AbortError) Error() string { return "wizard aborted" }
