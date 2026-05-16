package notify

import (
	"context"
	"errors"
	"sync"
	"testing"

	"hop.top/kit/go/runtime/bus"
)

// countingSink records every event it sees, lets tests inject a
// Drain error, and tracks Close calls. Safe for concurrent Drain.
type countingSink struct {
	mu       sync.Mutex
	drained  []bus.Event
	closed   int
	drainErr error
}

func (s *countingSink) Drain(_ context.Context, e bus.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drained = append(s.drained, e)
	return s.drainErr
}

func (s *countingSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed++
	return nil
}

func (s *countingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.drained)
}

func evt(topic string, payload any) bus.Event {
	return bus.Event{
		Topic:   bus.Topic(topic),
		Source:  "test",
		Payload: payload,
	}
}

// sevPayload implements WithSeverity.
type sevPayload struct{ s Severity }

func (p sevPayload) Severity() Severity { return p.s }

func TestFilterSink_NoOptionsPassesEverything(t *testing.T) {
	t.Parallel()
	inner := &countingSink{}
	f := NewFilterSink(inner)
	events := []bus.Event{
		evt("a.b.c", nil),
		evt("x.y.z", sevPayload{SeverityDebug}),
		evt("kit.foo", map[string]any{"severity": "critical"}),
	}
	for _, e := range events {
		if err := f.Drain(context.Background(), e); err != nil {
			t.Fatalf("Drain returned err: %v", err)
		}
	}
	if got := inner.count(); got != len(events) {
		t.Fatalf("inner saw %d events, want %d", got, len(events))
	}
}

func TestFilterSink_TopicPatternExactMatch(t *testing.T) {
	t.Parallel()
	inner := &countingSink{}
	f := NewFilterSink(inner, WithTopicPattern("kit.runtime.entity.created"))
	pass := evt("kit.runtime.entity.created", nil)
	drop := evt("kit.runtime.entity.deleted", nil)
	_ = f.Drain(context.Background(), pass)
	_ = f.Drain(context.Background(), drop)
	if got := inner.count(); got != 1 {
		t.Fatalf("inner saw %d events, want 1", got)
	}
	if inner.drained[0].Topic != pass.Topic {
		t.Fatalf("inner saw %q, want %q", inner.drained[0].Topic, pass.Topic)
	}
}

func TestFilterSink_TopicPatternStarWildcard(t *testing.T) {
	t.Parallel()
	inner := &countingSink{}
	// "*" matches exactly one segment.
	f := NewFilterSink(inner, WithTopicPattern("kit.*.created"))
	pass1 := evt("kit.entity.created", nil)
	pass2 := evt("kit.task.created", nil)
	drop1 := evt("kit.runtime.entity.created", nil) // too many segments
	drop2 := evt("kit.created", nil)                // too few
	for _, e := range []bus.Event{pass1, pass2, drop1, drop2} {
		_ = f.Drain(context.Background(), e)
	}
	if got := inner.count(); got != 2 {
		t.Fatalf("inner saw %d events, want 2", got)
	}
}

func TestFilterSink_TopicPatternHashWildcard(t *testing.T) {
	t.Parallel()
	inner := &countingSink{}
	// "#" matches zero or more trailing segments (must be last).
	f := NewFilterSink(inner, WithTopicPattern("kit.runtime.#"))
	pass := []bus.Event{
		evt("kit.runtime", nil),
		evt("kit.runtime.entity", nil),
		evt("kit.runtime.entity.created", nil),
	}
	drop := evt("kit.core.entity.created", nil)
	for _, e := range pass {
		_ = f.Drain(context.Background(), e)
	}
	_ = f.Drain(context.Background(), drop)
	if got := inner.count(); got != len(pass) {
		t.Fatalf("inner saw %d events, want %d", got, len(pass))
	}
}

func TestFilterSink_MinSeverityFloor(t *testing.T) {
	t.Parallel()
	inner := &countingSink{}
	f := NewFilterSink(inner, WithMinSeverity(SeverityWarn))

	cases := []struct {
		name string
		ev   bus.Event
		pass bool
	}{
		{"critical", evt("t", sevPayload{SeverityCritical}), true},
		{"error", evt("t", sevPayload{SeverityError}), true},
		{"warn", evt("t", sevPayload{SeverityWarn}), true},
		{"info", evt("t", sevPayload{SeverityInfo}), false},
		{"debug", evt("t", sevPayload{SeverityDebug}), false},
		// no-payload severity defaults to Info → dropped when min=Warn.
		{"default-info-no-payload", evt("t", nil), false},
	}
	wantPass := 0
	for _, c := range cases {
		_ = f.Drain(context.Background(), c.ev)
		if c.pass {
			wantPass++
		}
	}
	if got := inner.count(); got != wantPass {
		t.Fatalf("inner saw %d events, want %d", got, wantPass)
	}
}

