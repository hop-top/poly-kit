package api

import (
	"errors"
	"net/http"
)

// Idempotency replay on the projection: the bridge answers a call
// whose Idempotency-Key the same principal already used for the same
// command from its record, and the projection marks the response.
const (
	// HeaderIdempotentReplayed is set to "true" on a response the
	// server answered from its idempotency record rather than by
	// running the command.
	HeaderIdempotentReplayed = "Idempotent-Replayed"
	// CodeIdempotencyConflict is the 409 refusal of a call whose key
	// names a call still running.
	CodeIdempotencyConflict = "idempotency_conflict"
	// CodeIdempotencyKeyReused is the 422 refusal of a key reused for
	// a different call.
	CodeIdempotencyKeyReused = "idempotency_key_reused"
)

// Projection errors an executor returns for the idempotency refusals.
var (
	// ErrIdempotencyConflict reports that the call's key belongs to a
	// call from the same principal that is still running: 409.
	ErrIdempotencyConflict = errors.New("api: idempotency key in use by a call still running")
	// ErrIdempotencyKeyReused reports that the call's key was already
	// used for a different call: 422.
	ErrIdempotencyKeyReused = errors.New("api: idempotency key reused with a different call")
)

// idempotencyError maps the idempotency refusals onto their status and
// code; ok is false for any other error.
func idempotencyError(err error) (ae *APIError, ok bool) {
	switch {
	case errors.Is(err, ErrIdempotencyConflict):
		return &APIError{Status: http.StatusConflict, Code: CodeIdempotencyConflict, Message: err.Error()}, true
	case errors.Is(err, ErrIdempotencyKeyReused):
		return &APIError{Status: http.StatusUnprocessableEntity, Code: CodeIdempotencyKeyReused, Message: err.Error()}, true
	}
	return nil, false
}

// markReplayed sets the replay marker on a response the executor
// answered from its record.
func markReplayed(w http.ResponseWriter, replayed bool) {
	if replayed {
		w.Header().Set(HeaderIdempotentReplayed, "true")
	}
}

// replayedStream is implemented by a CommandStream that can tell,
// before it runs, whether it replays a recorded answer.
type replayedStream interface{ Replayed() bool }
