package sideeffect_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/sideeffect"
)

// recordingPublisher captures every Publish call.
type recordingPublisher struct {
	mu     sync.Mutex
	topics []string
	srcs   []string
	loads  []any
}

func (r *recordingPublisher) Publish(_ context.Context, topic, source string, payload any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.topics = append(r.topics, topic)
	r.srcs = append(r.srcs, source)
	r.loads = append(r.loads, payload)
	return nil
}

func (r *recordingPublisher) lastPayload() any {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.loads) == 0 {
		return nil
	}
	return r.loads[len(r.loads)-1]
}

type qualifiedPayload struct {
	bus.Qualifiers
	ID string
}

type plainPayload struct {
	ID string
}

func TestDryRunPublisher_PassthroughWhenNotDryRun(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	p := sideeffect.NewDryRunPublisher(rec)
	payload := &qualifiedPayload{ID: "x"}
	if err := p.Publish(context.Background(), "k.r.e.created", "src", payload); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if payload.Mechanism != "" {
		t.Fatalf("Mechanism must not be set when ctx is not dry-run; got %q",
			payload.Mechanism)
	}
}

func TestDryRunPublisher_TagsWhenDryRun(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	p := sideeffect.NewDryRunPublisher(rec)
	ctx := sideeffect.WithDryRun(context.Background(), true)
	payload := &qualifiedPayload{ID: "x"}
	if err := p.Publish(ctx, "k.r.e.created", "src", payload); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if payload.Mechanism != sideeffect.DryRunMechanism {
		t.Fatalf("Mechanism: got %q want %q",
			payload.Mechanism, sideeffect.DryRunMechanism)
	}
	if last, ok := rec.lastPayload().(*qualifiedPayload); !ok || last != payload {
		t.Fatalf("delegated payload identity changed: got %T", rec.lastPayload())
	}
}

func TestDryRunPublisher_PreservesExistingMechanism(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	p := sideeffect.NewDryRunPublisher(rec)
	ctx := sideeffect.WithDryRun(context.Background(), true)
	payload := &qualifiedPayload{
		Qualifiers: bus.Qualifiers{Mechanism: "signal"},
		ID:         "x",
	}
	if err := p.Publish(ctx, "k.r.e.created", "src", payload); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if payload.Mechanism != "signal" {
		t.Fatalf("existing Mechanism overwritten; got %q", payload.Mechanism)
	}
}

func TestDryRunPublisher_PayloadWithoutQualifiers(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	p := sideeffect.NewDryRunPublisher(rec)
	ctx := sideeffect.WithDryRun(context.Background(), true)
	payload := &plainPayload{ID: "x"}
	if err := p.Publish(ctx, "k.r.e.created", "src", payload); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// Plain payload published unchanged.
	if last, ok := rec.lastPayload().(*plainPayload); !ok || last != payload {
		t.Fatalf("delegated payload changed: %T", rec.lastPayload())
	}
}

func TestDryRunPublisher_NilInnerErr(t *testing.T) {
	t.Parallel()
	p := sideeffect.NewDryRunPublisher(nil)
	err := p.Publish(context.Background(), "k.r.e.created", "src", nil)
	if !errors.Is(err, sideeffect.ErrNilInner) {
		t.Fatalf("want ErrNilInner, got %v", err)
	}
}

func TestDryRunPublisher_ValuePayloadIsBestEffort(t *testing.T) {
	t.Parallel()
	// A payload passed by value cannot be augmented because the
	// reflected struct is non-addressable. The publisher MUST NOT
	// panic and MUST NOT clone-and-augment (see ADR-0019). It just
	// delegates the value unchanged.
	rec := &recordingPublisher{}
	p := sideeffect.NewDryRunPublisher(rec)
	ctx := sideeffect.WithDryRun(context.Background(), true)
	payload := qualifiedPayload{ID: "v"} // value, not pointer
	if err := p.Publish(ctx, "k.r.e.created", "src", payload); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got, ok := rec.lastPayload().(qualifiedPayload)
	if !ok {
		t.Fatalf("expected qualifiedPayload value; got %T", rec.lastPayload())
	}
	if got.Mechanism != "" {
		t.Fatalf("value payload should not be mutated; got Mechanism=%q",
			got.Mechanism)
	}
}