func TestFilterSink_Predicate(t *testing.T) {
	t.Parallel()
	inner := &countingSink{}
	f := NewFilterSink(inner, WithPredicate(func(e bus.Event) bool {
		return e.Source == "allowed"
	}))
	pass := evt("t", nil)
	pass.Source = "allowed"
	drop := evt("t", nil)
	drop.Source = "denied"

	_ = f.Drain(context.Background(), pass)
	_ = f.Drain(context.Background(), drop)
	if got := inner.count(); got != 1 {
		t.Fatalf("inner saw %d events, want 1", got)
	}
}

func TestFilterSink_NilPredicateIgnored(t *testing.T) {
	t.Parallel()
	inner := &countingSink{}
	// nil predicate should not blow up nor reject anything.
	f := NewFilterSink(inner, WithPredicate(nil))
	_ = f.Drain(context.Background(), evt("t", nil))
	if got := inner.count(); got != 1 {
		t.Fatalf("inner saw %d events, want 1", got)
	}
}

func TestFilterSink_AllOptionsCompose(t *testing.T) {
	t.Parallel()
	inner := &countingSink{}
	f := NewFilterSink(
		inner,
		WithTopicPattern("billing.#"),
		WithMinSeverity(SeverityWarn),
		WithPredicate(func(e bus.Event) bool {
			return e.Source == "billing-svc"
		}),
	)

	cases := []struct {
		name string
		ev   bus.Event
		pass bool
	}{
		{
			name: "all-pass",
			ev: bus.Event{
				Topic:   "billing.invoice.failed",
				Source:  "billing-svc",
				Payload: sevPayload{SeverityError},
			},
			pass: true,
		},
		{
			name: "topic-fails",
			ev: bus.Event{
				Topic:   "shipping.label.created",
				Source:  "billing-svc",
				Payload: sevPayload{SeverityError},
			},
			pass: false,
		},
		{
			name: "severity-fails",
			ev: bus.Event{
				Topic:   "billing.invoice.created",
				Source:  "billing-svc",
				Payload: sevPayload{SeverityInfo},
			},
			pass: false,
		},
		{
			name: "predicate-fails",
			ev: bus.Event{
				Topic:   "billing.invoice.failed",
				Source:  "ops-cli",
				Payload: sevPayload{SeverityError},
			},
			pass: false,
		},
	}
	wantPass := 0
	for _, c := range cases {
		_ = f.Drain(context.Background(), c.ev)
		if c.pass {
			wantPass++
		}
	}
	if got := inner.count(); got != wantPass {
		t.Fatalf("inner saw %d events, want %d", got, wantPass)
	}
}

func TestFilterSink_InnerErrorPropagated(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("inner failed")
	inner := &countingSink{drainErr: wantErr}
	f := NewFilterSink(inner)
	err := f.Drain(context.Background(), evt("t", nil))
	if !errors.Is(err, wantErr) {
		t.Fatalf("Drain returned %v, want %v", err, wantErr)
	}
}

func TestFilterSink_FilteredEventDoesNotCallInner(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("inner should not be called")
	inner := &countingSink{drainErr: wantErr}
	f := NewFilterSink(inner, WithMinSeverity(SeverityCritical))
	// SeverityInfo (default) is below Critical → filtered.
	err := f.Drain(context.Background(), evt("t", nil))
	if err != nil {
		t.Fatalf("Drain returned %v, want nil for filtered event", err)
	}
	if inner.count() != 0 {
		t.Fatalf("inner saw %d events, want 0", inner.count())
	}
}

func TestFilterSink_Composition_FilterWrapsFilter(t *testing.T) {
	t.Parallel()
	inner := &countingSink{}
	// outer requires topic match; inner requires severity floor.
	pipeline := NewFilterSink(
		NewFilterSink(inner, WithMinSeverity(SeverityWarn)),
		WithTopicPattern("kit.#"),
	)

	cases := []struct {
		ev   bus.Event
		pass bool
	}{
		{
			ev:   bus.Event{Topic: "kit.foo", Payload: sevPayload{SeverityCritical}},
			pass: true,
		},
		{
			// topic ok, severity below floor
			ev:   bus.Event{Topic: "kit.bar", Payload: sevPayload{SeverityInfo}},
			pass: false,
		},
		{
			// severity ok, topic miss
			ev:   bus.Event{Topic: "other.bar", Payload: sevPayload{SeverityCritical}},
			pass: false,
		},
	}
	wantPass := 0
	for _, c := range cases {
		_ = pipeline.Drain(context.Background(), c.ev)
		if c.pass {
			wantPass++
		}
	}
	if got := inner.count(); got != wantPass {
		t.Fatalf("inner saw %d events, want %d", got, wantPass)
	}
}

func TestFilterSink_CloseDelegates(t *testing.T) {
	t.Parallel()
	inner := &countingSink{}
	f := NewFilterSink(inner)
	if err := f.Close(); err != nil {
		t.Fatalf("Close returned %v", err)
	}
	if inner.closed != 1 {
		t.Fatalf("inner closed %d times, want 1", inner.closed)
	}
}

func TestFilterSink_ImplementsBusSink(t *testing.T) {
	t.Parallel()
	// compile-time assertion belongs here; the var declaration in
	// filter.go is fine but we double-check at runtime to keep test
	// failures clear if someone breaks the interface.
	var _ bus.Sink = (*FilterSink)(nil)
}
