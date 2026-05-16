package cmdsurface_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/cmdsurface"
)

// busFakeBus is an in-memory implementation of both
// cmdsurface.Subscriber and api.EventPublisher. It maintains a
// per-topic subscriber list under a mutex and dispatches each
// Publish to every registered handler synchronously. Tests drive the
// surface by calling deliver / publishRaw with crafted JSON payloads
// and assert against the captured publications.
type busFakeBus struct {
	mu          sync.Mutex
	subscribers map[string][]busSubEntry
	nextID      int
	// publications captures every Publish call keyed by topic so
	// tests can assert on response payloads without racing the
	// subscriber loop.
	publications map[string][]busPublication
	// publishCount counts every Publish invocation across topics —
	// the fire-and-forget test asserts that this stays zero.
	publishCount atomic.Int64
}

type busSubEntry struct {
	id      int
	handler func(msg cmdsurface.BusMessage) error
}

type busPublication struct {
	Topic   string
	Source  string
	Payload any
}

func newBusFakeBus() *busFakeBus {
	return &busFakeBus{
		subscribers:  make(map[string][]busSubEntry),
		publications: make(map[string][]busPublication),
	}
}

// Subscribe implements cmdsurface.Subscriber.
func (b *busFakeBus) Subscribe(
	_ context.Context,
	topic string,
	handler func(msg cmdsurface.BusMessage) error,
) (func(), error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	id := b.nextID
	b.subscribers[topic] = append(b.subscribers[topic], busSubEntry{id: id, handler: handler})
	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		list := b.subscribers[topic]
		kept := list[:0]
		for _, e := range list {
			if e.id != id {
				kept = append(kept, e)
			}
		}
		b.subscribers[topic] = kept
	}
	return cancel, nil
}

// Publish implements api.EventPublisher.
func (b *busFakeBus) Publish(_ context.Context, topic, source string, payload any) error {
	b.publishCount.Add(1)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.publications[topic] = append(b.publications[topic], busPublication{
		Topic:   topic,
		Source:  source,
		Payload: payload,
	})
	return nil
}

// deliver synchronously dispatches msg to every handler registered on
// msg.Topic. Returns the count of handlers invoked.
func (b *busFakeBus) deliver(msg cmdsurface.BusMessage) int {
	b.mu.Lock()
	handlers := make([]func(cmdsurface.BusMessage) error, 0, len(b.subscribers[msg.Topic]))
	for _, e := range b.subscribers[msg.Topic] {
		handlers = append(handlers, e.handler)
	}
	b.mu.Unlock()
	for _, h := range handlers {
		_ = h(msg)
	}
	return len(handlers)
}

// publicationsOn returns a copy of the publications recorded for topic.
func (b *busFakeBus) publicationsOn(topic string) []busPublication {
	b.mu.Lock()
	defer b.mu.Unlock()
	src := b.publications[topic]
	out := make([]busPublication, len(src))
	copy(out, src)
	return out
}

// totalPublishes reports how many Publish calls have run across all
// topics. The fire-and-forget test asserts on this directly.
func (b *busFakeBus) totalPublishes() int64 {
	return b.publishCount.Load()
}

// busFakeRunner is a programmable Runner used by the Bus surface
// tests. It mirrors the RPC suite's fakeRunner but keeps its name
// distinct so parallel work on other surfaces doesn't collide.
type busFakeRunner struct {
	mu             sync.Mutex
	RunFn          func(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error)
	LastInvocation cmdsurface.Invocation
	calls          atomic.Int64
}

func (r *busFakeRunner) Run(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
	r.calls.Add(1)
	r.mu.Lock()
	r.LastInvocation = inv
	fn := r.RunFn
	r.mu.Unlock()
	if fn == nil {
		return cmdsurface.Result{Stdout: "ok"}, nil
	}
	return fn(ctx, inv)
}

func (r *busFakeRunner) Stream(_ context.Context, _ cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
	close(out)
	return nil
}

