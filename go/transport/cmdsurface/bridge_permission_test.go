package cmdsurface

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/ai/cmdreflect"
)

// denyCaller refuses one named caller and lets everyone else through.
func denyCaller(name string) PermissionFunc {
	return func(_ context.Context, meta Meta, _ *Leaf) PermissionDecision {
		if meta.Caller == name {
			return PermissionDecision{Reason: "caller " + name + " is not entitled"}
		}
		return PermissionDecision{Allowed: true}
	}
}

// countingRunner records whether the Runner was reached.
func countingRunner(calls *int, got *Invocation) Runner {
	return &fakeRunner{run: func(_ context.Context, inv Invocation) (Result, error) {
		*calls++
		if got != nil {
			*got = inv
		}
		return Result{ExitCode: 0, Stdout: "ran"}, nil
	}}
}

func TestInvoke_DefaultPermissionAllowsEverything(t *testing.T) {
	calls := 0
	b := New(newBridgeTree(), WithRunner(countingRunner(&calls, nil)))
	b.Expose("*", SurfaceREST)

	_, err := b.Invoke(context.Background(), Invocation{
		Path: []string{"widget", "add"},
		Meta: Meta{Surface: SurfaceREST, Caller: "anyone"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("runner calls = %d, want 1", calls)
	}
}

func TestInvoke_PermissionDeniedCarriesReasonAndSkipsRunner(t *testing.T) {
	calls := 0
	b := New(
		newBridgeTree(),
		WithRunner(countingRunner(&calls, nil)),
		WithPermission(denyCaller("mallory")),
	)
	b.Expose("*", SurfaceREST)

	_, err := b.Invoke(context.Background(), Invocation{
		Path: []string{"widget", "add"},
		Meta: Meta{Surface: SurfaceREST, Caller: "mallory"},
	})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("err = %v, want ErrPermissionDenied", err)
	}
	if !strings.Contains(err.Error(), "caller mallory is not entitled") {
		t.Fatalf("reason missing from %q", err.Error())
	}
	if !strings.Contains(err.Error(), "widget add on rest") {
		t.Fatalf("leaf and surface missing from %q", err.Error())
	}
	if calls != 0 {
		t.Fatalf("runner must not run a denied invocation; calls = %d", calls)
	}

	// The same gate, a different caller: allowed.
	_, err = b.Invoke(context.Background(), Invocation{
		Path: []string{"widget", "add"},
		Meta: Meta{Surface: SurfaceREST, Caller: "alice"},
	})
	if err != nil {
		t.Fatalf("alice must be allowed: %v", err)
	}
	if calls != 1 {
		t.Fatalf("runner calls = %d, want 1", calls)
	}
}

func TestInvoke_GateOrderDestructiveCeilingBeforePermission(t *testing.T) {
	// A destructive leaf on a surface the policy does not name is
	// refused by the ceiling; the permission gate is never asked, so
	// its reason must not leak into the answer.
	asked := false
	b := New(
		newBridgeTree(),
		WithRunner(countingRunner(new(int), nil)),
		WithPermission(func(context.Context, Meta, *Leaf) PermissionDecision {
			asked = true
			return PermissionDecision{Reason: "should not be consulted"}
		}),
	)
	b.Expose("*", SurfaceREST)

	_, err := b.Invoke(context.Background(), Invocation{
		Path: []string{"widget", "delete"},
		Meta: Meta{Surface: SurfaceREST},
	})
	if !errors.Is(err, ErrDestructiveBlocked) {
		t.Fatalf("err = %v, want ErrDestructiveBlocked", err)
	}
	if asked {
		t.Fatal("permission gate must run after the destructive ceiling, not before")
	}

	// Enablement also precedes permission.
	_, err = b.Invoke(context.Background(), Invocation{
		Path: []string{"widget", "add"},
		Meta: Meta{Surface: SurfaceWS},
	})
	if !errors.Is(err, ErrSurfaceNotEnabled) {
		t.Fatalf("err = %v, want ErrSurfaceNotEnabled", err)
	}
	if asked {
		t.Fatal("permission gate must run after enablement, not before")
	}
}

func TestInvoke_PermissionGateAppliesOnEverySurface(t *testing.T) {
	// The gate cannot be bypassed by choosing a surface: a caller
	// denied over REST is denied over RPC and MCP too.
	b := New(
		newBridgeTree(),
		WithRunner(countingRunner(new(int), nil)),
		WithPermission(denyCaller("mallory")),
	)
	b.Expose("*", SurfaceREST, SurfaceRPC, SurfaceMCP)

	for _, s := range []Surface{SurfaceREST, SurfaceRPC, SurfaceMCP} {
		_, err := b.Invoke(context.Background(), Invocation{
			Path: []string{"widget", "add"},
			Meta: Meta{Surface: s, Caller: "mallory"},
		})
		if !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("%s: err = %v, want ErrPermissionDenied", s, err)
		}
	}
}

