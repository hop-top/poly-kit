package bus

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// spySink records events and optionally returns an error.
type spySink struct {
	mu     sync.Mutex
	events []Event
	err    error
	closed bool
}

func (s *spySink) Drain(_ context.Context, e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return s.err
}

func (s *spySink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *spySink) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]Event, len(s.events))
	copy(cp, s.events)
	return cp
}

func TestTeeBus_DeliversToHandlersAndSinks(t *testing.T) {
	inner := New()
	sink := &spySink{}
	tb := NewTeeBus(inner, []Sink{sink})

	var got Event
	tb.Subscribe("app.start", func(_ context.Context, e Event) error {
		got = e
		return nil
	})

	ev := NewEvent("app.start", "test", nil)
	if err := tb.Publish(context.Background(), ev); err != nil {
		t.Fatalf("publish: %v", err)
	}

	if got.Topic != "app.start" {
		t.Errorf("handler got topic %q, want app.start", got.Topic)
	}
	if events := sink.Events(); len(events) != 1 {
		t.Fatalf("sink got %d events, want 1", len(events))
	} else if events[0].Topic != "app.start" {
		t.Errorf("sink got topic %q, want app.start", events[0].Topic)
	}
}

func TestTeeBus_SinkErrorDoesNotBlockHandler(t *testing.T) {
	inner := New()
	badSink := &spySink{err: errors.New("sink failed")}

	var errs []error
	tb := NewTeeBus(inner, []Sink{badSink}, func(err error) {
		errs = append(errs, err)
	})

	var handled bool
	tb.Subscribe("x.y", func(_ context.Context, _ Event) error {
		handled = true
		return nil
	})

	ev := NewEvent("x.y", "test", nil)
	if err := tb.Publish(context.Background(), ev); err != nil {
		t.Fatalf("publish should succeed despite sink error: %v", err)
	}
	if !handled {
		t.Error("handler was not called")
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 sink error, got %d", len(errs))
	}
}

func TestTeeBus_MultipleSinksAllReceive(t *testing.T) {
	inner := New()
	s1 := &spySink{}
	s2 := &spySink{}
	s3 := &spySink{}
	tb := NewTeeBus(inner, []Sink{s1, s2, s3})

	ev := NewEvent("multi.test", "test", "payload")
	if err := tb.Publish(context.Background(), ev); err != nil {
		t.Fatalf("publish: %v", err)
	}

	for i, s := range []*spySink{s1, s2, s3} {
		events := s.Events()
		if len(events) != 1 {
			t.Errorf("sink[%d]: got %d events, want 1", i, len(events))
		} else if events[0].Payload != "payload" {
			t.Errorf("sink[%d]: wrong payload", i)
		}
	}
}

func TestTeeBus_CloseClosesBusAndAllSinks(t *testing.T) {
	inner := New()
	s1 := &spySink{}
	s2 := &spySink{}
	tb := NewTeeBus(inner, []Sink{s1, s2})

	if err := tb.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Bus should be closed — publish should fail.
	ev := NewEvent("after.close", "test", nil)
	if err := tb.Publish(context.Background(), ev); err == nil {
		t.Error("expected error publishing after close")
	}

	if !s1.closed {
		t.Error("sink 1 not closed")
	}
	if !s2.closed {
		t.Error("sink 2 not closed")
	}
}

func TestTeeBus_SinkReceivesWildcardTopicEvents(t *testing.T) {
	inner := New()
	sink := &spySink{}
	tb := NewTeeBus(inner, []Sink{sink})

	// Subscribe with wildcard — handlers get the event.
	var count int
	tb.Subscribe("app.#", func(_ context.Context, _ Event) error {
		count++
		return nil
	})

	events := []Event{
		NewEvent("app.start", "test", nil),
		NewEvent("app.stop", "test", nil),
		NewEvent("app.request.begin", "test", nil),
		NewEvent("other.topic", "test", nil),
	}

	for _, ev := range events {
		if err := tb.Publish(context.Background(), ev); err != nil {
			t.Fatalf("publish %s: %v", ev.Topic, err)
		}
	}

	// Handler with wildcard got 3 matching events.
	if count != 3 {
		t.Errorf("handler got %d events, want 3", count)
	}

	// Sink receives ALL published events (no filtering).
	if got := sink.Events(); len(got) != 4 {
		t.Errorf("sink got %d events, want 4", len(got))
	}
}

// Verify TeeBus satisfies Bus interface at compile time.
var _ Bus = (*TeeBus)(nil)
