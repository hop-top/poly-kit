package api_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"hop.top/kit/go/transport/api"

	"github.com/stretchr/testify/assert"
)

func TestAPIError_Error(t *testing.T) {
	e := &api.APIError{Status: 400, Code: "bad", Message: "bad request"}
	assert.Equal(t, "bad request", e.Error())
}

func TestMapError_NotFound(t *testing.T) {
	err := fmt.Errorf("item: %w", api.ErrNotFound)
	got := api.MapError(err)
	assert.Equal(t, http.StatusNotFound, got.Status)
	assert.Equal(t, "not_found", got.Code)
}

func TestMapError_Conflict(t *testing.T) {
	got := api.MapError(api.ErrConflict)
	assert.Equal(t, http.StatusConflict, got.Status)
	assert.Equal(t, "conflict", got.Code)
}

func TestMapError_Validation(t *testing.T) {
	got := api.MapError(api.ErrValidation)
	assert.Equal(t, http.StatusUnprocessableEntity, got.Status)
	assert.Equal(t, "validation_error", got.Code)
}

func TestMapError_InvalidTransition(t *testing.T) {
	got := api.MapError(api.ErrInvalidTransition)
	assert.Equal(t, http.StatusConflict, got.Status)
	assert.Equal(t, "invalid_transition", got.Code)
}

func TestMapError_Unknown(t *testing.T) {
	got := api.MapError(errors.New("mystery"))
	assert.Equal(t, http.StatusInternalServerError, got.Status)
	assert.Equal(t, "internal_error", got.Code)
	assert.Equal(t, "internal server error", got.Message)
}

func TestMapError_ValidationError(t *testing.T) {
	ve := &api.ValidationError{
		APIError: api.APIError{
			Status:  422,
			Code:    "validation_error",
			Message: "validation failed",
		},
		Details: []api.FieldError{{Field: "name", Message: "required"}},
	}
	got := api.MapError(ve)
	assert.Equal(t, 422, got.Status)
	assert.Equal(t, "validation_error", got.Code)
	assert.Equal(t, "validation failed", got.Message)
}

// --- regressions ---

func TestMapError_MessageNoDomainPrefix(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{api.ErrNotFound, "not found"},
		{api.ErrConflict, "conflict"},
		{api.ErrValidation, "validation error"},
		{api.ErrInvalidTransition, "invalid transition"},
		{fmt.Errorf("item: %w", api.ErrNotFound), "not found"},
	}
	for _, tc := range cases {
		got := api.MapError(tc.err)
		assert.Equal(t, tc.want, got.Message, "for %v", tc.err)
		assert.NotContains(t, got.Message, "domain:")
	}
}
