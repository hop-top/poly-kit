package consent_test

import (
	"context"
	"fmt"

	"hop.top/kit/go/core/consent"
)

// Resolve is pure: env comes from the injected EnvProvider, the
// persisted decision from the caller.
func ExampleResolve() {
	d := consent.Resolve(context.Background(), consent.Inputs{
		Env:       consent.MapEnv(map[string]string{"DO_NOT_TRACK": "1"}),
		Persisted: consent.Decision{State: consent.StateGranted},
	})
	fmt.Println(d.State, d.DecisionSource, d.Granted())
	// Output: denied env false
}
