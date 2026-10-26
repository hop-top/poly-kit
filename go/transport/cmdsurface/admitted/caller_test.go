package admitted_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"hop.top/kit/go/transport/cmdsurface/admitted"
	"hop.top/kit/internal/admittedctx"
)

// servedKey is the derivation the serve-lifecycle contract fixes for a
// served key: "served:" and the hex SHA-256 of scope, NUL, key.
func servedKey(scope, key string) string {
	sum := sha256.Sum256([]byte(scope + "\x00" + key))
	return "served:" + hex.EncodeToString(sum[:])
}

func stamp(ctx context.Context, c admitted.Caller) context.Context {
	return context.WithValue(ctx, admittedctx.CallerKey{}, c)
}

func TestScopeIdempotencyKey_EmptyKeyStaysEmpty(t *testing.T) {
	t.Setenv(admitted.EnvIdempotencyScope, "scope-from-parent")
	ctx := stamp(context.Background(), admitted.Caller{IdempotencyScope: "s"})
	if got := admitted.ScopeIdempotencyKey(ctx, ""); got != "" {
		t.Fatalf("empty key scoped to %q, want empty", got)
	}
}

func TestScopeIdempotencyKey_KeyAsTypedOutsideServedRun(t *testing.T) {
	t.Setenv(admitted.EnvIdempotencyScope, "")
	if got := admitted.ScopeIdempotencyKey(context.Background(), "k1"); got != "k1" {
		t.Fatalf("local key = %q, want it as typed", got)
	}
	//nolint:staticcheck // a nil context is outside every served run
	if got := admitted.ScopeIdempotencyKey(nil, "k1"); got != "k1" {
		t.Fatalf("nil-context key = %q, want it as typed", got)
	}
}

func TestScopeIdempotencyKey_EnvFallbackInSubprocessChild(t *testing.T) {
	t.Setenv(admitted.EnvIdempotencyScope, "scope-from-parent")
	got := admitted.ScopeIdempotencyKey(context.Background(), "k1")
	if want := servedKey("scope-from-parent", "k1"); got != want {
		t.Fatalf("child key = %q, want %q", got, want)
	}
}

func TestScopeIdempotencyKey_StampedCallerScopes(t *testing.T) {
	t.Setenv(admitted.EnvIdempotencyScope, "")
	ctx := stamp(context.Background(), admitted.Caller{IdempotencyScope: "scope-a"})
	got := admitted.ScopeIdempotencyKey(ctx, "k1")
	if want := servedKey("scope-a", "k1"); got != want {
		t.Fatalf("served key = %q, want %q", got, want)
	}
}

func TestScopeIdempotencyKey_StampedCallerWinsOverEnv(t *testing.T) {
	t.Setenv(admitted.EnvIdempotencyScope, "scope-from-parent")
	ctx := stamp(context.Background(), admitted.Caller{IdempotencyScope: "scope-a"})
	got := admitted.ScopeIdempotencyKey(ctx, "k1")
	if want := servedKey("scope-a", "k1"); got != want {
		t.Fatalf("served key = %q, want %q (the admitted caller, not the environment)", got, want)
	}
}

func TestScopeIdempotencyKey_TwoCallersNeverMeet(t *testing.T) {
	t.Setenv(admitted.EnvIdempotencyScope, "")
	a := admitted.ScopeIdempotencyKey(stamp(context.Background(), admitted.Caller{IdempotencyScope: "scope-a"}), "k1")
	b := admitted.ScopeIdempotencyKey(stamp(context.Background(), admitted.Caller{IdempotencyScope: "scope-b"}), "k1")
	if a == b {
		t.Fatalf("two callers' k1 map to one store key %q", a)
	}
	if a == "k1" || b == "k1" {
		t.Fatal("a served key met a local one")
	}
}

func TestCallerFrom_OutsideServedRun(t *testing.T) {
	if _, ok := admitted.CallerFrom(context.Background()); ok {
		t.Fatal("caller reported on an unstamped context")
	}
	//nolint:staticcheck // a nil context carries no caller
	if _, ok := admitted.CallerFrom(nil); ok {
		t.Fatal("caller reported on a nil context")
	}
	ctx := context.WithValue(context.Background(), admittedctx.CallerKey{}, "not a caller")
	if _, ok := admitted.CallerFrom(ctx); ok {
		t.Fatal("caller reported for a value of the wrong type")
	}
}

func TestCallerFrom_ReturnsStampedCaller(t *testing.T) {
	want := admitted.Caller{
		Established:      admitted.EstablishedVerified,
		Principal:        "alice",
		Tenant:           "acme",
		Scopes:           []string{"deploy:write"},
		IdempotencyScope: "scope-a",
	}
	got, ok := admitted.CallerFrom(stamp(context.Background(), want))
	if !ok {
		t.Fatal("stamped caller not found")
	}
	if got.Established != want.Established || got.Principal != want.Principal ||
		got.Tenant != want.Tenant || got.IdempotencyScope != want.IdempotencyScope ||
		len(got.Scopes) != 1 || got.Scopes[0] != "deploy:write" {
		t.Fatalf("CallerFrom = %+v, want %+v", got, want)
	}
}

func TestCallerFrom_CopiesScopes(t *testing.T) {
	ctx := stamp(context.Background(), admitted.Caller{
		Established: admitted.EstablishedVerified,
		Principal:   "alice",
		Scopes:      []string{"read", "write"},
	})
	first, _ := admitted.CallerFrom(ctx)
	first.Scopes[0] = "admin"
	second, _ := admitted.CallerFrom(ctx)
	if second.Scopes[0] != "read" {
		t.Fatalf("a reader's edit reached the next reader: scopes %v", second.Scopes)
	}
}
