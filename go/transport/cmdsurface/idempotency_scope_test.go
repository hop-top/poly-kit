package cmdsurface

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
)

func TestScopeIdempotencyKey_OutsideAServedInvocationIsTheKey(t *testing.T) {
	t.Setenv(EnvIdempotencyScope, "")
	if got := ScopeIdempotencyKey(context.Background(), "k1"); got != "k1" {
		t.Errorf("ScopeIdempotencyKey = %q, want the key unchanged", got)
	}
	//nolint:staticcheck // a nil context is what a command run without one carries
	if got := ScopeIdempotencyKey(nil, "k1"); got != "k1" {
		t.Errorf("ScopeIdempotencyKey(nil ctx) = %q, want the key unchanged", got)
	}
}

func TestScopeIdempotencyKey_EmptyKeyStaysEmpty(t *testing.T) {
	ctx := ContextWithMeta(context.Background(), Meta{Surface: SurfaceREST, Caller: "alice"})
	if got := ScopeIdempotencyKey(ctx, ""); got != "" {
		t.Errorf("ScopeIdempotencyKey(empty) = %q, want empty", got)
	}
}

func TestScopeIdempotencyKey_ServedKeyNeverMeetsTheLocalKey(t *testing.T) {
	t.Setenv(EnvIdempotencyScope, "")
	for _, m := range []Meta{
		{},
		{Surface: SurfaceLib},
		{Surface: SurfaceSocket, Established: EstablishedTransport},
		{Surface: SurfaceREST, Caller: "alice", Established: EstablishedVerified},
	} {
		got := ScopeIdempotencyKey(ContextWithMeta(context.Background(), m), "k1")
		if got == "k1" {
			t.Errorf("meta %+v: served key equals the local key", m)
		}
	}
}

func TestScopeIdempotencyKey_ScopedByPrincipal(t *testing.T) {
	t.Setenv(EnvIdempotencyScope, "")
	cases := []struct {
		name     string
		a, b     Meta
		wantSame bool
	}{
		{"same established caller", Meta{Surface: SurfaceREST, Caller: "alice", Established: EstablishedVerified},
			Meta{Surface: SurfaceREST, Caller: "alice", Established: EstablishedVerified}, true},
		{"other established caller", Meta{Surface: SurfaceREST, Caller: "alice", Established: EstablishedVerified},
			Meta{Surface: SurfaceREST, Caller: "bob", Established: EstablishedVerified}, false},
		{"established caller, other tenant",
			Meta{Surface: SurfaceREST, Caller: "alice", Tenant: "acme", Established: EstablishedVerified},
			Meta{Surface: SurfaceRPC, Caller: "alice", Tenant: "globex", Established: EstablishedVerified}, false},
		{"established caller on another surface",
			Meta{Surface: SurfaceREST, Caller: "alice", Established: EstablishedVerified},
			Meta{Surface: SurfaceRPC, Caller: "alice", Established: EstablishedVerified}, true},
		{"a claim never reaches an established caller's record",
			Meta{Surface: SurfaceREST, Caller: "alice", Established: EstablishedVerified},
			Meta{Surface: SurfaceREST, Caller: "alice"}, false},
		{"same claimed caller", Meta{Surface: SurfaceREST, Caller: "alice"}, Meta{Surface: SurfaceREST, Caller: "alice"}, true},
		{"other claimed caller", Meta{Surface: SurfaceREST, Caller: "alice"}, Meta{Surface: SurfaceREST, Caller: "bob"}, false},
		{"claim on another surface", Meta{Surface: SurfaceREST, Caller: "alice"}, Meta{Surface: SurfaceSocket, Caller: "alice"}, false},
		{"anonymous, same host, new port",
			Meta{Surface: SurfaceREST, Extra: map[string]string{"remote_addr": "10.0.0.7:5001"}},
			Meta{Surface: SurfaceREST, Extra: map[string]string{"remote_addr": "10.0.0.7:6002"}}, true},
		{"anonymous, other host",
			Meta{Surface: SurfaceREST, Extra: map[string]string{"remote_addr": "10.0.0.7:5001"}},
			Meta{Surface: SurfaceREST, Extra: map[string]string{"remote_addr": "10.0.0.8:5001"}}, false},
		{"owner-only socket", Meta{Surface: SurfaceSocket, Established: EstablishedTransport},
			Meta{Surface: SurfaceSocket, Established: EstablishedTransport}, true},
		{"request ids and traces do not scope",
			Meta{Surface: SurfaceREST, Caller: "alice", Established: EstablishedVerified, RequestID: "r1", TraceID: "t1"},
			Meta{Surface: SurfaceREST, Caller: "alice", Established: EstablishedVerified, RequestID: "r2", TraceID: "t2"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := ScopeIdempotencyKey(ContextWithMeta(context.Background(), c.a), "k1")
			b := ScopeIdempotencyKey(ContextWithMeta(context.Background(), c.b), "k1")
			if (a == b) != c.wantSame {
				t.Errorf("same scope = %v, want %v (a=%s b=%s)", a == b, c.wantSame, a, b)
			}
		})
	}
}

