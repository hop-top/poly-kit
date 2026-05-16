package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"hop.top/kit/go/transport/api"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidationError_EmbedsAPIError(t *testing.T) {
	ve := &api.ValidationError{
		APIError: api.APIError{
			Status:  http.StatusUnprocessableEntity,
			Code:    "validation_error",
			Message: "validation failed",
		},
		Details: []api.FieldError{
			{Field: "name", Message: "required"},
		},
	}
	assert.Equal(t, http.StatusUnprocessableEntity, ve.Status)
	assert.Equal(t, "validation_error", ve.Code)
	assert.Equal(t, "validation failed", ve.Error())
}

func TestValidationError_OmitsEmptyDetails(t *testing.T) {
	ve := &api.ValidationError{
		APIError: api.APIError{
			Status:  http.StatusUnprocessableEntity,
			Code:    "validation_error",
			Message: "validation failed",
		},
	}
	data, err := json.Marshal(ve)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	_, hasDetails := raw["details"]
	assert.False(t, hasDetails, "empty details should be omitted")
}

func TestMapHumaError_ConvertsErrorModel(t *testing.T) {
	humaErr := &huma.ErrorModel{
		Status: http.StatusUnprocessableEntity,
		Detail: "validation failed",
		Errors: []*huma.ErrorDetail{
			{Message: "expected required property", Location: "body.name"},
			{Message: "expected integer", Location: "body.age"},
		},
	}

	got := api.MapHumaError(humaErr)
	require.NotNil(t, got)
	assert.Equal(t, http.StatusUnprocessableEntity, got.Status)
	assert.Equal(t, "validation_error", got.Code)
	assert.Equal(t, "validation failed", got.Message)
	require.Len(t, got.Details, 2)
	assert.Equal(t, "body.name", got.Details[0].Field)
	assert.Equal(t, "expected required property", got.Details[0].Message)
	assert.Equal(t, "body.age", got.Details[1].Field)
	assert.Equal(t, "expected integer", got.Details[1].Message)
}

func TestMapHumaError_NilOnNonHumaError(t *testing.T) {
	got := api.MapHumaError(assert.AnError)
	assert.Nil(t, got)
}

func TestMapHumaError_NoErrors(t *testing.T) {
	humaErr := &huma.ErrorModel{
		Status: http.StatusBadRequest,
		Detail: "bad request",
	}
	got := api.MapHumaError(humaErr)
	require.NotNil(t, got)
	assert.Empty(t, got.Details)
}

func TestValidationFailed_WritesJSON(t *testing.T) {
	w := httptest.NewRecorder()
	api.ValidationFailed(w,
		api.FieldError{Field: "email", Message: "invalid format"},
		api.FieldError{Field: "age", Message: "must be positive"},
	)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")

	var got api.ValidationError
	require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
	assert.Equal(t, http.StatusUnprocessableEntity, got.Status)
	assert.Equal(t, "validation_error", got.Code)
	assert.Equal(t, "validation failed", got.Message)
	require.Len(t, got.Details, 2)
	assert.Equal(t, "email", got.Details[0].Field)
	assert.Equal(t, "age", got.Details[1].Field)
}
