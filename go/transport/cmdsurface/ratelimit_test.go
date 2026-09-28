package cmdsurface

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is a settable clock for the limiter.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(1_700_000_000, 0)} }

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// withRateLimitClock is WithRateLimit on a test clock.
func withRateLimitClock(cfg RateLimit, clk *fakeClock) Option {
	return func(c *bridgeConfig) { c.rateLimit = newRateLimiter(cfg, clk.now) }
}

// okRunner answers every call with exit 0.
func okRunner() Runner {
	return &fakeRunner{run: func(context.Context, Invocation) (Result, error) { return Result{}, nil }}
}

// smallLimit is a limit whose numbers are easy to reason about: one
// token a second per tier, a burst of three.
var smallLimit = RateLimit{
	Read:        RateRule{PerMinute: 60, Burst: 3},
	Write:       RateRule{PerMinute: 60, Burst: 3},
	Destructive: RateRule{PerMinute: 60, Burst: 3},
}

func restCall(path ...string) Invocation {
	return Invocation{Path: path, Meta: Meta{
		Surface: SurfaceREST,
		Extra:   map[string]string{"remote_addr": "203.0.113.7:50000"},
	}}
}

func TestRateLimit_BoundaryAndRetryAfter(t *testing.T) {
	clk := newFakeClock()
	sink := &admitSink{}
	calls := 0
	var got Invocation
	b := New(newBridgeTree(), WithRunner(countingRunner(&calls, &got)),
		WithSinks(sink.spec()), withRateLimitClock(smallLimit, clk))
	b.Expose("*", SurfaceREST)
	ctx := context.Background()

	for i := range 3 {
		if _, err := b.Invoke(ctx, restCall("ping")); err != nil {
			t.Fatalf("call %d within the burst refused: %v", i+1, err)
		}
	}
	_, err := b.Invoke(ctx, restCall("ping"))
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("call past the burst: err = %v, want ErrRateLimited", err)
	}
	var rl *RateLimitedError
	if !errors.As(err, &rl) || rl.Tier != RateTierRead || rl.Path != "ping" || rl.Surface != SurfaceREST {
		t.Fatalf("refusal = %#v", err)
	}
	wait, ok := RetryAfter(err)
	if !ok || wait != time.Second {
		t.Fatalf("RetryAfter = %v %v, want 1s (one token a second, bucket empty)", wait, ok)
	}
	if calls != 3 {
		t.Fatalf("runner reached %d times, want 3", calls)
	}
	last := sink.errs[len(sink.errs)-1]
	if !errors.Is(last, ErrRateLimited) {
		t.Fatalf("refusal not audited: last record err = %v", last)
	}

	// An early retry is refused with the same hint: a refusal gives
	// its token back instead of pushing the next one further out.
	_, err = b.Invoke(ctx, restCall("ping"))
	if wait2, _ := RetryAfter(err); wait2 != time.Second {
		t.Fatalf("second refusal RetryAfter = %v, want 1s", wait2)
	}

	// Honoring the hint is enough; a millisecond early is not. (The
	// bucket counts tokens in floating point, so the boundary is
	// exact to well under a millisecond, not to the nanosecond.)
	clk.advance(time.Second - time.Millisecond)
	if _, err := b.Invoke(ctx, restCall("ping")); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("retry before Retry-After admitted: %v", err)
	}
	clk.advance(time.Millisecond)
	if _, err := b.Invoke(ctx, restCall("ping")); err != nil {
		t.Fatalf("retry at Retry-After refused: %v", err)
	}
}

