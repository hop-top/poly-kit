package cli

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli/idemstore"
	"hop.top/kit/go/console/cli/policy"
	"hop.top/kit/go/transport/cmdsurface/admitted"
	"hop.top/kit/internal/admittedctx"
)

// These tests cover the core middleware's reading of the admitted
// caller without a bridge: they stamp the caller the way the bridge
// does, under the module-internal key.

func stampCaller(ctx context.Context, c admitted.Caller) context.Context {
	return context.WithValue(ctx, admittedctx.CallerKey{}, c)
}

func TestRunCaller_MapsTheStampedCaller(t *testing.T) {
	cases := []struct {
		name   string
		caller *admitted.Caller
		want   *policy.Caller
	}{
		{name: "local CLI run", caller: nil, want: nil},
		{
			name:   "transport-established caller is the owner",
			caller: &admitted.Caller{Established: admitted.EstablishedTransport},
			want:   &policy.Caller{Owner: true},
		},
		{
			name: "verified caller keeps principal, tenant and scopes",
			caller: &admitted.Caller{
				Established: admitted.EstablishedVerified,
				Principal:   "alice", Tenant: "acme",
				Scopes: []string{"deploy:write"},
			},
			want: &policy.Caller{Principal: "alice", Tenant: "acme", Scopes: []string{"deploy:write"}},
		},
		{
			name:   "unestablished caller is nobody",
			caller: &admitted.Caller{Established: admitted.EstablishedNone, IdempotencyScope: "s"},
			want:   nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.caller != nil {
				ctx = stampCaller(ctx, *tc.caller)
			}
			cmd := &cobra.Command{Use: "deploy"}
			cmd.SetContext(ctx)
			got := runCaller(cmd)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("runCaller = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// mintCommand is a write whose output names the caller it ran for:
// the shape of a command whose recorded output is that caller's alone.
func mintCommand(store idemstore.Store, runs *int) *cobra.Command {
	cmd := &cobra.Command{Use: "mint"}
	cmd.Flags().String(idempotencyKeyFlag, "", "")
	cmd.RunE = wrapIdempotencyRunE(func(c *cobra.Command, _ []string) error {
		*runs++
		who := "local"
		if caller, ok := admitted.CallerFrom(c.Context()); ok {
			who = caller.Principal
		}
		fmt.Fprintf(c.OutOrStdout(), "secret for %s", who)
		return nil
	}, store)
	return cmd
}

func runMint(t *testing.T, cmd *cobra.Command, ctx context.Context, key string) string {
	t.Helper()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetContext(ctx)
	if err := cmd.Flags().Set(idempotencyKeyFlag, key); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	return out.String()
}

// Two served callers sending the same key never replay each other's
// output, and neither meets the local user's key.
func TestIdempotencyWrap_StampedCallersNeverReplayEachOther(t *testing.T) {
	t.Setenv(admitted.EnvIdempotencyScope, "")
	store := idemstore.Memory()
	defer store.Close()
	runs := 0
	cmd := mintCommand(store, &runs)

	alice := stampCaller(context.Background(), admitted.Caller{
		Established: admitted.EstablishedVerified, Principal: "alice", IdempotencyScope: "scope-alice",
	})
	bob := stampCaller(context.Background(), admitted.Caller{
		Established: admitted.EstablishedVerified, Principal: "bob", IdempotencyScope: "scope-bob",
	})

	if got := runMint(t, cmd, alice, "k1"); got != "secret for alice" {
		t.Fatalf("alice's first call = %q", got)
	}
	if got := runMint(t, cmd, bob, "k1"); got != "secret for bob" {
		t.Fatalf("bob sending alice's key got %q, want his own run", got)
	}
	if got := runMint(t, cmd, context.Background(), "k1"); got != "secret for local" {
		t.Fatalf("the local k1 got %q, want its own run", got)
	}
	if runs != 3 {
		t.Fatalf("command ran %d times, want 3 (no replay across callers)", runs)
	}
	// The same caller's repeat is a replay.
	if got := runMint(t, cmd, alice, "k1"); got != "secret for alice" {
		t.Fatalf("alice's repeat = %q", got)
	}
	if runs != 3 {
		t.Fatalf("alice's repeat ran the command again (%d runs)", runs)
	}
}
