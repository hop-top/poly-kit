package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Bind decodes the JSON request body into a value of type T.
// It checks that Content-Type is application/json.
func Bind[T any](r *http.Request) (T, error) {
	var v T
	ct := r.Header.Get("Content-Type")
	if ct != "" && !strings.HasPrefix(ct, "application/json") {
		return v, fmt.Errorf("unsupported content type: %s", ct)
	}
	if r.Body == nil {
		return v, fmt.Errorf("empty request body")
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, fmt.Errorf("decode json: %w", err)
	}
	if dec.More() {
		return v, fmt.Errorf("unexpected trailing data")
	}
	return v, nil
}

// JSON writes a JSON-encoded response with the given status code.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Error writes an APIError as a JSON response.
func Error(w http.ResponseWriter, status int, err error) {
	apiErr, ok := err.(*APIError)
	if !ok {
		apiErr = &APIError{
			Status:  status,
			Code:    "error",
			Message: err.Error(),
		}
	}
	JSON(w, apiErr.Status, apiErr)
}

// Negotiate returns the preferred response format based on the Accept
// header. Currently only "json" is supported.
func Negotiate(_ *http.Request) string {
	return "json"
}
