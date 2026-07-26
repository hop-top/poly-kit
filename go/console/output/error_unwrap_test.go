package output_test

import (
	"encoding/json"
	"errors"
	"testing"

	"gopkg.in/yaml.v3"

	"hop.top/kit/go/console/output"
)

var errSentinel = errors.New("sentinel")

// Wrapping an error into the envelope must not sever the errors.Is chain:
// callers outside the handler classify failures by sentinel, and a flattened
// Message string is not matchable.
func TestErrorUnwrapPreservesSentinel(t *testing.T) {
	wrapped := output.WrapError(errSentinel, output.CodeGeneric, 1)

	if !errors.Is(wrapped, errSentinel) {
		t.Errorf("errors.Is(wrapped, errSentinel) = false, want true; the chain is severed")
	}
}

// errors.As must still reach the envelope itself, so renderers can pull the
// exit code and suggested fix off it.
func TestErrorUnwrapStillExposesEnvelope(t *testing.T) {
	wrapped := output.WrapError(errSentinel, output.CodeUnauthorized, 5)

	var env *output.Error
	if !errors.As(wrapped, &env) {
		t.Fatalf("errors.As did not reach *output.Error")
	}
	if env.ExitCode != 5 {
		t.Errorf("ExitCode = %d, want 5", env.ExitCode)
	}
}

// An envelope built without a wrapped error must unwrap to nil rather than
// panicking — most construction sites have no underlying error to retain.
func TestErrorUnwrapNilWhenNothingWrapped(t *testing.T) {
	e := &output.Error{Code: output.CodeGeneric, Message: "boom", ExitCode: 1}

	if got := errors.Unwrap(e); got != nil {
		t.Errorf("Unwrap() = %v, want nil", got)
	}
	if errors.Is(e, errSentinel) {
		t.Errorf("errors.Is matched an unrelated sentinel")
	}
}

// A nil receiver must not panic, matching the existing Error() guard.
func TestErrorUnwrapNilReceiver(t *testing.T) {
	var e *output.Error
	if got := e.Unwrap(); got != nil {
		t.Errorf("Unwrap() on nil receiver = %v, want nil", got)
	}
}

// The retained error is unexported precisely so it cannot reach the wire.
// Serialization must be byte-identical whether or not an error is wrapped.
func TestErrorSerializationUnchangedByWrapping(t *testing.T) {
	plain := &output.Error{Code: output.CodeGeneric, Message: "boom", ExitCode: 1}
	wrapped := output.WrapError(errors.New("boom"), output.CodeGeneric, 1)

	plainJSON, err := json.Marshal(plain)
	if err != nil {
		t.Fatalf("marshal plain: %v", err)
	}
	wrappedJSON, err := json.Marshal(wrapped)
	if err != nil {
		t.Fatalf("marshal wrapped: %v", err)
	}
	if string(plainJSON) != string(wrappedJSON) {
		t.Errorf("JSON differs:\n plain   = %s\n wrapped = %s", plainJSON, wrappedJSON)
	}

	plainYAML, err := yaml.Marshal(plain)
	if err != nil {
		t.Fatalf("marshal plain yaml: %v", err)
	}
	wrappedYAML, err := yaml.Marshal(wrapped)
	if err != nil {
		t.Fatalf("marshal wrapped yaml: %v", err)
	}
	if string(plainYAML) != string(wrappedYAML) {
		t.Errorf("YAML differs:\n plain   = %s\n wrapped = %s", plainYAML, wrappedYAML)
	}
}
