package cli

import (
	"errors"
	"testing"
)

var errHandlerSentinel = errors.New("handler sentinel")

// The middleware converts a handler failure into the structured envelope.
// That conversion must not sever the errors.Is chain: adopters classify
// failures by sentinel at the Execute boundary, and a Message string
// flattened via err.Error() is not matchable.
func TestToCLIErrorPreservesSentinel(t *testing.T) {
	converted := toCLIError(errHandlerSentinel)

	if converted == nil {
		t.Fatal("toCLIError returned nil")
	}
	if !errors.Is(converted, errHandlerSentinel) {
		t.Errorf("errors.Is(converted, sentinel) = false, want true; "+
			"the chain is severed at the middleware boundary (code=%q message=%q)",
			converted.Code, converted.Message)
	}
}

// A sentinel wrapped in context must still match through the envelope, since
// handlers routinely return fmt.Errorf("...: %w", sentinel).
func TestToCLIErrorPreservesWrappedSentinel(t *testing.T) {
	converted := toCLIError(errors.New("outer: " + errHandlerSentinel.Error()))
	if errors.Is(converted, errHandlerSentinel) {
		t.Errorf("a string-formatted error must NOT match by sentinel")
	}

	wrapped := toCLIError(wrapContext(errHandlerSentinel))
	if !errors.Is(wrapped, errHandlerSentinel) {
		t.Errorf("errors.Is through %%w-wrapped sentinel = false, want true")
	}
}

func wrapContext(err error) error {
	return &contextErr{err: err}
}

type contextErr struct{ err error }

func (c *contextErr) Error() string { return "context: " + c.err.Error() }
func (c *contextErr) Unwrap() error { return c.err }
