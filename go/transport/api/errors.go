package api

import (
	"context"
	"errors"
	"net/http"

	"hop.top/kit/go/runtime/domain"
)

// APIError is a structured error response.
type APIError struct {
	Status  int    `json:"status"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error implements the error interface.
func (e *APIError) Error() string {
	return e.Message
}

// Sentinel error aliases re-exported from the domain package.
var (
	ErrNotFound          = domain.ErrNotFound
	ErrConflict          = domain.ErrConflict
	ErrValidation        = domain.ErrValidation
	ErrInvalidTransition = domain.ErrInvalidTransition
)

// MapError converts a domain error into an APIError. A context
// deadline is a 504 with code deadline_exceeded: the work was cut
// off, and a retry may succeed.
func MapError(err error) *APIError {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return &ve.APIError
	}

	switch {
	case errors.Is(err, domain.ErrNotFound):
		return &APIError{
			Status:  http.StatusNotFound,
			Code:    "not_found",
			Message: "not found",
		}
	case errors.Is(err, domain.ErrConflict):
		return &APIError{
			Status:  http.StatusConflict,
			Code:    "conflict",
			Message: "conflict",
		}
	case errors.Is(err, domain.ErrValidation):
		return &APIError{
			Status:  http.StatusUnprocessableEntity,
			Code:    "validation_error",
			Message: "validation error",
		}
	case errors.Is(err, domain.ErrInvalidTransition):
		return &APIError{
			Status:  http.StatusConflict,
			Code:    "invalid_transition",
			Message: "invalid transition",
		}
	case errors.Is(err, context.DeadlineExceeded):
		return &APIError{
			Status:  http.StatusGatewayTimeout,
			Code:    CodeDeadlineExceeded,
			Message: "deadline exceeded",
		}
	default:
		return &APIError{
			Status:  http.StatusInternalServerError,
			Code:    "internal_error",
			Message: "internal server error",
		}
	}
}
