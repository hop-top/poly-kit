package domain

import "errors"

// Sentinel errors returned by domain operations.
var (
	// ErrNotFound indicates the requested entity does not exist.
	ErrNotFound = errors.New("domain: not found")

	// ErrConflict indicates a uniqueness or concurrency conflict.
	ErrConflict = errors.New("domain: conflict")

	// ErrValidation indicates one or more validation rules failed.
	ErrValidation = errors.New("domain: validation error")

	// ErrInvalidTransition indicates a state transition is not allowed.
	ErrInvalidTransition = errors.New("domain: invalid state transition")
)
