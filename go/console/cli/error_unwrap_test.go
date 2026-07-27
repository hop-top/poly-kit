package cli

import (
	"errors"
	"testing"

	"hop.top/kit/go/console/output"
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

// Boundary: an adopter error implementing AsCLIError takes the passthrough
// branch, which returns the adopter's own envelope verbatim. That envelope
// carries no retained error, so sentinel matching does NOT survive. This is
// deliberate — the adopter owns the envelope there — and is pinned so the
// asymmetry is a decision rather than a silent surprise.
func TestRunE_Middleware_TypedErrorDoesNotRetainSentinel(t *testing.T) {
	converted := toCLIError(&asCLIErrorStub{err: errHandlerSentinel})

	if converted.Code != "TYPED" {
		t.Fatalf("expected the adopter envelope to pass through, got code %q", converted.Code)
	}
	if errors.Is(converted, errHandlerSentinel) {
		t.Errorf("AsCLIError passthrough unexpectedly retains the sentinel; " +
			"if this is now intended, update the output README accordingly")
	}
}

type asCLIErrorStub struct{ err error }

func (a *asCLIErrorStub) Error() string { return "typed: " + a.err.Error() }
func (a *asCLIErrorStub) Unwrap() error { return a.err }
func (a *asCLIErrorStub) AsCLIError() *output.Error {
	return &output.Error{Code: "TYPED", Message: a.Error(), ExitCode: 7}
}