// busTestTree builds the standard tree the Bus tests share. Five
// leaves cover the safety axes used across the cases.
//
//	root
//	├── echo            (read-only)
//	├── destroy         (destructive)
//	├── secret          (auth-required)
//	├── confirm         (requires-confirmation)
//	└── hidden-bus      (left out of Expose — not enabled on bus)
func busTestTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}
	root.AddCommand(&cobra.Command{
		Use:  "echo",
		RunE: func(_ *cobra.Command, _ []string) error { return nil },
	})
	root.AddCommand(&cobra.Command{
		Use: "destroy",
		Annotations: map[string]string{
			"kit/side-effect": "destructive",
		},
		RunE: func(_ *cobra.Command, _ []string) error { return nil },
	})
	root.AddCommand(&cobra.Command{
		Use: "secret",
		Annotations: map[string]string{
			"kit/auth-required": "true",
		},
		RunE: func(_ *cobra.Command, _ []string) error { return nil },
	})
	root.AddCommand(&cobra.Command{
		Use: "confirm",
		Annotations: map[string]string{
			"kit/requires-confirmation": "true",
		},
		RunE: func(_ *cobra.Command, _ []string) error { return nil },
	})
	root.AddCommand(&cobra.Command{
		Use:  "hidden-bus",
		RunE: func(_ *cobra.Command, _ []string) error { return nil },
	})
	return root
}

// busBridge wires the standard tree into a Bridge with the supplied
// policy. Every non-hidden leaf is exposed on SurfaceBus; hidden-bus
// stays absent.
func busBridge(t *testing.T, runner cmdsurface.Runner, policy cmdsurface.Policy) *cmdsurface.Bridge {
	t.Helper()
	br := cmdsurface.New(busTestTree(),
		cmdsurface.WithRunner(runner),
		cmdsurface.WithPolicy(policy),
	)
	br.Expose("echo", cmdsurface.SurfaceBus)
	br.Expose("destroy", cmdsurface.SurfaceBus)
	br.Expose("secret", cmdsurface.SurfaceBus)
	br.Expose("confirm", cmdsurface.SurfaceBus)
	return br
}

// busDecodeResult unmarshals a publication payload as a Result.
// publications are stored as any; the surface passes Result by value
// into Publish, so a type assertion is the cheapest read path.
func busDecodeResult(t *testing.T, pub busPublication) cmdsurface.Result {
	t.Helper()
	switch v := pub.Payload.(type) {
	case cmdsurface.Result:
		return v
	case *cmdsurface.Result:
		return *v
	default:
		// Round-trip via JSON in case a future change returns the
		// payload through encoding (defensive).
		raw, err := json.Marshal(pub.Payload)
		if err != nil {
			t.Fatalf("busDecodeResult marshal: %v", err)
		}
		var out cmdsurface.Result
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("busDecodeResult unmarshal: %v (raw=%s)", err, raw)
		}
		return out
	}
}

// busDecodeError unmarshals a publication payload as an error envelope.
func busDecodeError(t *testing.T, pub busPublication) (code, message string) {
	t.Helper()
	raw, err := json.Marshal(pub.Payload)
	if err != nil {
		t.Fatalf("busDecodeError marshal: %v", err)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("busDecodeError unmarshal: %v (raw=%s)", err, raw)
	}
	return env.Error.Code, env.Error.Message
}

// --- tests ---

func TestMountBus_HappyPath(t *testing.T) {
	runner := &busFakeRunner{
		RunFn: func(_ context.Context, _ cmdsurface.Invocation) (cmdsurface.Result, error) {
			return cmdsurface.Result{Stdout: "hello", ExitCode: 0}, nil
		},
	}
	br := busBridge(t, runner, cmdsurface.DefaultPolicy())
	bus := newBusFakeBus()
	cleanup, err := cmdsurface.MountBus(br, bus, bus, []cmdsurface.BusBinding{{
		Path:          []string{"echo"},
		RequestTopic:  "cmd.echo.req",
		ResponseTopic: "cmd.echo.resp",
	}})
	if err != nil {
		t.Fatalf("MountBus: %v", err)
	}
	t.Cleanup(cleanup)

	payload, _ := json.Marshal(map[string]any{
		"args":  []string{"hi"},
		"flags": map[string]any{"loud": true},
	})
	n := bus.deliver(cmdsurface.BusMessage{Topic: "cmd.echo.req", Payload: payload})
	if n != 1 {
		t.Fatalf("deliver: handlers=%d want=1", n)
	}

	pubs := bus.publicationsOn("cmd.echo.resp")
	if len(pubs) != 1 {
		t.Fatalf("publications=%d want=1", len(pubs))
	}
	got := busDecodeResult(t, pubs[0])
	if got.Stdout != "hello" {
		t.Errorf("Stdout=%q want=hello", got.Stdout)
	}
}

