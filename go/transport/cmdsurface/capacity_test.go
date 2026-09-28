package cmdsurface_test

// The capacity gate, invocation-plane slot 11: a call past every gate
// takes an in-flight slot or waits in a bounded first-come-first-served
// queue, and is refused as overloaded when both are full. The deadline
// is armed before the call queues.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/cmdsurface"
)

// blockRunner reports each run's request id on started, then holds
// the run until proceed is received from or the run's context ends.
type blockRunner struct {
	started chan string
	proceed chan struct{}

	mu        sync.Mutex
	deadlines map[string]time.Time
}

func newBlockRunner() *blockRunner {
	return &blockRunner{
		started:   make(chan string, 16),
		proceed:   make(chan struct{}),
		deadlines: map[string]time.Time{},
	}
}

func (r *blockRunner) Run(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
	if dl, ok := ctx.Deadline(); ok {
		r.mu.Lock()
		r.deadlines[inv.Meta.RequestID] = dl
		r.mu.Unlock()
	}
	r.started <- inv.Meta.RequestID
	select {
	case <-r.proceed:
		return cmdsurface.Result{Stdout: "ran\n"}, nil
	case <-ctx.Done():
		return cmdsurface.Result{}, ctx.Err()
	}
}

func (r *blockRunner) Stream(ctx context.Context, inv cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
	defer close(out)
	res, err := r.Run(ctx, inv)
	out <- cmdsurface.Event{Kind: "done", Data: &res, At: time.Now()}
	return err
}

func (r *blockRunner) deadline(id string) (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	dl, ok := r.deadlines[id]
	return dl, ok
}

// capBridge builds a bridge over gateTree with every leaf exposed on
// REST, the capacity gate at cfg, and a recording sink.
func capBridge(run cmdsurface.Runner, cfg cmdsurface.Concurrency, opts ...cmdsurface.Option) (*cmdsurface.Bridge, *gateSink) {
	sink := &gateSink{}
	opts = append([]cmdsurface.Option{
		cmdsurface.WithRunner(run),
		cmdsurface.WithSinks(cmdsurface.SinkSpec{Sink: sink, OnOK: true, OnError: true}),
		cmdsurface.WithConcurrency(cfg),
	}, opts...)
	b := cmdsurface.New(gateTree(), opts...)
	b.Expose("*", cmdsurface.SurfaceREST)
	return b, sink
}

// capInv is a REST call to "open" carrying id as its request id.
func capInv(id string) cmdsurface.Invocation {
	return cmdsurface.Invocation{
		Path: []string{"open"},
		Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceREST, RequestID: id},
	}
}

type outcome struct {
	res cmdsurface.Result
	err error
}

// invokeAsync runs b.Invoke(ctx, capInv(id)) in the background.
func invokeAsync(ctx context.Context, b *cmdsurface.Bridge, id string) <-chan outcome {
	done := make(chan outcome, 1)
	go func() {
		res, err := b.Invoke(ctx, capInv(id))
		done <- outcome{res, err}
	}()
	return done
}

