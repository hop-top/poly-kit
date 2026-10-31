package cmdsurface

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/idemstore"
)

// TestIdempotencyContract runs the idempotency table of
// contracts/parity/api.json through the bridge's ledger. The Rust
// api-server runs the same table over HTTP; Go is the authority.
func TestIdempotencyContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.FromSlash("../../../contracts/parity/api.json"))
	require.NoError(t, err)
	var c struct {
		Cases []struct {
			Name  string `json:"name"`
			Steps []struct {
				Op        string `json:"op"`
				Principal string `json:"principal"`
				Key       string `json:"key"`
				Call      string `json:"call"`
				Fail      bool   `json:"fail"`
				Want      string `json:"want"`
			} `json:"steps"`
		} `json:"idempotency"`
	}
	require.NoError(t, json.Unmarshal(raw, &c))
	require.NotEmpty(t, c.Cases)

	name := map[string]string{"A": "a", "B": "b"}
	for _, tc := range c.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			run := &idemRunner{}
			for _, s := range tc.Steps {
				if s.Op == "hold" {
					run.gate = make(chan struct{})
					run.began = make(chan struct{}, 1)
				}
			}
			b, _ := idemBridge(t, run, NewIdempotencyLedger(idemstore.Memory()))
			ctx := context.Background()
			inv := func(principal, key, call string) Invocation {
				i := keyed(key, principal)
				i.Flags = map[string]any{"name": name[call]}
				return i
			}
			var held chan error
			for _, s := range tc.Steps {
				switch s.Op {
				case "hold":
					held = make(chan error, 1)
					go func(i Invocation) { _, err := b.Invoke(ctx, i); held <- err }(inv(s.Principal, s.Key, s.Call))
					<-run.began
				case "release":
					close(run.gate)
					require.NoError(t, <-held)
				case "call":
					run.exit = 0
					if s.Fail {
						run.exit = 1
					}
					before := run.calls.Load()
					res, err := b.Invoke(ctx, inv(s.Principal, s.Key, s.Call))
					got := "run"
					switch {
					case errors.Is(err, ErrIdempotencyKeyReused):
						got = "key_reused"
					case errors.Is(err, ErrIdempotencyConflict):
						got = "conflict"
					case err != nil:
						t.Fatalf("unexpected error: %v", err)
					case res.Replayed:
						got = "replay"
					}
					require.Equal(t, s.Want, got, "step %+v", s)
					ran := run.calls.Load() != before
					require.Equal(t, got == "run", ran, "the runner runs only on a run")
				default:
					t.Fatalf("unknown op %q", s.Op)
				}
			}
		})
	}
}