func TestMountBus_UnknownLeafBinding(t *testing.T) {
	br := busBridge(t, &busFakeRunner{}, cmdsurface.DefaultPolicy())
	bus := newBusFakeBus()
	_, err := cmdsurface.MountBus(br, bus, bus, []cmdsurface.BusBinding{{
		Path:         []string{"nope"},
		RequestTopic: "cmd.nope.req",
	}})
	if err == nil {
		t.Fatal("MountBus: expected error for unknown leaf")
	}
	if !errors.Is(err, cmdsurface.ErrUnknownCommand) {
		t.Errorf("err=%v want errors.Is(ErrUnknownCommand)", err)
	}
}

func TestMountBus_SurfaceNotEnabled(t *testing.T) {
	// hidden-bus exists but is not in Expose; default policy excludes
	// SurfaceBus from the default-enabled set, so the leaf is not
	// reachable on the bus.
	br := busBridge(t, &busFakeRunner{}, cmdsurface.DefaultPolicy())
	bus := newBusFakeBus()
	_, err := cmdsurface.MountBus(br, bus, bus, []cmdsurface.BusBinding{{
		Path:         []string{"hidden-bus"},
		RequestTopic: "cmd.hidden.req",
	}})
	if err == nil {
		t.Fatal("MountBus: expected error for not-enabled leaf")
	}
	if !errors.Is(err, cmdsurface.ErrSurfaceNotEnabled) {
		t.Errorf("err=%v want errors.Is(ErrSurfaceNotEnabled)", err)
	}
}

func TestMountBus_DestructiveBlocked(t *testing.T) {
	br := busBridge(t, &busFakeRunner{}, cmdsurface.DefaultPolicy())
	bus := newBusFakeBus()
	cleanup, err := cmdsurface.MountBus(br, bus, bus, []cmdsurface.BusBinding{{
		Path:          []string{"destroy"},
		RequestTopic:  "cmd.destroy.req",
		ResponseTopic: "cmd.destroy.resp",
	}})
	if err != nil {
		t.Fatalf("MountBus: %v", err)
	}
	t.Cleanup(cleanup)

	bus.deliver(cmdsurface.BusMessage{Topic: "cmd.destroy.req", Payload: []byte("{}")})

	pubs := bus.publicationsOn("cmd.destroy.resp")
	if len(pubs) != 1 {
		t.Fatalf("publications=%d want=1", len(pubs))
	}
	code, _ := busDecodeError(t, pubs[0])
	if code != "destructive_blocked" {
		t.Errorf("code=%q want=destructive_blocked", code)
	}
}

func TestMountBus_DestructiveAllowed(t *testing.T) {
	runner := &busFakeRunner{
		RunFn: func(_ context.Context, _ cmdsurface.Invocation) (cmdsurface.Result, error) {
			return cmdsurface.Result{Stdout: "boom"}, nil
		},
	}
	br := busBridge(t, runner, cmdsurface.Policy{
		AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceBus},
		DefaultEnabled:     []cmdsurface.Surface{cmdsurface.SurfaceCLI, cmdsurface.SurfaceLib, cmdsurface.SurfaceMCP},
	})
	bus := newBusFakeBus()
	cleanup, err := cmdsurface.MountBus(br, bus, bus, []cmdsurface.BusBinding{{
		Path:          []string{"destroy"},
		RequestTopic:  "cmd.destroy.req",
		ResponseTopic: "cmd.destroy.resp",
	}})
	if err != nil {
		t.Fatalf("MountBus: %v", err)
	}
	t.Cleanup(cleanup)

	bus.deliver(cmdsurface.BusMessage{Topic: "cmd.destroy.req", Payload: []byte("{}")})
	pubs := bus.publicationsOn("cmd.destroy.resp")
	if len(pubs) != 1 {
		t.Fatalf("publications=%d want=1", len(pubs))
	}
	got := busDecodeResult(t, pubs[0])
	if got.Stdout != "boom" {
		t.Errorf("Stdout=%q want=boom", got.Stdout)
	}
}

