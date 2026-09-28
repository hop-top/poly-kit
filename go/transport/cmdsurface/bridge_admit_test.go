package cmdsurface

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// admitSink records every audit record the bridge emits.
type admitSink struct {
	mu   sync.Mutex
	invs []Invocation
	res  []Result
	errs []error
}

func (s *admitSink) Emit(_ context.Context, inv Invocation, res Result, err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invs = append(s.invs, inv)
	s.res = append(s.res, res)
	s.errs = append(s.errs, err)
	return nil
}

func (s *admitSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.invs)
}

func (s *admitSink) spec() SinkSpec { return SinkSpec{Sink: s, OnOK: true, OnError: true} }

// streamingRunner emits one stdout line and a done event carrying
// exit, counting how often it was reached.
type streamingRunner struct {
	calls int
	exit  int
	got   Invocation
}

func (r *streamingRunner) Run(context.Context, Invocation) (Result, error) {
	return Result{}, errors.New("not run")
}

func (r *streamingRunner) Stream(_ context.Context, inv Invocation, out chan<- Event) error {
	defer close(out)
	r.calls++
	r.got = inv
	out <- Event{Kind: "stdout", Data: "line"}
	out <- Event{Kind: "done", Data: &Result{ExitCode: r.exit, Stdout: "line\n"}}
	return nil
}

func TestAdmit_RefusesWhatInvokeRefuses(t *testing.T) {
	cases := []struct {
		name string
		inv  Invocation
		want error
	}{
		{"unknown", Invocation{Path: []string{"nope"}, Meta: Meta{Surface: SurfaceREST}}, ErrUnknownCommand},
		{"not enabled", Invocation{Path: []string{"ping"}, Meta: Meta{Surface: SurfaceSSE}}, ErrSurfaceNotEnabled},
		{"destructive", Invocation{Path: []string{"widget", "delete"}, Meta: Meta{Surface: SurfaceREST}}, ErrDestructiveBlocked},
		{"permission", Invocation{Path: []string{"widget", "add"}, Meta: Meta{Surface: SurfaceREST, Caller: "mallory"}}, ErrPermissionDenied},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sink := &admitSink{}
			run := &streamingRunner{}
			b := New(newBridgeTree(), WithRunner(run),
				WithPermission(denyCaller("mallory")), WithSinks(sink.spec()))
			b.Expose("*", SurfaceREST)

			adm, err := b.Admit(context.Background(), c.inv)
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if adm != nil {
				t.Fatal("a refusal must not return an admission")
			}
			if sink.count() != 1 || !errors.Is(sink.errs[0], c.want) {
				t.Fatalf("refusal must be audited once; got %d records %v", sink.count(), sink.errs)
			}

			// Invoke answers the same way: one gate, two entry points.
			_, ierr := b.Invoke(context.Background(), c.inv)
			if !errors.Is(ierr, c.want) {
				t.Fatalf("Invoke err = %v, want %v", ierr, c.want)
			}
			if run.calls != 0 {
				t.Fatalf("runner reached %d times for a refused invocation", run.calls)
			}
		})
	}
}

func TestAdmission_StreamForwardsEventsAndAuditsOutcome(t *testing.T) {
	sink := &admitSink{}
	run := &streamingRunner{exit: 3}
	b := New(newBridgeTree(), WithRunner(run), WithSinks(sink.spec()))
	b.Expose("*", SurfaceREST)

	adm, err := b.Admit(context.Background(), Invocation{
		Path: []string{"widget", "add"},
		Meta: Meta{Surface: SurfaceREST, Caller: "alice"},
	})
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if sink.count() != 0 {
		t.Fatal("admission alone must not audit: nothing has run yet")
	}

	out := make(chan Event, 8)
	if err := adm.Stream(context.Background(), out); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var kinds []string
	for ev := range out { // closed by Stream
		kinds = append(kinds, ev.Kind)
	}
	if len(kinds) != 2 || kinds[0] != "stdout" || kinds[1] != "done" {
		t.Fatalf("events = %v, want [stdout done]", kinds)
	}
	if run.calls != 1 {
		t.Fatalf("runner calls = %d, want 1", run.calls)
	}
	if sink.count() != 1 {
		t.Fatalf("a remote stream must be audited once; got %d", sink.count())
	}
	if sink.res[0].ExitCode != 3 || sink.errs[0] != nil {
		t.Fatalf("audit = exit %d err %v, want exit 3 err nil", sink.res[0].ExitCode, sink.errs[0])
	}
	if sink.invs[0].Meta.Caller != "alice" {
		t.Fatalf("audit caller = %q, want alice", sink.invs[0].Meta.Caller)
	}
}

func TestAdmission_StreamAuditsCancellation(t *testing.T) {
	sink := &admitSink{}
	run := &fakeStreamRunner{stream: func(ctx context.Context, _ Invocation, out chan<- Event) error {
		defer close(out)
		<-ctx.Done()
		out <- Event{Kind: "done", Data: &Result{ExitCode: 1}}
		return ctx.Err()
	}}
	b := New(newBridgeTree(), WithRunner(run), WithSinks(sink.spec()))
	b.Expose("*", SurfaceREST)

	adm, err := b.Admit(context.Background(), Invocation{
		Path: []string{"ping"}, Meta: Meta{Surface: SurfaceREST},
	})
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan Event, 8)
	errc := make(chan error, 1)
	go func() { errc <- adm.Stream(ctx, out) }()
	cancel()
	for range out {
	}
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("Stream err = %v, want context.Canceled", err)
	}
	if sink.count() != 1 || !errors.Is(sink.errs[0], context.Canceled) {
		t.Fatalf("cancellation must be audited; got %v", sink.errs)
	}
}

type fakeStreamRunner struct {
	stream func(context.Context, Invocation, chan<- Event) error
}

func (f *fakeStreamRunner) Run(context.Context, Invocation) (Result, error) {
	return Result{}, errors.New("not run")
}

func (f *fakeStreamRunner) Stream(ctx context.Context, inv Invocation, out chan<- Event) error {
	return f.stream(ctx, inv, out)
}

func TestAdmission_ForwardsIdempotencyKey(t *testing.T) {
	run := &streamingRunner{}
	b := New(newBridgeTree(), WithRunner(run))
	b.Expose("*", SurfaceREST)
	adm, err := b.Admit(context.Background(), Invocation{
		Path: []string{"ping"}, Meta: Meta{Surface: SurfaceREST, IdempotencyKey: "k1"},
	})
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	// ping has no --idempotency-key flag: nothing is forwarded, and
	// the admitted invocation keeps the caller's Meta.
	if got := adm.Invocation(); got.Meta.IdempotencyKey != "k1" || got.Flags[idempotencyKeyFlag] != nil {
		t.Fatalf("admitted invocation = %+v", got)
	}
	if got := adm.Invocation(); got.Meta.RequestedAt.IsZero() {
		t.Fatal("admission must stamp RequestedAt like Invoke does")
	}
}