func TestRateLimit_TiersAreSeparateBuckets(t *testing.T) {
	clk := newFakeClock()
	b := New(newBridgeTree(), WithRunner(okRunner()),
		WithPolicy(Policy{AllowDestructiveOn: []Surface{SurfaceREST}}),
		withRateLimitClock(smallLimit, clk))
	b.Expose("*", SurfaceREST)
	ctx := context.Background()

	for range 3 {
		if _, err := b.Invoke(ctx, restCall("widget", "add")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Invoke(ctx, restCall("widget", "add")); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("write bucket not exhausted: %v", err)
	}
	var rl *RateLimitedError
	_, err := b.Invoke(ctx, restCall("widget", "add"))
	if !errors.As(err, &rl) || rl.Tier != RateTierWrite {
		t.Fatalf("write refusal = %v", err)
	}
	if _, err := b.Invoke(ctx, restCall("ping")); err != nil {
		t.Fatalf("read refused after writes spent the write bucket: %v", err)
	}
	_, err = b.Invoke(ctx, restCall("widget", "delete"))
	if err != nil {
		t.Fatalf("destructive refused after writes spent the write bucket: %v", err)
	}
}

func TestRateLimit_TierOfLeaf(t *testing.T) {
	b := New(newBridgeTree())
	for path, want := range map[string]RateTier{
		"ping":          RateTierRead,
		"report daily":  RateTierRead,
		"widget add":    RateTierWrite,
		"widget delete": RateTierDestructive,
	} {
		leaf, err := b.resolveLeaf(splitPath(path))
		if err != nil {
			t.Fatal(err)
		}
		if got := rateTierOf(leaf); got != want {
			t.Errorf("%s: tier %s, want %s", path, got, want)
		}
	}
	if got := rateTierOf(nil); got != RateTierWrite {
		t.Errorf("unknown leaf: tier %s, want write", got)
	}
}

func splitPath(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ' ' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return out
}

func TestRateLimit_Keys(t *testing.T) {
	addr := func(a string) Meta {
		return Meta{Surface: SurfaceREST, Extra: map[string]string{"remote_addr": a}}
	}
	same := []struct {
		name string
		a, b Meta
	}{
		{"one address, two ports", addr("198.51.100.1:1000"), addr("198.51.100.1:2000")},
		{"one IPv6 /64", addr("[2001:db8:1:2::1]:1"), addr("[2001:db8:1:2:ffff::9]:2")},
		{"claimed tenant does not split an address",
			Meta{Surface: SurfaceREST, Tenant: "t1", Extra: map[string]string{"remote_addr": "198.51.100.1:1"}},
			Meta{Surface: SurfaceREST, Tenant: "t2", Extra: map[string]string{"remote_addr": "198.51.100.1:2"}}},
		{"a principal is one caller from any address",
			Meta{Surface: SurfaceREST, Caller: "alice", Extra: map[string]string{"remote_addr": "198.51.100.1:1"}},
			Meta{Surface: SurfaceREST, Caller: "alice", Extra: map[string]string{"remote_addr": "198.51.100.2:1"}}},
		{"a principal is one caller on any surface",
			Meta{Surface: SurfaceREST, Caller: "alice"}, Meta{Surface: SurfaceMCP, Caller: "alice"}},
		{"no identity: one bucket per surface", Meta{Surface: SurfaceBus}, Meta{Surface: SurfaceBus}},
	}
	for _, c := range same {
		if rateKey(c.a, RateTierRead) != rateKey(c.b, RateTierRead) {
			t.Errorf("%s: keys differ: %q %q", c.name, rateKey(c.a, RateTierRead), rateKey(c.b, RateTierRead))
		}
	}
	differ := []struct {
		name string
		a, b Meta
	}{
		{"two addresses", addr("198.51.100.1:1"), addr("198.51.100.2:1")},
		{"two IPv6 /64s", addr("[2001:db8:1:2::1]:1"), addr("[2001:db8:1:3::1]:1")},
		{"two principals behind one address",
			Meta{Surface: SurfaceREST, Caller: "alice", Extra: map[string]string{"remote_addr": "198.51.100.1:1"}},
			Meta{Surface: SurfaceREST, Caller: "bob", Extra: map[string]string{"remote_addr": "198.51.100.1:1"}}},
		{"one principal, two tenants",
			Meta{Surface: SurfaceREST, Caller: "alice", Tenant: "t1"},
			Meta{Surface: SurfaceREST, Caller: "alice", Tenant: "t2"}},
		{"principal vs the address it came from",
			Meta{Surface: SurfaceREST, Caller: "198.51.100.1"}, addr("198.51.100.1:1")},
		{"bus vs cron", Meta{Surface: SurfaceBus}, Meta{Surface: SurfaceCron}},
	}
	for _, c := range differ {
		if rateKey(c.a, RateTierRead) == rateKey(c.b, RateTierRead) {
			t.Errorf("%s: keys collide: %q", c.name, rateKey(c.a, RateTierRead))
		}
	}
	if rateKey(Meta{Caller: "a"}, RateTierRead) == rateKey(Meta{Caller: "a"}, RateTierWrite) {
		t.Error("tiers share a key")
	}
}

func TestRateLimit_LocalSurfacesAreNeverLimited(t *testing.T) {
	clk := newFakeClock()
	b := New(newBridgeTree(), WithRunner(okRunner()), withRateLimitClock(smallLimit, clk))
	for _, s := range []Surface{SurfaceCLI, SurfaceLib, ""} {
		for i := range 10 {
			if _, err := b.Invoke(context.Background(), Invocation{Path: []string{"ping"}, Meta: Meta{Surface: s}}); err != nil {
				t.Fatalf("%q call %d: %v", s, i, err)
			}
		}
	}
}

func TestRateLimit_OffWithoutOption(t *testing.T) {
	b := New(newBridgeTree(), WithRunner(okRunner()))
	b.Expose("*", SurfaceREST)
	for i := range 1000 {
		if _, err := b.Invoke(context.Background(), restCall("ping")); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
}

// TestRateLimit_OnlyAdmittedCallsSpend pins the gate's slot: a call an
// earlier gate refuses — the destructive ceiling, the permission gate
// — takes no token, so a caller cannot be locked out by refusals, and
// a refusal is never reported as a rate limit.
func TestRateLimit_OnlyAdmittedCallsSpend(t *testing.T) {
	clk := newFakeClock()
	deny := func(_ context.Context, meta Meta, _ *Leaf) PermissionDecision {
		if meta.Extra["deny"] == "1" {
			return PermissionDecision{Reason: "denied"}
		}
		return PermissionDecision{Allowed: true}
	}
	b := New(newBridgeTree(), WithRunner(okRunner()), WithPermission(deny),
		withRateLimitClock(smallLimit, clk))
	b.Expose("*", SurfaceREST)
	ctx := context.Background()

	denied := restCall("widget", "add")
	denied.Meta.Extra = map[string]string{"remote_addr": "203.0.113.7:1", "deny": "1"}
	for range 10 {
		if _, err := b.Invoke(ctx, denied); !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("err = %v, want ErrPermissionDenied (permission answers before the rate limit)", err)
		}
		// The destructive ceiling refuses delete on REST by default.
		if _, err := b.Invoke(ctx, restCall("widget", "delete")); !errors.Is(err, ErrDestructiveBlocked) {
			t.Fatalf("err = %v, want ErrDestructiveBlocked", err)
		}
	}
	for i := range 3 {
		if _, err := b.Invoke(ctx, restCall("widget", "add")); err != nil {
			t.Fatalf("admitted call %d refused: refusals spent tokens: %v", i+1, err)
		}
	}
}

// TestRateLimit_StreamingAdmissionIsLimited pins that Admit, the half
// a streaming surface calls, applies the gate.
func TestRateLimit_StreamingAdmissionIsLimited(t *testing.T) {
	clk := newFakeClock()
	b := New(newBridgeTree(), WithRunner(&streamingRunner{}), withRateLimitClock(smallLimit, clk))
	b.Expose("*", SurfaceREST)
	for range 3 {
		if _, err := b.Admit(context.Background(), restCall("ping")); err != nil {
			t.Fatal(err)
		}
	}
	adm, err := b.Admit(context.Background(), restCall("ping"))
	if !errors.Is(err, ErrRateLimited) || adm != nil {
		t.Fatalf("Admit past the burst = %v, %v", adm, err)
	}
}

// TestRateLimit_Concurrent hammers one bucket from many goroutines on
// a frozen clock: exactly the burst is admitted, whatever the
// interleaving. Run with -race.
func TestRateLimit_Concurrent(t *testing.T) {
	clk := newFakeClock()
	cfg := RateLimit{Read: RateRule{PerMinute: 60, Burst: 25}}
	b := New(newBridgeTree(), WithRunner(okRunner()), withRateLimitClock(cfg, clk))
	b.Expose("*", SurfaceREST)

	var ok, limited, other atomic.Int64
	var wg sync.WaitGroup
	for g := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 20 {
				inv := restCall("ping")
				// Distinct ports, one address: one caller.
				inv.Meta.Extra = map[string]string{"remote_addr": fmt.Sprintf("203.0.113.7:%d", 1000+g*100+i)}
				_, err := b.Invoke(context.Background(), inv)
				switch {
				case err == nil:
					ok.Add(1)
				case errors.Is(err, ErrRateLimited):
					limited.Add(1)
				default:
					other.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 25 || limited.Load() != 16*20-25 || other.Load() != 0 {
		t.Fatalf("admitted %d, limited %d, other %d; want 25, %d, 0",
			ok.Load(), limited.Load(), other.Load(), 16*20-25)
	}
}

// TestRateLimit_ConcurrentCallersAreIndependent runs many callers at
// once, each spending exactly its burst: none is refused, so no
// caller's traffic leaks into another's bucket.
func TestRateLimit_ConcurrentCallersAreIndependent(t *testing.T) {
	clk := newFakeClock()
	b := New(newBridgeTree(), WithRunner(okRunner()), withRateLimitClock(smallLimit, clk))
	b.Expose("*", SurfaceREST)
	var wg sync.WaitGroup
	var failed atomic.Int64
	for c := range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 3 {
				inv := restCall("ping")
				inv.Meta.Caller = fmt.Sprintf("caller-%d", c)
				if _, err := b.Invoke(context.Background(), inv); err != nil {
					failed.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if failed.Load() != 0 {
		t.Fatalf("%d calls refused within their callers' bursts", failed.Load())
	}
}

func TestRateLimit_SweepDropsOnlyRefilledBuckets(t *testing.T) {
	clk := newFakeClock()
	rl := newRateLimiter(smallLimit, clk.now)
	// With the debtor below, the map reaches the sweep threshold, and
	// the next new caller sweeps.
	for i := range minSweep - 1 {
		rl.take(fmt.Sprintf("idle-%d", i), RateTierRead)
	}
	// A caller that emptied its bucket two seconds later still owes
	// tokens when the sweep runs: its debt must survive it.
	clk.advance(2 * time.Second)
	for range 4 {
		rl.take("debtor", RateTierRead)
	}
	// Three seconds refill a burst of three at one a second: the idle
	// buckets are full again, and a full bucket is a new one.
	clk.advance(time.Second)
	rl.take("new", RateTierRead) // triggers the sweep
	if n := rl.size(); n != 2 {
		t.Fatalf("buckets after sweep = %d, want 2 (debtor, new)", n)
	}
	// One second refilled one token: a forgotten bucket would hold
	// three.
	if _, ok := rl.take("debtor", RateTierRead); !ok {
		t.Fatal("the debtor's refilled token was lost")
	}
	if _, ok := rl.take("debtor", RateTierRead); ok {
		t.Fatal("the sweep forgave a debtor: its second call must be refused")
	}
}

func TestRetryAfterRounding(t *testing.T) {
	for d, want := range map[time.Duration]int{
		0: 1, time.Nanosecond: 1, time.Second: 1, time.Second + time.Nanosecond: 2, 1500 * time.Millisecond: 2,
	} {
		if got := RetryAfterSeconds(d); got != want {
			t.Errorf("RetryAfterSeconds(%v) = %d, want %d", d, got, want)
		}
	}
	for d, want := range map[time.Duration]int64{
		0: 1, time.Nanosecond: 1, time.Millisecond: 1, time.Millisecond + 1: 2, 1500 * time.Millisecond: 1500,
	} {
		if got := RetryAfterMillis(d); got != want {
			t.Errorf("RetryAfterMillis(%v) = %d, want %d", d, got, want)
		}
	}
	if _, ok := RetryAfter(errors.New("x")); ok {
		t.Error("RetryAfter on a plain error reported a hint")
	}
	wrapped := fmt.Errorf("outer: %w", &RateLimitedError{RetryAfter: 0})
	if d, ok := RetryAfter(wrapped); !ok || d != time.Millisecond {
		t.Errorf("RetryAfter(wrapped zero) = %v %v, want 1ms true", d, ok)
	}
}

func TestDefaultRateLimitFillsZeroRules(t *testing.T) {
	def := DefaultRateLimit()
	got := RateLimit{Write: RateRule{PerMinute: 5}}
	if r := got.Rule(RateTierWrite); r.PerMinute != 5 || r.Burst != def.Write.Burst {
		t.Errorf("write rule = %+v", r)
	}
	if r := got.Rule(RateTierRead); r != def.Read {
		t.Errorf("read rule = %+v, want %+v", r, def.Read)
	}
	if r := got.Rule(RateTierDestructive); r != def.Destructive {
		t.Errorf("destructive rule = %+v, want %+v", r, def.Destructive)
	}
}

func TestRateLimit_CodesOnMessageSurfaces(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &RateLimitedError{RetryAfter: time.Second})
	if got := bridgeErrorCode(err); got != "rate_limited" {
		t.Errorf("bus code = %q, want rate_limited", got)
	}
	if got := errorCode(err); got != "rate_limited" {
		t.Errorf("ws code = %q, want rate_limited", got)
	}
	if status, code := lambdaHTTPErrorCode(err); status != 429 || code != "rate_limited" {
		t.Errorf("lambda = %d %q, want 429 rate_limited", status, code)
	}
}
