package cmdsurface

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// authGateTree is a root with one kit/auth-required leaf ("sync"), one
// plain leaf ("ping") and one destructive auth-required leaf ("wipe").
func authGateTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}
	root.AddCommand(
		&cobra.Command{
			Use:         "sync",
			RunE:        func(*cobra.Command, []string) error { return nil },
			Annotations: map[string]string{"kit/side-effect": "read", "kit/auth-required": "true"},
		},
		&cobra.Command{
			Use:         "ping",
			RunE:        func(*cobra.Command, []string) error { return nil },
			Annotations: map[string]string{"kit/side-effect": "read"},
		},
		&cobra.Command{
			Use:         "wipe",
			RunE:        func(*cobra.Command, []string) error { return nil },
			Annotations: map[string]string{"kit/side-effect": "destructive", "kit/auth-required": "true"},
		},
	)
	return root
}

// remoteSurfaces is every surface the authentication gate applies to.
var remoteSurfaces = []Surface{
	SurfaceREST, SurfaceWS, SurfaceSSE, SurfaceRPC, SurfaceMCP,
	SurfaceWebhook, SurfaceBus, SurfaceCron, SurfaceOAuthCB,
	SurfaceSigned, SurfaceFaaS, SurfaceSocket,
}

func TestAdmit_AuthRequiredRefusedWithoutEstablishedIdentity(t *testing.T) {
	for _, s := range remoteSurfaces {
		t.Run(string(s), func(t *testing.T) {
			calls := 0
			sink := &admitSink{}
			b := New(authGateTree(), WithRunner(countingRunner(&calls, nil)), WithSinks(sink.spec()))
			b.Expose("*", s)

			// A claimed caller is not an established one.
			_, err := b.Invoke(context.Background(), Invocation{
				Path: []string{"sync"},
				Meta: Meta{Surface: s, Caller: "alice"},
			})
			if !errors.Is(err, ErrAuthRefused) {
				t.Fatalf("err = %v, want ErrAuthRefused", err)
			}
			if calls != 0 {
				t.Fatalf("runner reached: calls = %d", calls)
			}
			if sink.count() != 1 || !errors.Is(sink.errs[0], ErrAuthRefused) {
				t.Fatalf("audit = %v, want one ErrAuthRefused record", sink.errs)
			}
		})
	}
}

func TestAdmit_AuthRequiredRunsWithEstablishedIdentity(t *testing.T) {
	for _, est := range []Establishment{EstablishedVerified, EstablishedTransport} {
		for _, s := range remoteSurfaces {
			t.Run(string(est)+"/"+string(s), func(t *testing.T) {
				calls := 0
				b := New(authGateTree(), WithRunner(countingRunner(&calls, nil)))
				b.Expose("*", s)
				_, err := b.Invoke(context.Background(), Invocation{
					Path: []string{"sync"},
					Meta: Meta{Surface: s, Established: est},
				})
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				if calls != 1 {
					t.Fatalf("calls = %d, want 1", calls)
				}
			})
		}
	}
}

func TestAdmit_AuthGateSkipsLocalSurfacesAndPlainLeaves(t *testing.T) {
	calls := 0
	b := New(authGateTree(), WithRunner(countingRunner(&calls, nil)))
	b.Expose("*", SurfaceCLI, SurfaceLib, SurfaceREST)

	for _, inv := range []Invocation{
		{Path: []string{"sync"}, Meta: Meta{Surface: SurfaceLib}},
		{Path: []string{"sync"}, Meta: Meta{Surface: SurfaceCLI}},
		{Path: []string{"sync"}},
		{Path: []string{"ping"}, Meta: Meta{Surface: SurfaceREST}},
	} {
		if _, err := b.Invoke(context.Background(), inv); err != nil {
			t.Fatalf("%v: err = %v", inv, err)
		}
	}
	if calls != 4 {
		t.Fatalf("calls = %d, want 4", calls)
	}
}

func TestAdmit_AuthGateIsSlotFour(t *testing.T) {
	b := New(authGateTree(), WithPermission(func(context.Context, Meta, *Leaf) PermissionDecision {
		return PermissionDecision{Reason: "nobody"}
	}))
	b.Expose("*", SurfaceREST)

	// Identity before authority: an unauthenticated caller is told
	// 401-class, never the ceiling's or the permission gate's answer.
	for _, path := range []string{"sync", "wipe"} {
		_, err := b.Invoke(context.Background(), Invocation{
			Path: []string{path},
			Meta: Meta{Surface: SurfaceREST},
		})
		if !errors.Is(err, ErrAuthRefused) {
			t.Fatalf("%s: err = %v, want ErrAuthRefused", path, err)
		}
	}
	// After enablement: a leaf not on the surface is not_enabled.
	b.Hide("sync", SurfaceREST)
	_, err := b.Invoke(context.Background(), Invocation{
		Path: []string{"sync"},
		Meta: Meta{Surface: SurfaceREST},
	})
	if !errors.Is(err, ErrSurfaceNotEnabled) {
		t.Fatalf("err = %v, want ErrSurfaceNotEnabled", err)
	}
	// Authenticated, the next gate answers.
	_, err = b.Invoke(context.Background(), Invocation{
		Path: []string{"wipe"},
		Meta: Meta{Surface: SurfaceREST, Established: EstablishedVerified},
	})
	if !errors.Is(err, ErrDestructiveBlocked) {
		t.Fatalf("err = %v, want ErrDestructiveBlocked", err)
	}
}

func TestAdmit_UnknownEstablishmentIsUnauthenticated(t *testing.T) {
	b := New(authGateTree())
	b.Expose("*", SurfaceREST)
	_, err := b.Invoke(context.Background(), Invocation{
		Path: []string{"sync"},
		Meta: Meta{Surface: SurfaceREST, Established: "trust-me"},
	})
	if !errors.Is(err, ErrAuthRefused) {
		t.Fatalf("err = %v, want ErrAuthRefused", err)
	}
}

func TestMeta_EstablishedNeverDecodedFromJSON(t *testing.T) {
	var inv Invocation
	body := `{"path":["sync"],"meta":{"caller":"root","established":"verified","Established":"verified"}}`
	if err := json.Unmarshal([]byte(body), &inv); err != nil {
		t.Fatal(err)
	}
	if inv.Meta.Authenticated() || inv.Meta.Established != EstablishedNone {
		t.Fatalf("Established decoded from a body: %q", inv.Meta.Established)
	}
	out, err := json.Marshal(Meta{Caller: "a", Established: EstablishedVerified})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(out)), "established") ||
		strings.Contains(string(out), "verified") {
		t.Fatalf("Established serialized: %s", out)
	}
}
