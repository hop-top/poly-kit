package cmdsurface

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/storage/kv/memory"
)

// newCacheTree builds a tree whose leaves all declare kit/cache-ttl, on
// every tier, so a test can tell the tier rule from the annotation:
//
//	root
//	├── widget
//	│   ├── list    (read, cache-ttl 1m)
//	│   ├── show    (read, no cache-ttl)
//	│   ├── bad     (read, malformed cache-ttl)
//	│   ├── add     (write, cache-ttl 1m)
//	│   ├── purge   (destructive, cache-ttl 1m)
//	│   ├── peek    (unannotated, cache-ttl 1m)
//	│   └── delete  (unannotated, inferred destructive, cache-ttl 1m)
func newCacheTree() *cobra.Command {
	root := &cobra.Command{Use: "tool"}
	widget := &cobra.Command{Use: "widget"}
	leaf := func(use, tier, ttl string) *cobra.Command {
		ann := map[string]string{}
		if tier != "" {
			ann["kit/side-effect"] = tier
		}
		if ttl != "" {
			ann[AnnotationCacheTTL] = ttl
		}
		c := &cobra.Command{
			Use:         use,
			RunE:        func(*cobra.Command, []string) error { return nil },
			Annotations: ann,
		}
		c.Flags().Int("limit", 0, "")
		c.Flags().String("sort", "", "")
		return c
	}
	widget.AddCommand(
		leaf("list", "read", "1m"),
		leaf("show", "read", ""),
		leaf("bad", "read", "soon"),
		leaf("add", "write", "1m"),
		leaf("purge", "destructive", "1m"),
		leaf("peek", "", "1m"),
		leaf("delete", "", "1m"),
	)
	root.AddCommand(widget)
	return root
}

// tallyRunner answers every Run with its result func, counting runs.
type tallyRunner struct {
	calls  atomic.Int64
	result func(inv Invocation) (Result, error)
}

func (r *tallyRunner) Run(_ context.Context, inv Invocation) (Result, error) {
	n := r.calls.Add(1)
	if r.result != nil {
		return r.result(inv)
	}
	return Result{Data: map[string]any{"run": n}}, nil
}

func (r *tallyRunner) Stream(_ context.Context, _ Invocation, out chan<- Event) error {
	defer close(out)
	r.calls.Add(1)
	out <- Event{Kind: "done", Data: &Result{}}
	return nil
}

// newCacheBridge returns a bridge over newCacheTree with the result
// cache on a memory store, every leaf exposed on REST and MCP, and
// destructive leaves allowed on REST.
func newCacheBridge(t *testing.T, run Runner, sink *admitSink) *Bridge {
	t.Helper()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	opts := []Option{
		WithRunner(run),
		WithResultCache(store),
		WithPolicy(Policy{AllowDestructiveOn: []Surface{SurfaceREST}}),
	}
	if sink != nil {
		opts = append(opts, WithSinks(sink.spec()))
	}
	b := New(newCacheTree(), opts...)
	b.Expose("*", SurfaceREST, SurfaceMCP)
	return b
}

func cacheRESTCall(path ...string) Invocation {
	return Invocation{Path: path, Meta: Meta{Surface: SurfaceREST}}
}

// call admits and runs inv, returning the result and the cache info.
func call(t *testing.T, b *Bridge, inv Invocation) (Result, CacheInfo, bool) {
	t.Helper()
	adm, err := b.Admit(context.Background(), inv)
	if err != nil {
		t.Fatalf("Admit(%v): %v", inv.Path, err)
	}
	res, err := adm.Run(context.Background())
	if err != nil {
		t.Fatalf("Run(%v): %v", inv.Path, err)
	}
	info, ok := adm.Cache()
	return res, info, ok
}

