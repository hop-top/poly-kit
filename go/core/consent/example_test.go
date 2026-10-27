package consent_test

import (
	"context"
	"errors"
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

// Request asks once; Use gates every tap and audits it. No grant and
// no prompter is a refusal.
func ExampleGrants() {
	ctx := context.Background()
	g := consent.NewGrants(&consent.MemoryGrantStore{}) // OpenGrants() in production
	ask := consent.PrompterFunc(func(context.Context, consent.Prompt) (consent.Answer, error) {
		return consent.Answer{Decision: consent.Allow, Duration: consent.Session}, nil
	})
	_, err := g.Request(ctx, "io.clipboard.read", "app:mytool", ask)
	fmt.Println(err)
	_, err = g.Use(ctx, "io.clipboard.read", "app:mytool") // before every tap
	fmt.Println(err)
	_, err = g.Use(ctx, "io.screen.capture", "app:mytool")
	fmt.Println(consent.KindOf(err), errors.Is(err, consent.ErrRefused))
	// Output:
	// <nil>
	// <nil>
	//  true
}
