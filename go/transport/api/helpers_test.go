package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hop.top/kit/go/transport/api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testItem struct {
	Name string `json:"name"`
}

func TestBind(t *testing.T) {
	body := `{"name":"widget"}`
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	got, err := api.Bind[testItem](req)
	require.NoError(t, err)
	assert.Equal(t, "widget", got.Name)
}

func TestBind_WrongContentType(t *testing.T) {
	req := httptest.NewRequest("POST", "/", strings.NewReader("x"))
	req.Header.Set("Content-Type", "text/plain")

	_, err := api.Bind[testItem](req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported content type")
}

func TestBind_NilBody(t *testing.T) {
	req := httptest.NewRequest("POST", "/", nil)
	req.Body = nil

	_, err := api.Bind[testItem](req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty request body")
}

func TestBind_InvalidJSON(t *testing.T) {
	req := httptest.NewRequest("POST", "/", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")

	_, err := api.Bind[testItem](req)
	require.Error(t, err)
}

func TestJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	api.JSON(rec, http.StatusCreated, map[string]string{"id": "1"})

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var got map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, "1", got["id"])
}

func TestError_APIError(t *testing.T) {
	rec := httptest.NewRecorder()
	apiErr := &api.APIError{Status: 404, Code: "not_found", Message: "gone"}
	api.Error(rec, 404, apiErr)

	assert.Equal(t, 404, rec.Code)
	var got api.APIError
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, "not_found", got.Code)
}

func TestError_PlainError(t *testing.T) {
	rec := httptest.NewRecorder()
	api.Error(rec, 500, errors.New("boom"))

	assert.Equal(t, 500, rec.Code)
	var got api.APIError
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, "error", got.Code)
	assert.Equal(t, "boom", got.Message)
}

func TestBind_TrailingData(t *testing.T) {
	body := `{"name":"widget"}{"name":"extra"}`
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	_, err := api.Bind[testItem](req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected trailing data")
}

func TestNegotiate(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	assert.Equal(t, "json", api.Negotiate(req))
}