func TestResultCache_MissThenHit(t *testing.T) {
	run := &tallyRunner{}
	sink := &admitSink{}
	b := newCacheBridge(t, run, sink)

	first, info1, ok := call(t, b, cacheRESTCall("widget", "list"))
	if !ok || info1.Hit {
		t.Fatalf("first call: cache info %+v ok=%v, want a stored miss", info1, ok)
	}
	second, info2, ok := call(t, b, cacheRESTCall("widget", "list"))
	if !ok || !info2.Hit {
		t.Fatalf("second call: cache info %+v ok=%v, want a hit", info2, ok)
	}
	if n := run.calls.Load(); n != 1 {
		t.Fatalf("runner ran %d times, want 1", n)
	}
	a, _ := json.Marshal(first)
	c, _ := json.Marshal(second)
	if string(a) != string(c) {
		t.Fatalf("hit answers %s, miss answered %s", c, a)
	}
	if info1.ETag == "" || info1.ETag != info2.ETag {
		t.Fatalf("ETag miss=%q hit=%q, want equal and non-empty", info1.ETag, info2.ETag)
	}
	if info1.MaxAge != time.Minute || info2.MaxAge <= 0 || info2.MaxAge > time.Minute {
		t.Fatalf("MaxAge miss=%v hit=%v", info1.MaxAge, info2.MaxAge)
	}

	// Both calls are audited; only the hit is marked.
	if sink.count() != 2 {
		t.Fatalf("audited %d records, want 2", sink.count())
	}
	if m := sink.invs[0].Meta.Extra[cacheExtraKey]; m != "" {
		t.Fatalf("miss marked %q", m)
	}
	if m := sink.invs[1].Meta.Extra[cacheExtraKey]; m != CacheHit {
		t.Fatalf("hit marked %q, want %q", m, CacheHit)
	}
}

func TestResultCache_KeyedByInvocation(t *testing.T) {
	run := &tallyRunner{}
	b := newCacheBridge(t, run, nil)

	withFlags := func(flags map[string]any, args ...string) Invocation {
		inv := cacheRESTCall("widget", "list")
		inv.Flags = flags
		inv.Args = args
		return inv
	}
	call(t, b, withFlags(map[string]any{"limit": 5, "sort": "name"}))
	// The same flags, in another order: the same call.
	call(t, b, withFlags(map[string]any{"sort": "name", "limit": 5}))
	if n := run.calls.Load(); n != 1 {
		t.Fatalf("flag order changed the key: %d runs", n)
	}
	call(t, b, withFlags(map[string]any{"limit": 6, "sort": "name"}))
	call(t, b, withFlags(map[string]any{"limit": 5, "sort": "name"}, "a"))
	if n := run.calls.Load(); n != 3 {
		t.Fatalf("another flag value or arg must miss: %d runs, want 3", n)
	}
}

func TestResultCache_TTLExpiry(t *testing.T) {
	run := &tallyRunner{}
	b := newCacheBridge(t, run, nil)
	now := time.Unix(1_000_000, 0)
	var mu sync.Mutex
	b.rcache.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }

	call(t, b, cacheRESTCall("widget", "list"))
	advance(59 * time.Second)
	_, info, _ := call(t, b, cacheRESTCall("widget", "list"))
	if !info.Hit || info.MaxAge != time.Second {
		t.Fatalf("within ttl: %+v, want a hit with 1s left", info)
	}
	advance(time.Second)
	_, info, _ = call(t, b, cacheRESTCall("widget", "list"))
	if info.Hit {
		t.Fatal("an expired result was served")
	}
	if n := run.calls.Load(); n != 2 {
		t.Fatalf("runner ran %d times, want 2", n)
	}
}

