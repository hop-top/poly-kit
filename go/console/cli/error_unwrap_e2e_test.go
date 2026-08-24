package cli_test

import (
	"errors"
	"fmt"
	"testing"

	"hop.top/kit/go/console/output"
)

var errAdopterSentinel = errors.New("adopter sentinel")

// The unit tests in error_unwrap_test.go call toCLIError directly. This
// exercises the path an adopter actually hits: a handler failure traveling
// out through the full RunE middleware chain (policy, idempotency,
// deprecation, error-render). The error surfacing from Execute is what
// adopters match on, so the sentinel must survive every layer, not just the
// conversion function.
func TestRunE_Middleware_PreservesSentinelToCaller(t *testing.T) {
	_, err := runWithErr(t, "json", fmt.Errorf("loading config: %w", errAdopterSentinel))
	if err == nil {
		t.Fatal("expected an error out of the middleware chain")
	}
	if !errors.Is(err, errAdopterSentinel) {
		t.Errorf("errors.Is(executeErr, sentinel) = false, want true; "+
			"the chain is severed somewhere in the middleware (err=%#v)", err)
	}
}

// The envelope must stay reachable alongside the sentinel, so a caller can
// both classify the failure and read the exit code off the same error.
func TestRunE_Middleware_ExposesEnvelopeAndSentinel(t *testing.T) {
	_, err := runWithErr(t, "json", fmt.Errorf("loading config: %w", errAdopterSentinel))

	var env *output.Error
	if !errors.As(err, &env) {
		t.Fatalf("errors.As did not reach *output.Error, got %#v", err)
	}
	if env.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", env.ExitCode)
	}
	if !errors.Is(err, errAdopterSentinel) {
		t.Errorf("envelope reachable but sentinel lost")
	}
}
