package cmdsurface

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// sinkFakeBus is a test api.EventPublisher that records every
// Publish call.
type sinkFakeBus struct {
	mu      sync.Mutex
	calls   []sinkFakeBusCall
	wantErr error
}

type sinkFakeBusCall struct {
	topic   string
	source  string
	payload any
}

func (f *sinkFakeBus) Publish(_ context.Context, topic, source string, payload any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, sinkFakeBusCall{topic, source, payload})
	return f.wantErr
}

func (f *sinkFakeBus) latest() sinkFakeBusCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

func TestBusSink_PublishesEnvelopeToTopic(t *testing.T) {
	bus := &sinkFakeBus{}
	sink := &BusSink{Publisher: bus, Topic: "cmd.widget.add"}
	inv := Invocation{Path: []string{"widget", "add"}, Meta: Meta{Surface: SurfaceREST}}
	if err := sink.Emit(context.Background(), inv, Result{ExitCode: 0}, nil); err != nil {
		t.Fatalf("Emit err: %v", err)
	}
	if len(bus.calls) != 1 {
		t.Fatalf("got %d publishes, want 1", len(bus.calls))
	}
	c := bus.latest()
	if c.topic != "cmd.widget.add" {
		t.Errorf("topic=%q", c.topic)
	}
	if c.source != "cmdsurface" {
		t.Errorf("source=%q, want cmdsurface", c.source)
	}
	env, ok := c.payload.(busSinkEnvelope)
	if !ok {
		t.Fatalf("payload type=%T, want busSinkEnvelope", c.payload)
	}
	if got := env.Invocation.Path; len(got) != 2 || got[0] != "widget" {
		t.Errorf("invocation.path=%v", got)
	}
	if env.Error != nil {
		t.Errorf("error=%v, want nil", *env.Error)
	}
}

func TestBusSink_ErrorPropagatesIntoEnvelope(t *testing.T) {
	bus := &sinkFakeBus{}
	sink := &BusSink{Publisher: bus, Topic: "t"}
	_ = sink.Emit(context.Background(), Invocation{}, Result{ExitCode: 7}, errors.New("kaboom"))
	env := bus.latest().payload.(busSinkEnvelope)
	if env.Error == nil || *env.Error != "kaboom" {
		t.Errorf("error=%v", env.Error)
	}
	if env.Result.ExitCode != 7 {
		t.Errorf("exit_code=%d", env.Result.ExitCode)
	}
}

func TestBusSink_TopicFnOverridesTopic(t *testing.T) {
	bus := &sinkFakeBus{}
	sink := &BusSink{
		Publisher: bus,
		Topic:     "fallback",
		TopicFn: func(inv Invocation) string {
			return "cmd." + joinPath(inv.Path)
		},
	}
	if err := sink.Emit(context.Background(), Invocation{Path: []string{"a", "b"}}, Result{}, nil); err != nil {
		t.Fatalf("Emit err: %v", err)
	}
	if got := bus.latest().topic; got != "cmd.a b" {
		t.Errorf("topic=%q", got)
	}
}

func TestBusSink_TopicFnEmptyFallsBackToTopic(t *testing.T) {
	bus := &sinkFakeBus{}
	sink := &BusSink{
		Publisher: bus,
		Topic:     "fallback",
		TopicFn:   func(_ Invocation) string { return "" },
	}
	if err := sink.Emit(context.Background(), Invocation{Path: []string{"x"}}, Result{}, nil); err != nil {
		t.Fatalf("Emit err: %v", err)
	}
	if got := bus.latest().topic; got != "fallback" {
		t.Errorf("topic=%q", got)
	}
}

func TestBusSink_CustomSource(t *testing.T) {
	bus := &sinkFakeBus{}
	sink := &BusSink{Publisher: bus, Topic: "t", Source: "audit"}
	_ = sink.Emit(context.Background(), Invocation{}, Result{}, nil)
	if got := bus.latest().source; got != "audit" {
		t.Errorf("source=%q", got)
	}
}

func TestBusSink_NilPublisherError(t *testing.T) {
	sink := &BusSink{Topic: "t"}
	if err := sink.Emit(context.Background(), Invocation{}, Result{}, nil); !errors.Is(err, ErrBusSinkNoPublisher) {
		t.Fatalf("got %v, want ErrBusSinkNoPublisher", err)
	}
}

func TestBusSink_EmptyTopicError(t *testing.T) {
	sink := &BusSink{Publisher: &sinkFakeBus{}}
	if err := sink.Emit(context.Background(), Invocation{}, Result{}, nil); !errors.Is(err, ErrBusSinkNoTopic) {
		t.Fatalf("got %v, want ErrBusSinkNoTopic", err)
	}
}

func TestBusSink_PublisherErrorPropagated(t *testing.T) {
	want := errors.New("bus down")
	sink := &BusSink{Publisher: &sinkFakeBus{wantErr: want}, Topic: "t"}
	if err := sink.Emit(context.Background(), Invocation{}, Result{}, nil); !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}