// An identity the transport did not establish is a claim: it never
// selects, or reaches, an identity's entry. The call is keyed as
// anonymous, and its result is not private to the name it claimed.
func TestResultCache_ClaimedIdentityIsAnonymous(t *testing.T) {
	run := &tallyRunner{result: func(Invocation) (Result, error) {
		return Result{Data: map[string]any{"same": true}}, nil
	}}
	b := newCacheBridge(t, run, nil)

	claimed := func(caller, tenant, scopes string) Invocation {
		inv := cacheRESTCall("widget", "list")
		inv.Meta.Caller, inv.Meta.Tenant = caller, tenant
		inv.Meta.Extra = map[string]string{"scopes": scopes}
		return inv
	}
	alice := claimed("alice", "acme", "read")
	alice.Meta.Established = EstablishedVerified

	anon, _ := cacheKey(b.root, cacheRESTCall("widget", "list"))
	for _, inv := range []Invocation{claimed("alice", "acme", "read"), claimed("mallory", "", "")} {
		if key, _ := cacheKey(b.root, inv); key != anon {
			t.Fatalf("claimed %+v is not keyed as anonymous", inv.Meta)
		}
	}
	if key, _ := cacheKey(b.root, alice); key == anon {
		t.Fatal("an established principal is keyed as anonymous")
	}

	// alice's own entry is out of reach of a caller claiming her name.
	if _, info, _ := call(t, b, alice); info.Hit || !info.Private {
		t.Fatalf("established alice: Hit=%v Private=%v, want a private miss", info.Hit, info.Private)
	}
	_, info, _ := call(t, b, claimed("alice", "acme", "read"))
	if info.Hit {
		t.Fatal("a claim of alice's identity was answered from her entry")
	}
	if info.Private {
		t.Fatal("a claimed identity made the result private")
	}
	// Claims share the anonymous entry.
	if _, info, _ = call(t, b, claimed("mallory", "", "")); !info.Hit {
		t.Fatal("a second claim missed the anonymous entry")
	}
	if n := run.calls.Load(); n != 2 {
		t.Fatalf("runner ran %d times, want 2 (alice, anonymous)", n)
	}
}

func TestResultCache_PrincipalIsolation(t *testing.T) {
	// Every caller gets the same data, so only the key tells their
	// entries and their ETags apart.
	run := &tallyRunner{result: func(Invocation) (Result, error) {
		return Result{Data: map[string]any{"same": true}}, nil
	}}
	b := newCacheBridge(t, run, nil)

	as := func(caller, tenant, scopes string) Invocation {
		inv := cacheRESTCall("widget", "list")
		inv.Meta.Caller = caller
		inv.Meta.Tenant = tenant
		if scopes != "" {
			inv.Meta.Extra = map[string]string{"scopes": scopes}
		}
		if caller != "" || tenant != "" || scopes != "" {
			inv.Meta.Established = EstablishedVerified
		}
		return inv
	}
	callers := []Invocation{
		as("", "", ""),
		as("alice", "", ""),
		as("bob", "", ""),
		as("alice", "acme", ""),
		as("alice", "acme", "read"),
	}
	etags := map[string]bool{}
	for _, inv := range callers {
		res, info, _ := call(t, b, inv)
		if info.Hit {
			t.Fatalf("%+v answered from another caller's result %v", inv.Meta, res.Data)
		}
		wantPrivate := inv.Meta.Caller != "" || inv.Meta.Tenant != "" || inv.Meta.Extra != nil
		if info.Private != wantPrivate {
			t.Fatalf("%+v: Private = %v, want %v", inv.Meta, info.Private, wantPrivate)
		}
		etags[info.ETag] = true
	}
	if n := run.calls.Load(); n != int64(len(callers)) {
		t.Fatalf("runner ran %d times, want %d", n, len(callers))
	}
	if len(etags) != len(callers) {
		t.Fatalf("callers share an ETag: %v", etags)
	}
	// Each caller hits its own entry; scope order does not matter.
	_, info, _ := call(t, b, as("alice", "acme", "read"))
	if !info.Hit {
		t.Fatal("alice missed her own entry")
	}
	two := as("carol", "", "b,a")
	call(t, b, two)
	_, info, _ = call(t, b, as("carol", "", "a, b"))
	if !info.Hit {
		t.Fatal("scope order changed the key")
	}
}