func TestInvoke_AuditsRefusalsAndRemoteExecutions(t *testing.T) {
	rec := &sinkRecorder{}
	b := New(
		newBridgeTree(),
		WithRunner(countingRunner(new(int), nil)),
		WithPermission(denyCaller("mallory")),
		WithSinks(SinkSpec{Sink: rec, OnOK: true, OnError: true}),
	)
	b.Expose("*", SurfaceREST)

	meta := Meta{
		Surface:   SurfaceREST,
		Caller:    "mallory",
		Tenant:    "acme",
		RequestID: "req-1",
		TraceID:   "trace-1",
	}
	ctx := context.Background()

	// Unknown command, surface not enabled, destructive ceiling,
	// permission: four refusals, four records, each with the error
	// that refused it and the Meta the transport supplied.
	_, _ = b.Invoke(ctx, Invocation{Path: []string{"nosuch"}, Meta: meta})
	_, _ = b.Invoke(ctx, Invocation{Path: []string{"widget", "add"}, Meta: Meta{Surface: SurfaceWS}})
	_, _ = b.Invoke(ctx, Invocation{Path: []string{"widget", "delete"}, Meta: meta})
	_, _ = b.Invoke(ctx, Invocation{Path: []string{"widget", "add"}, Meta: meta})
	// And one execution.
	meta.Caller = "alice"
	_, _ = b.Invoke(ctx, Invocation{Path: []string{"widget", "add"}, Meta: meta})

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.calls) != 5 {
		t.Fatalf("records = %d, want 5", len(rec.calls))
	}
	wantErr := []error{ErrUnknownCommand, ErrSurfaceNotEnabled, ErrDestructiveBlocked, ErrPermissionDenied}
	for i, want := range wantErr {
		if !errors.Is(rec.calls[i].err, want) {
			t.Fatalf("record %d err = %v, want %v", i, rec.calls[i].err, want)
		}
	}
	denied := rec.calls[3]
	if denied.inv.Meta.Caller != "mallory" || denied.inv.Meta.Tenant != "acme" ||
		denied.inv.Meta.RequestID != "req-1" || denied.inv.Meta.TraceID != "trace-1" ||
		denied.inv.Meta.Surface != SurfaceREST || strings.Join(denied.inv.Path, " ") != "widget add" {
		t.Fatalf("denied record lost provenance: %+v", denied.inv.Meta)
	}
	if denied.inv.Meta.RequestedAt.IsZero() {
		t.Fatal("audit record must carry a timestamp even when the surface left it zero")
	}
	ran := rec.calls[4]
	if ran.err != nil || ran.res.ExitCode != 0 || ran.inv.Meta.Caller != "alice" {
		t.Fatalf("execution record = %+v / %v, want alice exit 0", ran.res, ran.err)
	}
}

func TestInvoke_DoesNotAuditLocalSurfaces(t *testing.T) {
	rec := &sinkRecorder{}
	b := New(
		newBridgeTree(),
		WithRunner(countingRunner(new(int), nil)),
		WithSinks(SinkSpec{Sink: rec, OnOK: true, OnError: true}),
	)

	ctx := context.Background()
	_, _ = b.Invoke(ctx, Invocation{Path: []string{"widget", "add"}, Meta: Meta{Surface: SurfaceCLI}})
	_, _ = b.Invoke(ctx, Invocation{Path: []string{"widget", "add"}, Meta: Meta{Surface: SurfaceLib}})
	_, _ = b.Invoke(ctx, Invocation{Path: []string{"widget", "add"}}) // defaults to lib

	if rec.count() != 0 {
		t.Fatalf("local surfaces must not emit audit records; got %d", rec.count())
	}
}

