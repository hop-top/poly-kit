package cmdsurface

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hop.top/kit/go/console/cli/idemstore"
	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/storage/kv/memory"
)

// idemRunner counts runs and answers with a Result naming the
// run, so a replay is told from a second run by its content.
type idemRunner struct {
	calls atomic.Int32
	exit  int
	gate  chan struct{} // when set, a run waits on it
	began chan struct{} // when set, a run signals it started
}

func (r *idemRunner) Run(ctx context.Context, inv Invocation) (Result, error) {
	n := r.calls.Add(1)
	if r.began != nil {
		r.began <- struct{}{}
	}
	if r.gate != nil {
		select {
		case <-r.gate:
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
	return Result{
		ExitCode: r.exit,
		Stdout:   "run " + string(rune('0'+n)) + "\n",
		Data:     map[string]any{"run": int(n), "path": inv.Path},
	}, nil
}

func (r *idemRunner) Stream(ctx context.Context, inv Invocation, out chan<- Event) error {
	defer close(out)
	res, err := r.Run(ctx, inv)
	out <- Event{Kind: "stdout", Data: "line"}
	out <- Event{Kind: "done", Data: &res}
	return err
}

func idemBridge(t *testing.T, run Runner, ledger *IdempotencyLedger, opts ...Option) (*Bridge, *admitSink) {
	t.Helper()
	sink := &admitSink{}
	all := append([]Option{WithRunner(run), WithSinks(sink.spec()), WithIdempotency(ledger, time.Hour)}, opts...)
	b := New(newBridgeTree(), all...)
	b.Expose("*", SurfaceREST, SurfaceRPC, SurfaceSocket)
	return b, sink
}

func keyed(key, caller string) Invocation {
	return Invocation{
		Path:  []string{"widget", "add"},
		Flags: map[string]any{"name": "a"},
		Meta:  Meta{Surface: SurfaceREST, Caller: caller, IdempotencyKey: key},
	}
}

func TestIdempotency_ReplaysRecordedResult(t *testing.T) {
	run := &idemRunner{}
	b, sink := idemBridge(t, run, NewIdempotencyLedger(idemstore.Memory()))
	ctx := context.Background()

	first, err := b.Invoke(ctx, keyed("k1", "alice"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.Invoke(ctx, keyed("k1", "alice"))
	if err != nil {
		t.Fatal(err)
	}
	if got := run.calls.Load(); got != 1 {
		t.Fatalf("runner reached %d times, want 1", got)
	}
	if first.Replayed || !second.Replayed {
		t.Fatalf("replayed = %v, %v; want false, true", first.Replayed, second.Replayed)
	}
	if second.Stdout != first.Stdout || second.ExitCode != first.ExitCode {
		t.Fatalf("replay = %+v, want the first answer %+v", second, first)
	}
	data, _ := second.Data.(map[string]any)
	if data == nil || data["run"] == nil {
		t.Fatalf("replay lost Data: %#v", second.Data)
	}
	if sink.count() != 2 {
		t.Fatalf("audit records = %d, want 2 (run and replay)", sink.count())
	}
	if sink.invs[1].Meta.Extra[idempotentReplayedExtra] != "true" {
		t.Fatalf("replay audit record lacks the marker: %v", sink.invs[1].Meta.Extra)
	}
	if sink.invs[0].Meta.Extra[idempotentReplayedExtra] != "" {
		t.Fatal("the run's audit record must not be marked replayed")
	}
}

func TestIdempotency_KeyReusedForAnotherInvocation(t *testing.T) {
	run := &idemRunner{}
	b, sink := idemBridge(t, run, NewIdempotencyLedger(idemstore.Memory()))
	ctx := context.Background()

	if _, err := b.Invoke(ctx, keyed("k1", "alice")); err != nil {
		t.Fatal(err)
	}
	other := keyed("k1", "alice")
	other.Flags = map[string]any{"name": "b"}
	_, err := b.Invoke(ctx, other)
	if !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Fatalf("err = %v, want ErrIdempotencyKeyReused", err)
	}
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatal("ErrIdempotencyKeyReused must wrap domain.ErrValidation")
	}
	if run.calls.Load() != 1 {
		t.Fatal("a reused key must not run")
	}
	if last := sink.errs[len(sink.errs)-1]; !errors.Is(last, ErrIdempotencyKeyReused) {
		t.Fatalf("refusal not audited: %v", last)
	}
}

func TestIdempotency_ConflictWhileFirstCallRuns(t *testing.T) {
	run := &idemRunner{gate: make(chan struct{}), began: make(chan struct{}, 1)}
	b, _ := idemBridge(t, run, NewIdempotencyLedger(idemstore.Memory()))
	ctx := context.Background()

	done := make(chan Result, 1)
	go func() {
		res, err := b.Invoke(ctx, keyed("k1", "alice"))
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()
	<-run.began

	_, err := b.Invoke(ctx, keyed("k1", "alice"))
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("err = %v, want ErrIdempotencyConflict", err)
	}
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatal("ErrIdempotencyConflict must wrap domain.ErrConflict")
	}
	other := keyed("k1", "alice")
	other.Flags = map[string]any{"name": "b"}
	if _, err := b.Invoke(ctx, other); !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Fatalf("another invocation under a running key: err = %v, want ErrIdempotencyKeyReused", err)
	}

	close(run.gate)
	first := <-done
	res, err := b.Invoke(ctx, keyed("k1", "alice"))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Replayed || res.Stdout != first.Stdout {
		t.Fatalf("after the first call ends the key replays: %+v", res)
	}
	if run.calls.Load() != 1 {
		t.Fatalf("runner reached %d times, want 1", run.calls.Load())
	}
}