func TestResultCache_NeverCachesOtherTiers(t *testing.T) {
	for _, path := range [][]string{
		{"widget", "add"},    // write
		{"widget", "purge"},  // destructive
		{"widget", "peek"},   // unannotated
		{"widget", "delete"}, // inferred destructive
		{"widget", "show"},   // read without kit/cache-ttl
		{"widget", "bad"},    // read with a malformed kit/cache-ttl
	} {
		t.Run(joinPath(path), func(t *testing.T) {
			run := &tallyRunner{}
			b := newCacheBridge(t, run, nil)
			leaf, err := b.resolveLeaf(path)
			if err != nil {
				t.Fatal(err)
			}
			if leaf.CacheTTL() != 0 {
				t.Fatalf("CacheTTL = %v, want 0", leaf.CacheTTL())
			}
			for range 2 {
				if _, _, ok := call(t, b, cacheRESTCall(path...)); ok {
					t.Fatal("result reported cacheable")
				}
			}
			if n := run.calls.Load(); n != 2 {
				t.Fatalf("runner ran %d times, want 2", n)
			}
		})
	}
}

func TestResultCache_OnlyOnREST(t *testing.T) {
	run := &tallyRunner{}
	b := newCacheBridge(t, run, nil)
	for range 2 {
		inv := cacheRESTCall("widget", "list")
		inv.Meta.Surface = SurfaceMCP
		if _, _, ok := call(t, b, inv); ok {
			t.Fatal("MCP result reported cacheable")
		}
	}
	if n := run.calls.Load(); n != 2 {
		t.Fatalf("runner ran %d times, want 2", n)
	}
}

func TestResultCache_OffWithoutStore(t *testing.T) {
	run := &tallyRunner{}
	b := New(newCacheTree(), WithRunner(run))
	b.Expose("*", SurfaceREST)
	for range 2 {
		if _, _, ok := call(t, b, cacheRESTCall("widget", "list")); ok {
			t.Fatal("cache active without a store")
		}
	}
	if n := run.calls.Load(); n != 2 {
		t.Fatalf("runner ran %d times, want 2", n)
	}
}

func TestResultCache_FailuresNotStored(t *testing.T) {
	for name, result := range map[string]func(Invocation) (Result, error){
		"exit code": func(Invocation) (Result, error) { return Result{ExitCode: 1}, nil },
		"error":     func(Invocation) (Result, error) { return Result{}, errors.New("boom") },
	} {
		t.Run(name, func(t *testing.T) {
			run := &tallyRunner{result: result}
			b := newCacheBridge(t, run, nil)
			for range 2 {
				adm, err := b.Admit(context.Background(), cacheRESTCall("widget", "list"))
				if err != nil {
					t.Fatal(err)
				}
				_, _ = adm.Run(context.Background())
				if _, ok := adm.Cache(); ok {
					t.Fatal("a failed run reported cacheable")
				}
			}
			if n := run.calls.Load(); n != 2 {
				t.Fatalf("runner ran %d times, want 2", n)
			}
		})
	}
}

// Numbers survive the store exactly: an id past float64's precision
// reads back as the same digits.
func TestResultCache_KeepsNumbersExact(t *testing.T) {
	run := &tallyRunner{result: func(Invocation) (Result, error) {
		return Result{Data: map[string]any{"id": int64(9007199254740993)}}, nil
	}}
	b := newCacheBridge(t, run, nil)
	first, _, _ := call(t, b, cacheRESTCall("widget", "list"))
	second, _, _ := call(t, b, cacheRESTCall("widget", "list"))
	for _, res := range []Result{first, second} {
		raw, _ := json.Marshal(res.Data)
		if string(raw) != `{"id":9007199254740993}` {
			t.Fatalf("data = %s", raw)
		}
	}
}

// blockingRunner holds every Run until release is closed.
type blockingRunner struct {
	calls   atomic.Int64
	entered chan struct{}
	release chan struct{}
	exit    int
}

func (r *blockingRunner) Run(context.Context, Invocation) (Result, error) {
	if r.calls.Add(1) == 1 {
		close(r.entered)
	}
	<-r.release
	return Result{ExitCode: r.exit, Stdout: "ran\n"}, nil
}

func (r *blockingRunner) Stream(context.Context, Invocation, chan<- Event) error {
	return errors.New("no stream")
}

