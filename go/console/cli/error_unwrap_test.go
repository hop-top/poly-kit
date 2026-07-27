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

// An adopter error implementing AsCLIError takes the passthrough branch. The
// adopter owns every rendered field there, but the sentinel must still
// survive: types like conformance.UsageError document that errors.Is identity
// holds, and the envelope conversion must not quietly break that promise.
func TestToCLIErrorTypedErrorRetainsSentinel(t *testing.T) {
	converted := toCLIError(&asCLIErrorStub{err: errHandlerSentinel})

	if !errors.Is(converted, errHandlerSentinel) {
		t.Errorf("errors.Is through the AsCLIError passthrough = false, want true")
	}
}

// Reattaching the sentinel must not disturb the envelope the adopter built.
func TestToCLIErrorTypedErrorKeepsAdopterFields(t *testing.T) {
	converted := toCLIError(&asCLIErrorStub{err: errHandlerSentinel})

	if converted.Code != "TYPED" {
		t.Errorf("Code = %q, want %q", converted.Code, "TYPED")
	}
	if converted.ExitCode != 7 {
		t.Errorf("ExitCode = %d, want 7", converted.ExitCode)
	}
}

// Adopters commonly return a shared package-level envelope from AsCLIError.
// Retaining must copy rather than mutate, or one call's error leaks into the
// next (and concurrent commands race on the same struct).
func TestToCLIErrorTypedErrorDoesNotMutateSharedEnvelope(t *testing.T) {
	shared := &output.Error{Code: "SHARED", Message: "shared", ExitCode: 9}
	first := errors.New("first")
	second := errors.New("second")

	a := toCLIError(&sharedEnvelopeStub{err: first, env: shared})
	b := toCLIError(&sharedEnvelopeStub{err: second, env: shared})

	if errors.Unwrap(shared) != nil {
		t.Errorf("the shared envelope was mutated; Retaining must copy")
	}
	if !errors.Is(a, first) || errors.Is(a, second) {
		t.Errorf("first conversion retained the wrong error")
	}
	if !errors.Is(b, second) || errors.Is(b, first) {
		t.Errorf("second conversion retained the wrong error")
	}
}

type asCLIErrorStub struct{ err error }

func (a *asCLIErrorStub) Error() string { return "typed: " + a.err.Error() }
func (a *asCLIErrorStub) Unwrap() error { return a.err }
func (a *asCLIErrorStub) AsCLIError() *output.Error {
	return &output.Error{Code: "TYPED", Message: a.Error(), ExitCode: 7}
}

type sharedEnvelopeStub struct {
	err error
	env *output.Error
}

func (s *sharedEnvelopeStub) Error() string             { return s.err.Error() }
func (s *sharedEnvelopeStub) Unwrap() error             { return s.err }
func (s *sharedEnvelopeStub) AsCLIError() *output.Error { return s.env }