func TestMountBus_AuthRequiredMissing(t *testing.T) {
	br := busBridge(t, &busFakeRunner{}, cmdsurface.DefaultPolicy())
	bus := newBusFakeBus()
	cleanup, err := cmdsurface.MountBus(br, bus, bus, []cmdsurface.BusBinding{{
		Path:          []string{"secret"},
		RequestTopic:  "cmd.secret.req",
		ResponseTopic: "cmd.secret.resp",
	}})
	if err != nil {
		t.Fatalf("MountBus: %v", err)
	}
	t.Cleanup(cleanup)

	bus.deliver(cmdsurface.BusMessage{Topic: "cmd.secret.req", Payload: []byte("{}")})

	pubs := bus.publicationsOn("cmd.secret.resp")
	if len(pubs) != 1 {
		t.Fatalf("publications=%d want=1", len(pubs))
	}
	code, _ := busDecodeError(t, pubs[0])
	if code != "unauthenticated" {
		t.Errorf("code=%q want=unauthenticated", code)
	}
}

func TestMountBus_ConfirmationRequiredMissing(t *testing.T) {
	br := busBridge(t, &busFakeRunner{}, cmdsurface.DefaultPolicy())
	bus := newBusFakeBus()
	cleanup, err := cmdsurface.MountBus(br, bus, bus, []cmdsurface.BusBinding{{
		Path:          []string{"confirm"},
		RequestTopic:  "cmd.confirm.req",
		ResponseTopic: "cmd.confirm.resp",
	}})
	if err != nil {
		t.Fatalf("MountBus: %v", err)
	}
	t.Cleanup(cleanup)

	bus.deliver(cmdsurface.BusMessage{Topic: "cmd.confirm.req", Payload: []byte("{}")})

	pubs := bus.publicationsOn("cmd.confirm.resp")
	if len(pubs) != 1 {
		t.Fatalf("publications=%d want=1", len(pubs))
	}
	code, _ := busDecodeError(t, pubs[0])
	if code != "confirmation_required" {
		t.Errorf("code=%q want=confirmation_required", code)
	}
}

func TestMountBus_JSONDecodeError(t *testing.T) {
	br := busBridge(t, &busFakeRunner{}, cmdsurface.DefaultPolicy())
	bus := newBusFakeBus()
	cleanup, err := cmdsurface.MountBus(br, bus, bus, []cmdsurface.BusBinding{{
		Path:          []string{"echo"},
		RequestTopic:  "cmd.echo.req",
		ResponseTopic: "cmd.echo.resp",
	}})
	if err != nil {
		t.Fatalf("MountBus: %v", err)
	}
	t.Cleanup(cleanup)

	bus.deliver(cmdsurface.BusMessage{
		Topic:   "cmd.echo.req",
		Payload: []byte("{not json"),
	})

	pubs := bus.publicationsOn("cmd.echo.resp")
	if len(pubs) != 1 {
		t.Fatalf("publications=%d want=1", len(pubs))
	}
	code, _ := busDecodeError(t, pubs[0])
	if code != "bad_request" {
		t.Errorf("code=%q want=bad_request", code)
	}
}

func TestMountBus_FireAndForget(t *testing.T) {
	runner := &busFakeRunner{}
	br := busBridge(t, runner, cmdsurface.DefaultPolicy())
	bus := newBusFakeBus()
	// pub MAY be nil when every binding is fire-and-forget. Pass nil
	// here so a regression that publishes despite empty ResponseTopic
	// would panic.
	cleanup, err := cmdsurface.MountBus(br, bus, nil, []cmdsurface.BusBinding{{
		Path:         []string{"echo"},
		RequestTopic: "cmd.echo.req",
		// ResponseTopic intentionally empty.
	}})
	if err != nil {
		t.Fatalf("MountBus: %v", err)
	}
	t.Cleanup(cleanup)

	payload, _ := json.Marshal(map[string]any{"args": []string{"x"}})
	bus.deliver(cmdsurface.BusMessage{Topic: "cmd.echo.req", Payload: payload})

	if got := bus.totalPublishes(); got != 0 {
		t.Errorf("Publish called %d times; want 0 (fire-and-forget)", got)
	}
	if runner.calls.Load() != 1 {
		t.Errorf("runner calls=%d want=1", runner.calls.Load())
	}
}