// waitForWaiters polls until n calls wait on key's flight.
func waitForWaiters(t *testing.T, rc *resultCache, key string, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rc.mu.Lock()
		f := rc.inflight[key]
		got := 0
		if f != nil {
			got = f.waiters
		}
		rc.mu.Unlock()
		if got == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("fewer than %d calls joined the flight", n)
}

func TestResultCache_CoalescesInFlight(t *testing.T) {
	const followers = 8
	run := &blockingRunner{entered: make(chan struct{}), release: make(chan struct{})}
	sink := &admitSink{}
	b := newCacheBridge(t, run, sink)
	key, _ := cacheKey(b.root, cacheRESTCall("widget", "list"))

	var wg sync.WaitGroup
	results := make([]Result, followers+1)
	errs := make([]error, followers+1)
	start := func(i int) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = b.Invoke(context.Background(), cacheRESTCall("widget", "list"))
		}()
	}
	start(0)
	<-run.entered
	for i := 1; i <= followers; i++ {
		start(i)
	}
	waitForWaiters(t, b.rcache, key, followers)
	close(run.release)
	wg.Wait()

	if n := run.calls.Load(); n != 1 {
		t.Fatalf("runner ran %d times for identical calls in flight, want 1", n)
	}
	for i := range results {
		if errs[i] != nil || results[i].Stdout != "ran\n" {
			t.Fatalf("call %d: %+v, %v", i, results[i], errs[i])
		}
	}
	coalesced := 0
	for _, inv := range sink.invs {
		if inv.Meta.Extra[cacheExtraKey] == CacheCoalesced {
			coalesced++
		}
	}
	if sink.count() != followers+1 || coalesced != followers {
		t.Fatalf("audited %d records, %d coalesced; want %d and %d",
			sink.count(), coalesced, followers+1, followers)
	}
}

// A run that fails is not shared: each waiting call runs for itself.
func TestResultCache_FailureNotShared(t *testing.T) {
	run := &blockingRunner{entered: make(chan struct{}), release: make(chan struct{}), exit: 1}
	b := newCacheBridge(t, run, nil)
	key, _ := cacheKey(b.root, cacheRESTCall("widget", "list"))

	var wg sync.WaitGroup
	for i := range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = b.Invoke(context.Background(), cacheRESTCall("widget", "list"))
		}()
		if i == 0 {
			<-run.entered
		}
	}
	waitForWaiters(t, b.rcache, key, 2)
	close(run.release)
	wg.Wait()
	if n := run.calls.Load(); n != 3 {
		t.Fatalf("runner ran %d times, want 3", n)
	}
}

// A waiting call whose context ends leaves without the result, and the
// run it waited on carries on.
func TestResultCache_WaiterLeavesOnCancel(t *testing.T) {
	run := &blockingRunner{entered: make(chan struct{}), release: make(chan struct{})}
	b := newCacheBridge(t, run, nil)
	key, _ := cacheKey(b.root, cacheRESTCall("widget", "list"))

	leaderDone := make(chan error, 1)
	go func() {
		_, err := b.Invoke(context.Background(), cacheRESTCall("widget", "list"))
		leaderDone <- err
	}()
	<-run.entered

	ctx, cancel := context.WithCancel(context.Background())
	waiterDone := make(chan error, 1)
	go func() {
		_, err := b.Invoke(ctx, cacheRESTCall("widget", "list"))
		waiterDone <- err
	}()
	waitForWaiters(t, b.rcache, key, 1)
	cancel()
	if err := <-waiterDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter err = %v, want context.Canceled", err)
	}
	close(run.release)
	if err := <-leaderDone; err != nil {
		t.Fatalf("leader: %v", err)
	}
	if _, info, _ := call(t, b, cacheRESTCall("widget", "list")); !info.Hit {
		t.Fatal("the leader's result was not stored")
	}
}

