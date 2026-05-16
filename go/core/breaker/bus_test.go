package breaker_test

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	charmlog "charm.land/log/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/breaker"
)

// recPub is a thread-safe in-memory EventPublisher used to assert on
// the events the breaker emits. Bus events arrive on background
// goroutines (matching the kit fire-and-forget pattern), so tests
// poll snapshot()/findTopic() via waitForTopic / waitForCount.
type recPub struct {
	mu     sync.Mutex
	events []recEvent
}

type recEvent struct {
	Topic   string
	Source  string
	Payload any
}

func (r *recPub) Publish(_ context.Context, topic, source string, payload any) error {
	r.mu.Lock()
	r.events = append(r.events, recEvent{Topic: topic, Source: source, Payload: payload})
	r.mu.Unlock()
	return nil
}

func (r *recPub) snapshot() []recEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recEvent, len(r.events))
	copy(out, r.events)
	return out
}

// findTopic returns the first event matching topic, or zero+false.
func (r *recPub) findTopic(topic string) (recEvent, bool) {
	for _, e := range r.snapshot() {
		if e.Topic == topic {
			return e, true
		}
	}
	return recEvent{}, false
}

func newBusCaptureLogger() (*charmlog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	l := charmlog.NewWithOptions(buf, charmlog.Options{
		Level:     charmlog.DebugLevel,
		Formatter: charmlog.JSONFormatter,
	})
	return l, buf
}

// TestBus_NilPublisher_LogOnlyNoBusEvent asserts the default opt-out
// path: without WithPublisher, manual Trip + auto transitions still
// log but never publish.
func TestBus_NilPublisher_LogOnlyNoBusEvent(t *testing.T) {
	const name = "test-bus-nil-pub"
	t.Cleanup(func() { breaker.Unregister(name) })

	lg, buf := newBusCaptureLogger()
	b := breaker.New(name,
		breaker.Logger(lg),
		breaker.WithCircuit(breaker.CircuitOpts{
			FailureThreshold: 1,
			SuccessThreshold: 1,
			Delay:            5 * time.Millisecond,
		}),
	)

	b.Trip("nil-pub-test")
	require.Equal(t, breaker.Open, b.State())
	require.NotEmpty(t, buf.String(), "log channel must still fire")
	// No publisher attached: nothing else to assert beyond log presence;
	// the absence of a panic + logged content confirms behavior is
	// preserved.
	assert.Contains(t, buf.String(), "nil-pub-test")
}

// TestBus_PublisherSet_TripEmitsLogAndBus asserts manual Trip emits
// both channels: log line + TrippedPayload bus event on the Tripped
// topic.
func TestBus_PublisherSet_TripEmitsLogAndBus(t *testing.T) {
	const name = "test-bus-trip"
	t.Cleanup(func() { breaker.Unregister(name) })

	pub := &recPub{}
	lg, buf := newBusCaptureLogger()

	b := breaker.New(name,
		breaker.Logger(lg),
		breaker.WithPublisher(pub),
	)

	// Trip → 1 manual TrippedPayload + 1 auto Opened TransitionPayload
	b.Trip("manual-test")
	waitForCount(t, pub, 2)

	// log channel preserved
	assert.Contains(t, buf.String(), "manual-test")

	tripped, ok := pub.findTopic(string(breaker.DefaultTopics.Tripped))
	require.True(t, ok, "expected Tripped event; got %+v", pub.snapshot())
	require.IsType(t, breaker.TrippedPayload{}, tripped.Payload)
	tp := tripped.Payload.(breaker.TrippedPayload)
	assert.Equal(t, "manual-test", tp.Reason)
	assert.Equal(t, breaker.Closed, tp.From)
	assert.Equal(t, breaker.Open, tp.To)

	opened, ok := pub.findTopic(string(breaker.DefaultTopics.Opened))
	require.True(t, ok, "expected Opened event; got %+v", pub.snapshot())
	require.IsType(t, breaker.TransitionPayload{}, opened.Payload)
	op := opened.Payload.(breaker.TransitionPayload)
	assert.Equal(t, breaker.Closed, op.From)
	assert.Equal(t, breaker.Open, op.To)
	assert.Equal(t, "auto", op.Trigger)
}