// waitLoad waits until the gate holds inflight running and queued
// waiting.
func waitLoad(t *testing.T, b *cmdsurface.Bridge, inflight, queued int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var got cmdsurface.CapacityLoad
	for time.Now().Before(deadline) {
		got, _ = b.Capacity()
		if got.Inflight == inflight && got.Queued == queued {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("load = %+v, want %d in flight and %d queued", got, inflight, queued)
}

func awaitStart(t *testing.T, run *blockRunner, want string) {
	t.Helper()
	select {
	case got := <-run.started:
		if got != want {
			t.Fatalf("started %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("%s never started", want)
	}
}

func awaitOutcome(t *testing.T, done <-chan outcome) outcome {
	t.Helper()
	select {
	case o := <-done:
		return o
	case <-time.After(2 * time.Second):
		t.Fatal("call never returned")
		return outcome{}
	}
}

func TestCapacity_QueuesThenRefusesOverloaded(t *testing.T) {
	run := newBlockRunner()
	b, sink := capBridge(run, cmdsurface.Concurrency{MaxInflight: 1, MaxQueue: 1})
	ctx := context.Background()

	first := invokeAsync(ctx, b, "c1")
	awaitStart(t, run, "c1")
	second := invokeAsync(ctx, b, "c2")
	waitLoad(t, b, 1, 1)

	_, err := b.Invoke(ctx, capInv("c3"))
	var oe *cmdsurface.OverloadedError
	if !errors.As(err, &oe) || !errors.Is(err, cmdsurface.ErrOverloaded) {
		t.Fatalf("third call: err = %v, want an *OverloadedError", err)
	}
	if oe.Path != "open" || oe.Surface != cmdsurface.SurfaceREST || oe.MaxInflight != 1 || oe.MaxQueue != 1 {
		t.Errorf("refusal = %+v", oe)
	}
	if wait, ok := cmdsurface.RetryAfter(err); !ok || wait < time.Second {
		t.Errorf("RetryAfter = %v, %v; want at least a second", wait, ok)
	}
	if recs := sink.records(); len(recs) != 1 || !errors.Is(recs[0], cmdsurface.ErrOverloaded) {
		t.Fatalf("audit = %v, want the one overloaded refusal", recs)
	}

	run.proceed <- struct{}{}
	if o := awaitOutcome(t, first); o.err != nil {
		t.Fatalf("first call: %v", o.err)
	}
	awaitStart(t, run, "c2")
	run.proceed <- struct{}{}
	if o := awaitOutcome(t, second); o.err != nil || o.res.Stdout != "ran\n" {
		t.Fatalf("queued call: %+v", o)
	}
	waitLoad(t, b, 0, 0)
}

func TestCapacity_QueueDrainsInOrder(t *testing.T) {
	run := newBlockRunner()
	b, _ := capBridge(run, cmdsurface.Concurrency{MaxInflight: 1, MaxQueue: 4})
	ctx := context.Background()

	var calls []<-chan outcome
	calls = append(calls, invokeAsync(ctx, b, "c1"))
	awaitStart(t, run, "c1")
	ids := []string{"c2", "c3", "c4", "c5"}
	for i, id := range ids {
		calls = append(calls, invokeAsync(ctx, b, id))
		waitLoad(t, b, 1, i+1) // each joins behind the last
	}
	for _, id := range ids {
		run.proceed <- struct{}{}
		awaitStart(t, run, id)
	}
	run.proceed <- struct{}{}
	for i, done := range calls {
		if o := awaitOutcome(t, done); o.err != nil {
			t.Fatalf("call %d: %v", i+1, o.err)
		}
	}
	waitLoad(t, b, 0, 0)
}

func TestCapacity_CancelWhileQueuedFreesThePlace(t *testing.T) {
	run := newBlockRunner()
	var mu sync.Mutex
	var queued, peak int
	observe := func(_ context.Context, inv cmdsurface.Invocation, delta int) {
		mu.Lock()
		defer mu.Unlock()
		if inv.Meta.Surface != cmdsurface.SurfaceREST {
			t.Errorf("observer saw surface %q", inv.Meta.Surface)
		}
		queued += delta
		peak = max(peak, queued)
	}
	b, sink := capBridge(run, cmdsurface.Concurrency{MaxInflight: 1, MaxQueue: 1},
		cmdsurface.WithQueueObserver(observe))
	release := cmdsurface.TestingHoldSlots(b)

	ctx, cancel := context.WithCancel(context.Background())
	gone := invokeAsync(ctx, b, "gone")
	waitLoad(t, b, 1, 1)
	cancel()
	o := awaitOutcome(t, gone)
	if !errors.Is(o.err, context.Canceled) {
		t.Fatalf("canceled call: err = %v, want context.Canceled", o.err)
	}
	waitLoad(t, b, 1, 0)
	if recs := sink.records(); len(recs) != 1 || !errors.Is(recs[0], context.Canceled) {
		t.Errorf("audit = %v, want the canceled call", recs)
	}

	// Its place is free again: the next call queues instead of being
	// refused, and runs once the slot frees.
	next := invokeAsync(context.Background(), b, "next")
	waitLoad(t, b, 1, 1)
	release()
	awaitStart(t, run, "next")
	run.proceed <- struct{}{}
	if o := awaitOutcome(t, next); o.err != nil {
		t.Fatalf("next call: %v", o.err)
	}
	select {
	case id := <-run.started:
		t.Fatalf("the canceled call ran: %s", id)
	default:
	}
	mu.Lock()
	defer mu.Unlock()
	if queued != 0 || peak != 1 {
		t.Errorf("observer: net %d, peak %d; want 0 and 1", queued, peak)
	}
}

func TestCapacity_DeadlineCoversQueueWait(t *testing.T) {
	t.Run("outwaited", func(t *testing.T) {
		run := newBlockRunner()
		b, sink := capBridge(run, cmdsurface.Concurrency{MaxInflight: 1, MaxQueue: 1},
			cmdsurface.WithCommandTimeout(80*time.Millisecond))
		defer cmdsurface.TestingHoldSlots(b)()

		start := time.Now()
		err := awaitOutcome(t, invokeAsync(context.Background(), b, "late")).err
		if !errors.Is(err, cmdsurface.ErrDeadlineExceeded) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want ErrDeadlineExceeded", err)
		}
		if !strings.Contains(err.Error(), "waited past its 80ms deadline") {
			t.Errorf("err = %q, want it to say the call waited", err)
		}
		if el := time.Since(start); el < 80*time.Millisecond {
			t.Errorf("returned after %v, before the deadline", el)
		}
		select {
		case id := <-run.started:
			t.Fatalf("a call that outwaited its deadline ran: %s", id)
		default:
		}
		waitLoad(t, b, 1, 0)
		if recs := sink.records(); len(recs) != 1 || !errors.Is(recs[0], cmdsurface.ErrDeadlineExceeded) {
			t.Errorf("audit = %v, want the deadline", recs)
		}
	})
	t.Run("armed at queue entry", func(t *testing.T) {
		const bound = 300 * time.Millisecond
		const wait = 150 * time.Millisecond
		run := newBlockRunner()
		b, _ := capBridge(run, cmdsurface.Concurrency{MaxInflight: 1, MaxQueue: 1},
			cmdsurface.WithCommandTimeout(bound))
		release := cmdsurface.TestingHoldSlots(b)

		start := time.Now()
		done := invokeAsync(context.Background(), b, "queued")
		waitLoad(t, b, 1, 1)
		time.Sleep(wait)
		release()
		awaitStart(t, run, "queued")
		dl, ok := run.deadline("queued")
		if !ok {
			t.Fatal("the run carried no deadline")
		}
		// Armed when the call queued, not when it started to run: a
		// deadline armed at the run would land past start+wait+bound.
		if dl.After(start.Add(bound + wait/2)) {
			t.Errorf("deadline %v after start, want about %v: the wait did not count",
				dl.Sub(start), bound)
		}
		o := awaitOutcome(t, done)
		if !errors.Is(o.err, cmdsurface.ErrDeadlineExceeded) {
			t.Fatalf("err = %v, want ErrDeadlineExceeded", o.err)
		}
	})
}

func TestCapacity_Stream(t *testing.T) {
	t.Run("refused without reserve", func(t *testing.T) {
		b, sink := capBridge(newBlockRunner(), cmdsurface.Concurrency{MaxInflight: 1})
		defer cmdsurface.TestingHoldSlots(b)()
		adm, err := b.Admit(context.Background(), capInv("s"))
		if err != nil {
			t.Fatal(err)
		}
		out := make(chan cmdsurface.Event, 4)
		err = adm.Stream(context.Background(), out)
		if !errors.Is(err, cmdsurface.ErrOverloaded) {
			t.Fatalf("err = %v, want overloaded", err)
		}
		if _, open := <-out; open {
			t.Fatal("a refused stream sent an event")
		}
		if recs := sink.records(); len(recs) != 1 || !errors.Is(recs[0], cmdsurface.ErrOverloaded) {
			t.Fatalf("audit = %v, want one overloaded refusal", recs)
		}
	})
	t.Run("reserve refuses before the stream", func(t *testing.T) {
		b, sink := capBridge(newBlockRunner(), cmdsurface.Concurrency{MaxInflight: 1})
		defer cmdsurface.TestingHoldSlots(b)()
		adm, err := b.Admit(context.Background(), capInv("s"))
		if err != nil {
			t.Fatal(err)
		}
		if err := adm.Reserve(context.Background()); !errors.Is(err, cmdsurface.ErrOverloaded) {
			t.Fatalf("Reserve = %v, want overloaded", err)
		}
		out := make(chan cmdsurface.Event, 4)
		if err := adm.Stream(context.Background(), out); !errors.Is(err, cmdsurface.ErrOverloaded) {
			t.Fatalf("Stream after a refused Reserve = %v", err)
		}
		if _, err := adm.Run(context.Background()); !errors.Is(err, cmdsurface.ErrOverloaded) {
			t.Fatalf("Run after a refused Reserve = %v", err)
		}
		if recs := sink.records(); len(recs) != 1 {
			t.Fatalf("audit = %v, want the refusal once", recs)
		}
	})
	t.Run("reserved place waits in the stream", func(t *testing.T) {
		run := newBlockRunner()
		b, _ := capBridge(run, cmdsurface.Concurrency{MaxInflight: 1, MaxQueue: 1})
		release := cmdsurface.TestingHoldSlots(b)
		adm, err := b.Admit(context.Background(), capInv("s"))
		if err != nil {
			t.Fatal(err)
		}
		if err := adm.Reserve(context.Background()); err != nil {
			t.Fatalf("Reserve = %v, want a place in the queue", err)
		}
		waitLoad(t, b, 1, 1)
		// The reserved place counts: the queue is full.
		other, _ := b.Admit(context.Background(), capInv("o"))
		if err := other.Reserve(context.Background()); !errors.Is(err, cmdsurface.ErrOverloaded) {
			t.Fatalf("second Reserve = %v, want overloaded", err)
		}
		out := make(chan cmdsurface.Event, 4)
		errc := make(chan error, 1)
		go func() { errc <- adm.Stream(context.Background(), out) }()
		release()
		awaitStart(t, run, "s")
		run.proceed <- struct{}{}
		if err := <-errc; err != nil {
			t.Fatalf("Stream = %v", err)
		}
		waitLoad(t, b, 0, 0)
	})
	t.Run("reserved and never run", func(t *testing.T) {
		// A reserved call whose caller is gone before it waits gives
		// its place back rather than holding it.
		b, _ := capBridge(newBlockRunner(), cmdsurface.Concurrency{MaxInflight: 1, MaxQueue: 1})
		release := cmdsurface.TestingHoldSlots(b)
		defer release()
		adm, _ := b.Admit(context.Background(), capInv("s"))
		if err := adm.Reserve(context.Background()); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := adm.Run(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v, want canceled", err)
		}
		waitLoad(t, b, 1, 0)
	})
}

func TestCapacity_RunnerBoundsInflight(t *testing.T) {
	def := cmdsurface.DefaultConcurrency()
	if def.MaxInflight != 32 || def.MaxQueue != 64 {
		t.Fatalf("defaults = %+v", def)
	}
	cases := map[string]struct {
		opts []cmdsurface.Option
		want int
	}{
		"shared tree runs one at a time": {nil, 1},
		"root factory runs in parallel": {[]cmdsurface.Option{cmdsurface.WithRunner(
			cmdsurface.InProcessRunner(nil, cmdsurface.WithRootFactory(gateTree)))}, 32},
		"any other runner": {[]cmdsurface.Option{cmdsurface.WithRunner(newBlockRunner())}, 32},
		"middleware keeps the runner's bound": {[]cmdsurface.Option{cmdsurface.WithRunnerMiddleware(
			func(r cmdsurface.Runner) cmdsurface.Runner { return r })}, 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			b := cmdsurface.New(gateTree(), append(tc.opts, cmdsurface.WithConcurrency(def))...)
			load, ok := b.Capacity()
			if !ok || load.MaxInflight != tc.want || load.MaxQueue != 64 {
				t.Fatalf("load = %+v (%v), want %d in flight and 64 queued", load, ok, tc.want)
			}
		})
	}
	if _, ok := cmdsurface.New(gateTree()).Capacity(); ok {
		t.Error("a bridge without WithConcurrency reports a gate")
	}
}

func TestCapacity_SharedTreeQueuesInTheGate(t *testing.T) {
	// Over the shared-tree runner a second call waits in the gate's
	// queue — counted, cancelable — not on the runner's lock.
	hold := make(chan struct{})
	started := make(chan struct{}, 1)
	root := &cobra.Command{Use: "root"}
	root.AddCommand(&cobra.Command{
		Use:         "slow",
		Annotations: map[string]string{"kit/side-effect": "read"},
		RunE: func(*cobra.Command, []string) error {
			started <- struct{}{}
			<-hold
			return nil
		},
	})
	b := cmdsurface.New(root, cmdsurface.WithConcurrency(cmdsurface.DefaultConcurrency()))
	b.Expose("*", cmdsurface.SurfaceREST)
	inv := cmdsurface.Invocation{Path: []string{"slow"}, Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceREST}}
	first := make(chan error, 1)
	go func() { _, err := b.Invoke(context.Background(), inv); first <- err }()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	second := make(chan error, 1)
	go func() { _, err := b.Invoke(ctx, inv); second <- err }()
	waitLoad(t, b, 1, 1)
	cancel()
	if err := <-second; !errors.Is(err, context.Canceled) {
		t.Fatalf("queued call: %v, want canceled", err)
	}
	close(hold)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
}

func TestCapacity_LocalSurfacesAreNotCounted(t *testing.T) {
	b, _ := capBridge(&rlRunner{}, cmdsurface.Concurrency{MaxInflight: 1})
	defer cmdsurface.TestingHoldSlots(b)()
	b.Expose("*", cmdsurface.SurfaceLib)
	inv := capInv("lib")
	inv.Meta.Surface = cmdsurface.SurfaceLib
	if _, err := b.Invoke(context.Background(), inv); err != nil {
		t.Fatalf("library call: %v", err)
	}
	if _, err := b.Invoke(context.Background(), capInv("rest")); !errors.Is(err, cmdsurface.ErrOverloaded) {
		t.Fatalf("remote call: %v, want overloaded", err)
	}
}
