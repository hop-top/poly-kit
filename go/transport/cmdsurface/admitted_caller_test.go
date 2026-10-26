package cmdsurface

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"hop.top/kit/go/transport/cmdsurface/admitted"
)

// baseServedKey is the derivation the serve-lifecycle contract fixes for
// a served --idempotency-key, over the bridge's own [IdempotencyScope]:
// "served:" and the hex SHA-256 of scope, NUL, key.
func baseServedKey(m Meta, key string) string {
	sum := sha256.Sum256([]byte(IdempotencyScope(m) + "\x00" + key))
	return "served:" + hex.EncodeToString(sum[:])
}

func callerMetas() map[string]Meta {
	return map[string]Meta{
		"verified": {
			Surface: SurfaceREST, Established: EstablishedVerified,
			Caller: "alice", Tenant: "acme",
			Extra: map[string]string{"scopes": "deploy:write,read"},
		},
		"transport": {
			Surface: SurfaceSocket, Established: EstablishedTransport,
			Caller: "claims-to-be-root", Tenant: "t",
		},
		"claimed": {
			Surface: SurfaceRPC, Caller: "bob", Tenant: "acme",
			Extra: map[string]string{"remote_addr": "10.0.0.1:5555"},
		},
		"claimed-anonymous": {
			Surface: SurfaceREST,
			Extra:   map[string]string{"remote_addr": "10.0.0.2:4444"},
		},
	}
}

// The leaf scopes a key read from the stamped caller exactly as the
// bridge's own derivation over the admitted Meta does, for every kind
// of caller: the leaf and IdempotencyScope cannot drift apart.
func TestAdmittedScopeIdempotencyKey_MatchesBaseDerivation(t *testing.T) {
	t.Setenv(EnvIdempotencyScope, "")
	for name, m := range callerMetas() {
		t.Run(name, func(t *testing.T) {
			ctx := withAdmitted(context.Background(), m)
			want := baseServedKey(m, "k1")
			if got := admitted.ScopeIdempotencyKey(ctx, "k1"); got != want {
				t.Errorf("admitted.ScopeIdempotencyKey = %q, want %q", got, want)
			}
			if got := ScopeIdempotencyKey(ctx, "k1"); got != want {
				t.Errorf("cmdsurface.ScopeIdempotencyKey = %q, want %q", got, want)
			}
		})
	}
}

// A served call's own scope wins over a KIT_IDEMPOTENCY_SCOPE the
// process inherited.
func TestAdmittedScopeIdempotencyKey_StampWinsOverEnvironment(t *testing.T) {
	t.Setenv(EnvIdempotencyScope, "inherited")
	m := callerMetas()["verified"]
	if got, want := admitted.ScopeIdempotencyKey(withAdmitted(context.Background(), m), "k1"), baseServedKey(m, "k1"); got != want {
		t.Fatalf("key = %q, want %q", got, want)
	}
}

func TestCallerOf_NamesOnlyAVerifiedCaller(t *testing.T) {
	ms := callerMetas()

	v := CallerOf(ms["verified"])
	if v.Established != EstablishedVerified || v.Principal != "alice" || v.Tenant != "acme" {
		t.Errorf("verified caller = %+v", v)
	}
	if len(v.Scopes) != 2 || v.Scopes[0] != "deploy:write" || v.Scopes[1] != "read" {
		t.Errorf("verified scopes = %v, want [deploy:write read]", v.Scopes)
	}

	for _, name := range []string{"transport", "claimed", "claimed-anonymous"} {
		c := CallerOf(ms[name])
		if c.Principal != "" || c.Tenant != "" || c.Scopes != nil {
			t.Errorf("%s caller carries a claim as identity: %+v", name, c)
		}
		if c.Established != ms[name].Established {
			t.Errorf("%s caller established %q, want %q", name, c.Established, ms[name].Established)
		}
	}
	for name, m := range ms {
		if got := CallerOf(m).IdempotencyScope; got != IdempotencyScope(m) {
			t.Errorf("%s IdempotencyScope = %q, want IdempotencyScope(meta)", name, got)
		}
	}
}

// withAdmitted stamps both views from the same Meta.
func TestWithAdmitted_StampsMetaAndCaller(t *testing.T) {
	m := callerMetas()["verified"]
	ctx := withAdmitted(context.Background(), m)
	got, ok := AdmittedMeta(ctx)
	if !ok || got.Caller != "alice" {
		t.Fatalf("AdmittedMeta = %+v, %v", got, ok)
	}
	c, ok := admitted.CallerFrom(ctx)
	if !ok || c.Principal != "alice" || c.IdempotencyScope != IdempotencyScope(m) {
		t.Fatalf("CallerFrom = %+v, %v", c, ok)
	}
}