// TestBus_AutoTransitions_OpenedClosedHalfOpened drives the underlying
// circuit through Closed → Open → HalfOpen → Closed and asserts each
// transition publishes the matching topic with payload shape.
//
// SuccessThreshold is set to 2 so the first probe success in HalfOpen
// does NOT immediately close — that lets us assert the HalfOpened
// event independently from the Closed event. waitForTopic gives the
// goroutine publish path time to land.
func TestBus_AutoTransitions_OpenedClosedHalfOpened(t *testing.T) {
	const name = "test-bus-auto"
	t.Cleanup(func() { breaker.Unregister(name) })

	pub := &recPub{}
	b := breaker.New(name,
		breaker.WithPublisher(pub),
		breaker.WithCircuit(breaker.CircuitOpts{
			FailureThreshold: 1,
			SuccessThreshold: 2,
			Delay:            10 * time.Millisecond,
		}),
	)

	// Closed → Open (failure threshold reached)
	b.Record(false, 0)
	require.Equal(t, breaker.Open, b.State())
	waitForTopic(t, pub, string(breaker.DefaultTopics.Opened))

	opened, ok := pub.findTopic(string(breaker.DefaultTopics.Opened))
	require.True(t, ok)
	require.IsType(t, breaker.TransitionPayload{}, opened.Payload)
	op := opened.Payload.(breaker.TransitionPayload)
	assert.Equal(t, breaker.Closed, op.From)
	assert.Equal(t, breaker.Open, op.To)
	assert.Equal(t, "auto", op.Trigger)

	// Open → HalfOpen (delay elapsed; Allow drives a probe through cb)
	time.Sleep(20 * time.Millisecond)
	_ = b.Allow()
	require.Equal(t, breaker.HalfOpen, b.State())
	waitForTopic(t, pub, string(breaker.DefaultTopics.HalfOpened))

	half, ok := pub.findTopic(string(breaker.DefaultTopics.HalfOpened))
	require.True(t, ok)
	require.IsType(t, breaker.TransitionPayload{}, half.Payload)
	hp := half.Payload.(breaker.TransitionPayload)
	assert.Equal(t, breaker.Open, hp.From)
	assert.Equal(t, breaker.HalfOpen, hp.To)
	assert.Equal(t, "auto", hp.Trigger)

	// HalfOpen → Closed (need 2 successes to satisfy SuccessThreshold)
	b.Record(true, 0)
	b.Record(true, 0)
	require.Equal(t, breaker.Closed, b.State())
	waitForTopic(t, pub, string(breaker.DefaultTopics.Closed))

	closed, ok := pub.findTopic(string(breaker.DefaultTopics.Closed))
	require.True(t, ok)
	require.IsType(t, breaker.TransitionPayload{}, closed.Payload)
	cp := closed.Payload.(breaker.TransitionPayload)
	assert.Equal(t, breaker.HalfOpen, cp.From)
	assert.Equal(t, breaker.Closed, cp.To)
	assert.Equal(t, "auto", cp.Trigger)
}

// waitForTopic polls the recorder until topic appears or times out.
// Used in flows where the number of intermediate publishes is hard
// to predict in advance.
func waitForTopic(t *testing.T, pub *recPub, topic string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := pub.findTopic(topic); ok {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for topic %q; got %+v", topic, pub.snapshot())
}

// waitForCount polls until the recorder has at least n events.
func waitForCount(t *testing.T, pub *recPub, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(pub.snapshot()) >= n {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d events; got %d: %+v", n, len(pub.snapshot()), pub.snapshot())
}

// TestBus_WithTopicPrefix overrides all four topics from a 3-segment
// prefix; events must publish on the prefixed topics, not the kit
// defaults.
func TestBus_WithTopicPrefix(t *testing.T) {
	const name = "test-bus-prefix"
	t.Cleanup(func() { breaker.Unregister(name) })

	pub := &recPub{}
	b := breaker.New(name,
		breaker.WithPublisher(pub),
		breaker.WithTopicPrefix("myapp.core.breaker"),
	)

	b.Trip("prefix-test")
	waitForCount(t, pub, 2) // Tripped + Opened

	_, ok := pub.findTopic("myapp.core.breaker.tripped")
	assert.True(t, ok, "expected prefixed Tripped topic; got %+v", pub.snapshot())
	_, ok = pub.findTopic("myapp.core.breaker.opened")
	assert.True(t, ok, "expected prefixed Opened topic; got %+v", pub.snapshot())

	// kit defaults must NOT have been used
	_, ok = pub.findTopic(string(breaker.DefaultTopics.Tripped))
	assert.False(t, ok, "default Tripped topic must not appear when prefix overrides it")
}

// TestBus_WithTopicPrefix_InvalidPanics covers the fail-loud
// constructor contract.
func TestBus_WithTopicPrefix_InvalidPanics(t *testing.T) {
	// 4-segment prefix is invalid: PrefixTopics requires 3 segments
	assert.Panics(t, func() {
		breaker.WithTopicPrefix("too.many.segments.here")
	})
	// empty prefix
	assert.Panics(t, func() {
		breaker.WithTopicPrefix("")
	})
	// uppercase segment fails validSegment
	assert.Panics(t, func() {
		breaker.WithTopicPrefix("MyApp.core.breaker")
	})
}

// TestBus_WithTopics_PartialOverride asserts an explicit Topics with
// only one field set keeps DefaultTopics for the remaining fields.
func TestBus_WithTopics_PartialOverride(t *testing.T) {
	const name = "test-bus-topics-partial"
	t.Cleanup(func() { breaker.Unregister(name) })

	pub := &recPub{}
	b := breaker.New(name,
		breaker.WithPublisher(pub),
		breaker.WithTopics(breaker.Topics{
			Tripped: "x.y.z.tripped",
		}),
	)

	b.Trip("partial-override")
	waitForCount(t, pub, 2) // Tripped (overridden) + Opened (default)

	_, ok := pub.findTopic("x.y.z.tripped")
	assert.True(t, ok, "overridden Tripped topic must be used")

	_, ok = pub.findTopic(string(breaker.DefaultTopics.Opened))
	assert.True(t, ok, "non-overridden Opened topic must fall back to default")

	// the kit-default Tripped MUST NOT appear
	_, ok = pub.findTopic(string(breaker.DefaultTopics.Tripped))
	assert.False(t, ok)
}

// TestBus_DefaultTopicsShape asserts the published topic strings
// match the kit baseline; freezes the default contract for adopters.
func TestBus_DefaultTopicsShape(t *testing.T) {
	assert.Equal(t, "kit.core.breaker.tripped", string(breaker.DefaultTopics.Tripped))
	assert.Equal(t, "kit.core.breaker.opened", string(breaker.DefaultTopics.Opened))
	assert.Equal(t, "kit.core.breaker.closed", string(breaker.DefaultTopics.Closed))
	assert.Equal(t, "kit.core.breaker.half_opened", string(breaker.DefaultTopics.HalfOpened))
}