func TestMountBus_MetaSurfaceForced(t *testing.T) {
	runner := &busFakeRunner{}
	br := busBridge(t, runner, cmdsurface.DefaultPolicy())
	bus := newBusFakeBus()
	cleanup, err := cmdsurface.MountBus(br, bus, bus, []cmdsurface.BusBinding{{
		Path:          []string{"echo"},
		RequestTopic:  "cmd.echo.req",
		ResponseTopic: "cmd.echo.resp",
	}})
	if err != nil {
		t.Fatalf("MountBus: %v", err)
	}
	t.Cleanup(cleanup)

	// Caller lies in the envelope about Surface — handler must
	// overwrite it with SurfaceBus before invoking the bridge.
	payload, _ := json.Marshal(map[string]any{
		"meta": map[string]any{"surface": "cli", "caller": "u@1"},
	})
	bus.deliver(cmdsurface.BusMessage{Topic: "cmd.echo.req", Payload: payload})

	if got := runner.LastInvocation.Meta.Surface; got != cmdsurface.SurfaceBus {
		t.Errorf("runner saw Meta.Surface=%q want=%q", got, cmdsurface.SurfaceBus)
	}
	// Other Meta fields should be preserved.
	if got := runner.LastInvocation.Meta.Caller; got != "u@1" {
		t.Errorf("Meta.Caller=%q want=u@1", got)
	}
}

func TestMountBus_CleanupUnsubscribes(t *testing.T) {
	runner := &busFakeRunner{}
	br := busBridge(t, runner, cmdsurface.DefaultPolicy())
	bus := newBusFakeBus()
	cleanup, err := cmdsurface.MountBus(br, bus, bus, []cmdsurface.BusBinding{{
		Path:          []string{"echo"},
		RequestTopic:  "cmd.echo.req",
		ResponseTopic: "cmd.echo.resp",
	}})
	if err != nil {
		t.Fatalf("MountBus: %v", err)
	}

	// Confirm subscription is live before cleanup.
	if n := bus.deliver(cmdsurface.BusMessage{Topic: "cmd.echo.req", Payload: []byte("{}")}); n != 1 {
		t.Fatalf("pre-cleanup handlers=%d want=1", n)
	}
	preCalls := runner.calls.Load()

	cleanup()

	// After cleanup the subscription must be gone — deliver should
	// hit zero handlers.
	if n := bus.deliver(cmdsurface.BusMessage{Topic: "cmd.echo.req", Payload: []byte("{}")}); n != 0 {
		t.Errorf("post-cleanup handlers=%d want=0", n)
	}
	if got := runner.calls.Load(); got != preCalls {
		t.Errorf("runner calls after cleanup=%d want=%d (unchanged)", got, preCalls)
	}
}

func TestMountBus_ConcurrentMessages(t *testing.T) {
	const n = 10
	runner := &busFakeRunner{
		RunFn: func(_ context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
			// Sleep a touch so concurrent dispatch is observable.
			time.Sleep(5 * time.Millisecond)
			return cmdsurface.Result{Stdout: fmt.Sprintf("ok:%v", inv.Args)}, nil
		},
	}
	br := busBridge(t, runner, cmdsurface.DefaultPolicy())
	bus := newBusFakeBus()
	cleanup, err := cmdsurface.MountBus(br, bus, bus, []cmdsurface.BusBinding{{
		Path:          []string{"echo"},
		RequestTopic:  "cmd.echo.req",
		ResponseTopic: "cmd.echo.resp",
	}})
	if err != nil {
		t.Fatalf("MountBus: %v", err)
	}
	t.Cleanup(cleanup)

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			payload, _ := json.Marshal(map[string]any{
				"args": []string{fmt.Sprintf("msg-%d", i)},
			})
			bus.deliver(cmdsurface.BusMessage{Topic: "cmd.echo.req", Payload: payload})
		}(i)
	}
	wg.Wait()

	pubs := bus.publicationsOn("cmd.echo.resp")
	if len(pubs) != n {
		t.Fatalf("publications=%d want=%d", len(pubs), n)
	}
	if got := runner.calls.Load(); got != int64(n) {
		t.Errorf("runner calls=%d want=%d", got, n)
	}
	for _, pub := range pubs {
		res := busDecodeResult(t, pub)
		if res.Stdout == "" {
			t.Errorf("empty Stdout in publication: %+v", pub)
		}
	}
}