// The environment is how a SubprocessRunner child learns its scope:
// the child's key must be the key the in-process runner would use.
func TestScopeIdempotencyKey_FromTheEnvironment(t *testing.T) {
	m := Meta{Surface: SurfaceREST, Caller: "alice", Established: EstablishedVerified}
	want := ScopeIdempotencyKey(ContextWithMeta(context.Background(), m), "k1")

	t.Setenv(EnvIdempotencyScope, IdempotencyScope(m))
	if got := ScopeIdempotencyKey(context.Background(), "k1"); got != want {
		t.Errorf("env-scoped key = %q, want %q", got, want)
	}
	// The invocation on the context wins over an inherited variable.
	other := Meta{Surface: SurfaceREST, Caller: "bob", Established: EstablishedVerified}
	if got := ScopeIdempotencyKey(ContextWithMeta(context.Background(), other), "k1"); got == want {
		t.Error("an inherited scope overrode the invocation's own")
	}
}

func TestInProcessRunner_CarriesMetaToTheCommand(t *testing.T) {
	var seen []Meta
	root := &cobra.Command{Use: "tool"}
	root.AddCommand(&cobra.Command{
		Use: "who",
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, ok := MetaFromContext(cmd.Context())
			if !ok {
				t.Error("command context carries no invocation Meta")
			}
			seen = append(seen, m)
			return nil
		},
	})
	r := InProcessRunner(root)
	meta := Meta{Surface: SurfaceREST, Caller: "alice", Established: EstablishedVerified}
	inv := Invocation{Path: []string{"who"}, Meta: meta}

	if _, err := r.Run(context.Background(), inv); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := make(chan Event, 8)
	if err := r.Stream(context.Background(), inv, out); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range out {
	}
	if len(seen) != 2 {
		t.Fatalf("command ran %d times, want 2", len(seen))
	}
	for i, m := range seen {
		if m.Caller != "alice" || m.Established != EstablishedVerified || m.Surface != SurfaceREST {
			t.Errorf("run %d: Meta = %+v, want the invocation's", i, m)
		}
	}
}

func TestSubprocessRunner_PassesIdempotencyScopeToChild(t *testing.T) {
	sh := findSh(t)
	if sh == "" {
		t.Skip("no POSIX shell available")
	}
	// A scope the server inherited must be replaced.
	t.Setenv(EnvIdempotencyScope, "stale")
	meta := Meta{Surface: SurfaceREST, Caller: "alice", Established: EstablishedVerified}
	inv := Invocation{
		Args:    []string{"-c", `printf '%s' "${` + EnvIdempotencyScope + `:--}"`},
		ownArgv: true,
		Meta:    meta,
	}
	want := IdempotencyScope(meta)

	res, err := SubprocessRunner(sh).Run(context.Background(), inv)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("Run: %v exit=%d stderr=%q", err, res.ExitCode, res.Stderr)
	}
	if res.Stdout != want {
		t.Errorf("Run child scope = %q, want %q", res.Stdout, want)
	}
	out := make(chan Event, 8)
	if err := SubprocessRunner(sh).Stream(context.Background(), inv, out); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var got string
	for ev := range out {
		if ev.Kind == "stdout" {
			got += ev.Data.(string)
		}
	}
	if got != want {
		t.Errorf("Stream child scope = %q, want %q", got, want)
	}
}