func TestInvoke_ForwardsIdempotencyKeyToRegisteredFlag(t *testing.T) {
	root := newBridgeTree()
	add, _, err := root.Find([]string{"widget", "add"})
	if err != nil {
		t.Fatal(err)
	}
	add.Flags().String(idempotencyKeyFlag, "", "replay key")

	var got Invocation
	b := New(root, WithRunner(countingRunner(new(int), &got)))
	b.Expose("*", SurfaceREST)

	caller := map[string]any{"x": 1}
	_, err = b.Invoke(context.Background(), Invocation{
		Path:  []string{"widget", "add"},
		Flags: caller,
		Meta:  Meta{Surface: SurfaceREST, IdempotencyKey: "k-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Flags[idempotencyKeyFlag] != "k-1" {
		t.Fatalf("flag = %v, want k-1", got.Flags[idempotencyKeyFlag])
	}
	if got.Flags["x"] != 1 {
		t.Fatal("other flags must be preserved")
	}
	if _, leaked := caller[idempotencyKeyFlag]; leaked {
		t.Fatal("the caller's Flags map must not be mutated")
	}
	if got.Meta.IdempotencyKey != "k-1" {
		t.Fatal("the key must also remain on Meta for sinks")
	}

	// An explicit flag wins over the header-derived key.
	_, err = b.Invoke(context.Background(), Invocation{
		Path:  []string{"widget", "add"},
		Flags: map[string]any{idempotencyKeyFlag: "explicit"},
		Meta:  Meta{Surface: SurfaceREST, IdempotencyKey: "k-2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Flags[idempotencyKeyFlag] != "explicit" {
		t.Fatalf("flag = %v, want explicit", got.Flags[idempotencyKeyFlag])
	}

	// A leaf without the flag gets nothing injected: cobra would
	// reject an unknown flag.
	_, err = b.Invoke(context.Background(), Invocation{
		Path: []string{"ping"},
		Meta: Meta{Surface: SurfaceREST, IdempotencyKey: "k-3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, present := got.Flags[idempotencyKeyFlag]; present {
		t.Fatal("a leaf without the flag must not receive it")
	}
}

func TestInvoke_StampsRequestedAtWhenZero(t *testing.T) {
	var got Invocation
	b := New(newBridgeTree(), WithRunner(countingRunner(new(int), &got)))
	b.Expose("*", SurfaceREST)

	before := time.Now()
	_, err := b.Invoke(context.Background(), Invocation{
		Path: []string{"ping"}, Meta: Meta{Surface: SurfaceREST},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Meta.RequestedAt.Before(before) {
		t.Fatalf("RequestedAt = %v, want stamped at invoke", got.Meta.RequestedAt)
	}

	// A surface that set it keeps its value.
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	_, err = b.Invoke(context.Background(), Invocation{
		Path: []string{"ping"}, Meta: Meta{Surface: SurfaceREST, RequestedAt: at},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Meta.RequestedAt.Equal(at) {
		t.Fatalf("RequestedAt = %v, want %v preserved", got.Meta.RequestedAt, at)
	}
}

func TestBridge_PermissionAndAuditDirect(t *testing.T) {
	rec := &sinkRecorder{}
	b := New(
		newBridgeTree(),
		WithPermission(func(_ context.Context, _ Meta, leaf *Leaf) PermissionDecision {
			if leaf.PathKey() == "widget add" {
				return PermissionDecision{Reason: "closed", CallerIndependent: true}
			}
			return PermissionDecision{Allowed: true}
		}),
		WithSinks(SinkSpec{Sink: rec, OnError: true}),
	)

	// Permission with a nil leaf is a permit: there is nothing to
	// decide about.
	if dec := b.Permission(context.Background(), Meta{}, nil); !dec.Allowed {
		t.Fatal("nil leaf must be allowed")
	}
	var add *Leaf
	for _, l := range b.Leaves() {
		if l.PathKey() == "widget add" {
			add = l
		}
	}
	dec := b.Permission(context.Background(), Meta{Surface: SurfaceREST}, add)
	if dec.Allowed || !dec.CallerIndependent || dec.Reason != "closed" {
		t.Fatalf("decision = %+v", dec)
	}

	// Audit reaches sinks with the transport's own refusal.
	b.Audit(context.Background(), Invocation{Path: []string{"x"}, Meta: Meta{Surface: SurfaceREST}},
		Result{}, ErrAuthRefused)
	if rec.count() != 1 {
		t.Fatalf("records = %d, want 1", rec.count())
	}
	if !errors.Is(rec.calls[0].err, ErrAuthRefused) {
		t.Fatalf("err = %v", rec.calls[0].err)
	}
}

func TestReasonPermissionDeniedSpelling(t *testing.T) {
	// The reason is a wire constant clients switch on; it must keep
	// the reflector's hyphenated-lowercase spelling.
	if ReasonPermissionDenied != "permission-denied" {
		t.Fatalf("ReasonPermissionDenied = %q", ReasonPermissionDenied)
	}
}

func TestInvocationStringCarriesTenantAndRequest(t *testing.T) {
	s := Invocation{
		Path: []string{"widget", "add"},
		Meta: Meta{Surface: SurfaceREST, Caller: "alice", Tenant: "acme", RequestID: "r1", TraceID: "t1"},
	}.String()
	for _, want := range []string{"caller=alice", "tenant=acme", "request=r1", "trace=t1"} {
		if !strings.Contains(s, want) {
			t.Fatalf("%q missing %q", s, want)
		}
	}
}

func TestInvoke_RefusesInteractiveBeforeCeilingAndPermission(t *testing.T) {
	root := newBridgeTree()
	root.AddCommand(&cobra.Command{
		Use:         "shell",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "interactive"},
	})
	calls := 0
	asked := false
	rec := &sinkRecorder{}
	b := New(
		root,
		WithRunner(countingRunner(&calls, nil)),
		WithPermission(func(context.Context, Meta, *Leaf) PermissionDecision {
			asked = true
			return PermissionDecision{Allowed: true}
		}),
		WithSinks(SinkSpec{Sink: rec, OnError: true}),
	)
	b.Expose("*", SurfaceRPC)

	// The leaf exists on the bridge — AllowInteractive keeps it
	// describable — but no transport may run it.
	var found bool
	for _, l := range b.Leaves() {
		found = found || l.PathKey() == "shell"
	}
	if !found {
		t.Fatal("interactive command must remain a leaf for discovery")
	}

	_, err := b.Invoke(context.Background(), Invocation{
		Path: []string{"shell"}, Meta: Meta{Surface: SurfaceRPC},
	})
	if !errors.Is(err, ErrNotInvocable) {
		t.Fatalf("err = %v, want ErrNotInvocable", err)
	}
	if !strings.Contains(err.Error(), "shell on rpc is interactive") {
		t.Fatalf("reason missing from %q", err.Error())
	}
	if calls != 0 {
		t.Fatal("the runner must not be reached")
	}
	if asked {
		t.Fatal("the permission gate must not be consulted for a leaf that can never run")
	}
	if rec.count() != 1 || !errors.Is(rec.calls[0].err, ErrNotInvocable) {
		t.Fatalf("the refusal must be audited; records = %d", rec.count())
	}
}

func TestNotInvocableReasonOrdersSelfHostingFirst(t *testing.T) {
	// A self-hosting leaf is refused as such even when it is also
	// destructive, so the answer names the rule nothing can lift
	// rather than the ceiling a policy could.
	cases := []struct {
		name string
		leaf *Leaf
		want cmdreflect.NonInvocableReason
	}{
		{"nil leaf", nil, cmdreflect.ReasonNone},
		{"no descriptor", &Leaf{}, cmdreflect.ReasonNone},
		{"plain", &Leaf{Descriptor: &cmdreflect.Descriptor{}}, cmdreflect.ReasonNone},
		{"interactive", &Leaf{Descriptor: &cmdreflect.Descriptor{
			Safety: cmdreflect.Safety{Tier: cmdreflect.TierInteractive},
		}}, cmdreflect.ReasonInteractive},
		{"self-hosting", &Leaf{Descriptor: &cmdreflect.Descriptor{
			Surface: cmdreflect.SurfaceMeta{SelfHosting: true},
		}}, cmdreflect.ReasonSelfHosting},
		{"self-hosting and destructive", &Leaf{Descriptor: &cmdreflect.Descriptor{
			Surface: cmdreflect.SurfaceMeta{SelfHosting: true},
			Safety:  cmdreflect.Safety{Tier: cmdreflect.TierDestructiveShared},
		}}, cmdreflect.ReasonSelfHosting},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := notInvocableReason(c.leaf); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}