// --- supplementary coverage ---

// Auth + confirmation gates should pass when the matching header is
// present. Together with the missing-header tests above this proves
// the gates are decidable in both directions.
func TestMountBus_AuthAndConfirmPresent(t *testing.T) {
	runner := &busFakeRunner{}
	br := busBridge(t, runner, cmdsurface.DefaultPolicy())
	bus := newBusFakeBus()
	cleanup, err := cmdsurface.MountBus(br, bus, bus,
		[]cmdsurface.BusBinding{
			{
				Path:          []string{"secret"},
				RequestTopic:  "cmd.secret.req",
				ResponseTopic: "cmd.secret.resp",
			},
			{
				Path:          []string{"confirm"},
				RequestTopic:  "cmd.confirm.req",
				ResponseTopic: "cmd.confirm.resp",
			},
		},
	)
	if err != nil {
		t.Fatalf("MountBus: %v", err)
	}
	t.Cleanup(cleanup)

	bus.deliver(cmdsurface.BusMessage{
		Topic:   "cmd.secret.req",
		Payload: []byte("{}"),
		Headers: map[string]string{cmdsurface.BusHeaderAuthorization: "Bearer xyz"},
	})
	bus.deliver(cmdsurface.BusMessage{
		Topic:   "cmd.confirm.req",
		Payload: []byte("{}"),
		Headers: map[string]string{cmdsurface.BusHeaderConfirmToken: "yes"},
	})

	for _, topic := range []string{"cmd.secret.resp", "cmd.confirm.resp"} {
		pubs := bus.publicationsOn(topic)
		if len(pubs) != 1 {
			t.Fatalf("%s publications=%d want=1", topic, len(pubs))
		}
		// Should be a Result, not an error envelope.
		raw, _ := json.Marshal(pubs[0].Payload)
		var probe struct {
			Error *struct{} `json:"error"`
		}
		_ = json.Unmarshal(raw, &probe)
		if probe.Error != nil {
			t.Errorf("%s: expected Result, got error envelope (raw=%s)", topic, raw)
		}
	}
}

// MountBus must reject ResponseTopic + nil EventPublisher.
func TestMountBus_NilPublisherWithResponseTopic(t *testing.T) {
	br := busBridge(t, &busFakeRunner{}, cmdsurface.DefaultPolicy())
	bus := newBusFakeBus()
	_, err := cmdsurface.MountBus(br, bus, nil, []cmdsurface.BusBinding{{
		Path:          []string{"echo"},
		RequestTopic:  "cmd.echo.req",
		ResponseTopic: "cmd.echo.resp",
	}})
	if err == nil {
		t.Fatal("MountBus: expected error when ResponseTopic is set but publisher is nil")
	}
}

// MountBus(nil, ...) and MountBus(b, nil, ...) must report explicit
// errors before any allocation.
func TestMountBus_NilArgs(t *testing.T) {
	bus := newBusFakeBus()
	if _, err := cmdsurface.MountBus(nil, bus, bus, nil); err == nil {
		t.Error("MountBus(nil, sub, pub) = nil err; want error")
	}
	br := busBridge(t, &busFakeRunner{}, cmdsurface.DefaultPolicy())
	if _, err := cmdsurface.MountBus(br, nil, bus, nil); err == nil {
		t.Error("MountBus(b, nil, pub) = nil err; want error")
	}
}
