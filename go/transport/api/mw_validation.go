package api

import (
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// FieldError describes a validation failure on a single field.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ValidationError extends APIError with field-level detail.
type ValidationError struct {
	APIError
	Details []FieldError `json:"details,omitempty"`
}

// MapHumaError converts a huma ErrorModel to a ValidationError.
// Returns nil when err is not a *huma.ErrorModel.
func MapHumaError(err error) *ValidationError {
	var model *huma.ErrorModel
	if !errors.As(err, &model) {
		return nil
	}

	details := make([]FieldError, 0, len(model.Errors))
	for _, d := range model.Errors {
		details = append(details, FieldError{
			Field:   d.Location,
			Message: d.Message,
		})
	}

	ve := &ValidationError{
		APIError: APIError{
			Status:  model.Status,
			Code:    "validation_error",
			Message: model.Detail,
		},
	}
	if len(details) > 0 {
		ve.Details = details
	}
	return ve
}

// ValidationFailed writes a 422 response with field-level details.
func ValidationFailed(w http.ResponseWriter, fields ...FieldError) {
	ve := &ValidationError{
		APIError: APIError{
			Status:  http.StatusUnprocessableEntity,
			Code:    "validation_error",
			Message: "validation failed",
		},
		Details: fields,
	}
	JSON(w, http.StatusUnprocessableEntity, ve)
}