func TestResultCache_StreamAnswersHit(t *testing.T) {
	run := &tallyRunner{}
	sink := &admitSink{}
	b := newCacheBridge(t, run, sink)
	want, _, _ := call(t, b, cacheRESTCall("widget", "list"))

	adm, err := b.Admit(context.Background(), cacheRESTCall("widget", "list"))
	if err != nil {
		t.Fatal(err)
	}
	out := make(chan Event, 4)
	if err := adm.Stream(context.Background(), out); err != nil {
		t.Fatal(err)
	}
	var events []Event
	for ev := range out {
		events = append(events, ev)
	}
	if len(events) != 1 || events[0].Kind != "done" {
		t.Fatalf("events = %+v, want one done", events)
	}
	got, _ := json.Marshal(events[0].Data)
	exp, _ := json.Marshal(want)
	if string(got) != string(exp) {
		t.Fatalf("done carries %s, want %s", got, exp)
	}
	if n := run.calls.Load(); n != 1 {
		t.Fatalf("runner reached %d times, want 1", n)
	}
	if m := sink.invs[len(sink.invs)-1].Meta.Extra[cacheExtraKey]; m != CacheHit {
		t.Fatalf("stream hit marked %q", m)
	}
}

func TestParseCacheTTL(t *testing.T) {
	if d, err := ParseCacheTTL(" 90s "); err != nil || d != 90*time.Second {
		t.Fatalf("ParseCacheTTL(90s) = %v, %v", d, err)
	}
	for _, bad := range []string{"", "soon", "0s", "-5m", "30"} {
		if _, err := ParseCacheTTL(bad); err == nil {
			t.Fatalf("ParseCacheTTL(%q) accepted", bad)
		}
	}
}

// The shipped sinks record the cache mark, through the redacting
// SinkSet, so an audit trail tells a served result from a run.
func TestResultCache_SinksRecordTheMark(t *testing.T) {
	l, path := openChain(t)
	var file bytes.Buffer
	var logged bytes.Buffer
	sinks := SinkSet{
		{Sink: &ChainSink{Log: l}, OnOK: true},
		{Sink: &FileSink{W: &file}, OnOK: true},
		{Sink: &LogSink{Handler: slog.NewJSONHandler(&logged, nil)}, OnOK: true},
	}
	b := New(newCacheTree(), WithRunner(&tallyRunner{}), WithResultCache(memory.New()), WithSinks(sinks...))
	b.Expose("*", SurfaceREST)
	call(t, b, cacheRESTCall("widget", "list"))
	call(t, b, cacheRESTCall("widget", "list"))
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	chain, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string]string{"chain": string(chain), "file": file.String(), "log": logged.String()} {
		if n := strings.Count(out, `"cache":"hit"`); n != 1 {
			t.Errorf("%s sink: %d hit marks, want 1:\n%s", name, n, out)
		}
	}
}

// A caller the transport vouches for is the server's owner, scoped to
// that transport as idempotency and the rate limit scope it: the name
// it claims never reaches the entry of the verified identity it names,
// nor splits the owner's entry.
func TestResultCache_TransportCallerScopedToTransport(t *testing.T) {
	run := &tallyRunner{result: func(Invocation) (Result, error) {
		return Result{Data: map[string]any{"same": true}}, nil
	}}
	b := newCacheBridge(t, run, nil)

	as := func(est Establishment, caller string) Invocation {
		inv := cacheRESTCall("widget", "list")
		inv.Meta.Caller, inv.Meta.Tenant = caller, "acme"
		inv.Meta.Extra = map[string]string{"scopes": "read"}
		inv.Meta.Established = est
		return inv
	}
	if _, info, _ := call(t, b, as(EstablishedVerified, "alice")); info.Hit {
		t.Fatal("verified alice: want a miss")
	}
	_, info, _ := call(t, b, as(EstablishedTransport, "alice"))
	if info.Hit {
		t.Fatal("a transport caller claiming alice read verified alice's entry")
	}
	if !info.Private {
		t.Fatal("the owner's result must be private")
	}
	if _, info, _ = call(t, b, as(EstablishedTransport, "bob")); !info.Hit {
		t.Fatal("the owner's entry is split by the name it claims")
	}
	if n := run.calls.Load(); n != 2 {
		t.Fatalf("runner ran %d times, want 2 (verified alice, owner)", n)
	}
}