// TestIdempotency_ConcurrentCallsRunOnce races many identical keyed
// calls: exactly one runs, and every other is a conflict or a replay.
func TestIdempotency_ConcurrentCallsRunOnce(t *testing.T) {
	run := &idemRunner{}
	b, _ := idemBridge(t, run, NewIdempotencyLedger(idemstore.Memory()))
	ctx := context.Background()

	var wg sync.WaitGroup
	var ran, replayed, conflicts atomic.Int32
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := b.Invoke(ctx, keyed("race", "alice"))
			switch {
			case errors.Is(err, ErrIdempotencyConflict):
				conflicts.Add(1)
			case err != nil:
				t.Error(err)
			case res.Replayed:
				replayed.Add(1)
			default:
				ran.Add(1)
			}
		}()
	}
	wg.Wait()
	if ran.Load() != 1 || run.calls.Load() != 1 {
		t.Fatalf("ran = %d (runner %d), want exactly 1", ran.Load(), run.calls.Load())
	}
	if ran.Load()+replayed.Load()+conflicts.Load() != 32 {
		t.Fatalf("outcomes do not add up: ran %d replayed %d conflicts %d",
			ran.Load(), replayed.Load(), conflicts.Load())
	}
}

func TestIdempotency_TTLExpiry(t *testing.T) {
	run := &idemRunner{}
	ledger := NewIdempotencyLedger(idemstore.Memory())
	now := time.Now()
	ledger.now = func() time.Time { return now }
	b, _ := idemBridge(t, run, ledger)
	ctx := context.Background()

	if _, err := b.Invoke(ctx, keyed("k1", "alice")); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	if res, _ := b.Invoke(ctx, keyed("k1", "alice")); !res.Replayed {
		t.Fatal("a record exactly ttl old still replays")
	}
	now = now.Add(time.Second)
	res, err := b.Invoke(ctx, keyed("k1", "alice"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Replayed || run.calls.Load() != 2 {
		t.Fatalf("an expired record must run again: replayed=%v runs=%d", res.Replayed, run.calls.Load())
	}
	// Past its ttl, the key is free for another invocation too.
	now = now.Add(2 * time.Hour)
	other := keyed("k1", "alice")
	other.Flags = map[string]any{"name": "b"}
	if _, err := b.Invoke(ctx, other); err != nil {
		t.Fatalf("expired key reused: %v", err)
	}
}

func TestIdempotency_ScopedByPrincipal(t *testing.T) {
	cases := []struct {
		name       string
		a, b       Meta
		wantReplay bool
	}{
		{"same caller", Meta{Surface: SurfaceREST, Caller: "alice"}, Meta{Surface: SurfaceREST, Caller: "alice"}, true},
		{"other caller", Meta{Surface: SurfaceREST, Caller: "alice"}, Meta{Surface: SurfaceREST, Caller: "bob"}, false},
		{"other tenant", Meta{Surface: SurfaceREST, Caller: "alice", Tenant: "acme"}, Meta{Surface: SurfaceREST, Caller: "alice", Tenant: "globex"}, false},
		{"claim on another surface", Meta{Surface: SurfaceREST, Caller: "alice"}, Meta{Surface: SurfaceSocket, Caller: "alice"}, false},
		{"anonymous, same host, new port",
			Meta{Surface: SurfaceREST, Extra: map[string]string{"remote_addr": "10.0.0.7:5001"}},
			Meta{Surface: SurfaceREST, Extra: map[string]string{"remote_addr": "10.0.0.7:6002"}}, true},
		{"anonymous, other host",
			Meta{Surface: SurfaceREST, Extra: map[string]string{"remote_addr": "10.0.0.7:5001"}},
			Meta{Surface: SurfaceREST, Extra: map[string]string{"remote_addr": "10.0.0.8:5001"}}, false},
		{"anonymous socket", Meta{Surface: SurfaceSocket}, Meta{Surface: SurfaceSocket}, true},
		{"established caller on another surface",
			Meta{Surface: SurfaceREST, Caller: "alice", Established: EstablishedVerified},
			Meta{Surface: SurfaceRPC, Caller: "alice", Established: EstablishedVerified}, true},
		{"established caller, transport-established on the socket",
			Meta{Surface: SurfaceREST, Caller: "alice", Established: EstablishedVerified},
			Meta{Surface: SurfaceSocket, Caller: "alice", Established: EstablishedTransport}, true},
		{"established caller, other tenant",
			Meta{Surface: SurfaceREST, Caller: "alice", Tenant: "acme", Established: EstablishedVerified},
			Meta{Surface: SurfaceRPC, Caller: "alice", Tenant: "globex", Established: EstablishedVerified}, false},
		{"a claim never reaches an established caller's record",
			Meta{Surface: SurfaceREST, Caller: "alice", Established: EstablishedVerified},
			Meta{Surface: SurfaceREST, Caller: "alice"}, false},
		{"an established caller never reaches a claim's record",
			Meta{Surface: SurfaceREST, Caller: "alice"},
			Meta{Surface: SurfaceREST, Caller: "alice", Established: EstablishedVerified}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := &idemRunner{}
			b, _ := idemBridge(t, run, NewIdempotencyLedger(idemstore.Memory()))
			ctx := context.Background()
			first, second := keyed("k1", ""), keyed("k1", "")
			first.Meta, second.Meta = c.a, c.b
			first.Meta.IdempotencyKey, second.Meta.IdempotencyKey = "k1", "k1"
			if _, err := b.Invoke(ctx, first); err != nil {
				t.Fatal(err)
			}
			res, err := b.Invoke(ctx, second)
			if err != nil {
				t.Fatal(err)
			}
			if res.Replayed != c.wantReplay {
				t.Fatalf("replayed = %v, want %v", res.Replayed, c.wantReplay)
			}
		})
	}
}

func TestIdempotency_UntouchedCalls(t *testing.T) {
	cases := []struct {
		name string
		inv  Invocation
	}{
		{"no key", Invocation{Path: []string{"widget", "add"}, Meta: Meta{Surface: SurfaceREST}}},
		{"lib surface", Invocation{Path: []string{"widget", "add"}, Meta: Meta{Surface: SurfaceLib, IdempotencyKey: "k"}}},
		{"cli surface", Invocation{Path: []string{"widget", "add"}, Meta: Meta{Surface: SurfaceCLI, IdempotencyKey: "k"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := &idemRunner{}
			b, _ := idemBridge(t, run, NewIdempotencyLedger(idemstore.Memory()))
			b.Expose("*", SurfaceLib, SurfaceCLI)
			for range 2 {
				res, err := b.Invoke(context.Background(), c.inv)
				if err != nil {
					t.Fatal(err)
				}
				if res.Replayed {
					t.Fatal("replayed a call idempotency does not touch")
				}
			}
			if run.calls.Load() != 2 {
				t.Fatalf("runs = %d, want 2", run.calls.Load())
			}
		})
	}
}

func TestIdempotency_OffWithoutLedger(t *testing.T) {
	run := &idemRunner{}
	b, _ := idemBridge(t, run, nil)
	for range 2 {
		if _, err := b.Invoke(context.Background(), keyed("k1", "alice")); err != nil {
			t.Fatal(err)
		}
	}
	if run.calls.Load() != 2 {
		t.Fatalf("runs = %d, want 2 with replay off", run.calls.Load())
	}
}

func TestIdempotency_FailureIsNotRecorded(t *testing.T) {
	run := &idemRunner{exit: 6}
	b, _ := idemBridge(t, run, NewIdempotencyLedger(idemstore.Memory()))
	for range 2 {
		res, err := b.Invoke(context.Background(), keyed("k1", "alice"))
		if err != nil {
			t.Fatal(err)
		}
		if res.Replayed {
			t.Fatal("a failed run must not replay")
		}
	}
	if run.calls.Load() != 2 {
		t.Fatalf("runs = %d, want 2: a failure is retried, not replayed", run.calls.Load())
	}
}

func TestIdempotency_AbandonAndContextReleaseTheKey(t *testing.T) {
	run := &idemRunner{}
	b, _ := idemBridge(t, run, NewIdempotencyLedger(idemstore.Memory()))

	adm, err := b.Admit(context.Background(), keyed("k1", "alice"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Admit(context.Background(), keyed("k1", "alice")); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("an admitted, unrun key is in flight: err = %v", err)
	}
	adm.Abandon()
	if _, err := b.Invoke(context.Background(), keyed("k1", "alice")); err != nil {
		t.Fatalf("after Abandon the key is free: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	if _, err := b.Admit(ctx, keyed("k2", "alice")); err != nil {
		t.Fatal(err)
	}
	cancel()
	deadline := time.Now().Add(time.Second)
	for {
		_, err := b.Admit(context.Background(), keyed("k2", "alice"))
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the request context ending did not release the key: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestIdempotency_StreamRecordsAndReplays(t *testing.T) {
	run := &idemRunner{}
	b, _ := idemBridge(t, run, NewIdempotencyLedger(idemstore.Memory()))
	ctx := context.Background()

	stream := func() ([]Event, bool) {
		adm, err := b.Admit(ctx, keyed("k1", "alice"))
		if err != nil {
			t.Fatal(err)
		}
		out := make(chan Event, 8)
		errc := make(chan error, 1)
		go func() { errc <- adm.Stream(ctx, out) }()
		var evs []Event
		for ev := range out {
			evs = append(evs, ev)
		}
		if err := <-errc; err != nil {
			t.Fatal(err)
		}
		return evs, adm.Replayed()
	}
	first, replayed := stream()
	if replayed {
		t.Fatal("the first stream runs")
	}
	second, replayed := stream()
	if !replayed || run.calls.Load() != 1 {
		t.Fatalf("second stream: replayed=%v runs=%d", replayed, run.calls.Load())
	}
	last := second[len(second)-1]
	res, _ := last.Data.(*Result)
	if last.Kind != "done" || res == nil || !res.Replayed {
		t.Fatalf("a replayed stream ends with the replayed Result: %+v", last)
	}
	firstDone, _ := first[len(first)-1].Data.(*Result)
	if res.Stdout != firstDone.Stdout {
		t.Fatalf("replayed stdout %q, want %q", res.Stdout, firstDone.Stdout)
	}
	if second[0].Kind != "stdout" || second[0].Data != "run 1" {
		t.Fatalf("a replayed stream sends its lines first: %+v", second[0])
	}
	if unary, _ := b.Invoke(ctx, keyed("k1", "alice")); !unary.Replayed {
		t.Fatal("a streamed record replays on a unary call too")
	}
}

func TestIdempotency_FingerprintIgnoresKeyFlagAndValueSpelling(t *testing.T) {
	a := Invocation{Path: []string{"widget", "add"}, Flags: map[string]any{"count": int64(3), "yes": true}}
	b := Invocation{Path: []string{"widget", "add"}, Flags: map[string]any{"count": float64(3), "yes": true, "idempotency-key": "k"}}
	if idempotencyFingerprint(a) != idempotencyFingerprint(b) {
		t.Fatal("the same call spelled as a query string and a JSON body must fingerprint the same")
	}
	c := Invocation{Path: []string{"widget", "add"}, Args: []string{"x"}, Flags: a.Flags}
	if idempotencyFingerprint(a) == idempotencyFingerprint(c) {
		t.Fatal("different args must fingerprint differently")
	}
}

func TestIdempotency_StoreFailureFailsKeyedCall(t *testing.T) {
	run := &idemRunner{}
	boom := errors.New("disk full")
	ledger := OpenIdempotencyLedger(func() (idemstore.Store, error) { return nil, boom })
	b, _ := idemBridge(t, run, ledger)

	_, err := b.Invoke(context.Background(), keyed("k1", "alice"))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the store's error", err)
	}
	if run.calls.Load() != 0 {
		t.Fatal("a keyed call whose ledger cannot answer must not run")
	}
	if _, err := b.Invoke(context.Background(), Invocation{Path: []string{"widget", "add"}, Meta: Meta{Surface: SurfaceREST}}); err != nil {
		t.Fatalf("a call without a key never touches the store: %v", err)
	}
}

// TestIdempotency_SentinelsMapToCodes pins the refusal code each
// sentinel carries in its message, which the surfaces render.
func TestIdempotency_SentinelsMapToCodes(t *testing.T) {
	for _, c := range []struct {
		err  error
		code string
	}{{ErrIdempotencyConflict, CodeIdempotencyConflict}, {ErrIdempotencyKeyReused, CodeIdempotencyKeyReused}} {
		if got := IdempotencyRefusalCode(c.err); got != c.code {
			t.Errorf("IdempotencyRefusalCode(%v) = %q, want %q", c.err, got, c.code)
		}
	}
	if IdempotencyRefusalCode(errors.New("x")) != "" {
		t.Error("an unrelated error has no idempotency code")
	}
}

// Inside slot 8 replay answers first and the result cache second. A
// keyed read the cache runs or answers is recorded under its key and
// its reservation released, so the key's retry replays instead of
// meeting its own call as still running.
func TestIdempotency_ReplayBeforeResultCache(t *testing.T) {
	run := &tallyRunner{}
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	b := New(newCacheTree(), WithRunner(run), WithResultCache(store),
		WithIdempotency(NewIdempotencyLedger(idemstore.Memory()), time.Hour))
	b.Expose("*", SurfaceREST)
	ctx := context.Background()
	read := func(key string) Result {
		t.Helper()
		inv := cacheRESTCall("widget", "list")
		inv.Meta.IdempotencyKey = key
		res, err := b.Invoke(ctx, inv)
		if err != nil {
			t.Fatalf("key %s: %v", key, err)
		}
		return res
	}

	if res := read("k1"); res.Replayed {
		t.Fatal("first call replayed")
	}
	if res := read("k1"); !res.Replayed {
		t.Fatal("a keyed read the cache ran was not recorded: its retry did not replay")
	}
	if res := read("k2"); res.Replayed {
		t.Fatal("a new key replayed")
	}
	if res := read("k2"); !res.Replayed {
		t.Fatal("a keyed read the cache answered was not recorded: its retry did not replay")
	}
	if n := run.calls.Load(); n != 1 {
		t.Fatalf("runner ran %d times, want 1", n)
	}
}
