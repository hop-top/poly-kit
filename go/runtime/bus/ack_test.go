package bus

import (
	"context"
	"errors"
	"testing"
	"time"
)

func localAcked(ctx context.Context, b Bus, topic Topic) <-chan ackOutcome {
	ch := make(chan ackOutcome, 1)
	go func() {
		a, err := PublishAcked(ctx, b, NewEvent(topic, "test", nil))
		ch <- ackOutcome{a, err}
	}()
	return ch
}

func TestPublishAcked_WaitsForAsyncHandlers(t *testing.T) {
	for name, b := range map[string]Bus{
		"memory":         New(),
		"memory adapter": New(WithAdapter(NewMemoryAdapter())),
		"tee":            NewTeeBus(New(), nil),
	} {
		t.Run(name, func(t *testing.T) {
			release := newGate(t)
			b.SubscribeAsync("acme.#", func(context.Context, Event) { <-release.ch })
			b.SubscribeAsync("acme.#", func(context.Context, Event) {})
			ch := localAcked(context.Background(), b, "acme.crm.deal.created")
			pendingAck(t, ch)
			release.open()
			o := waitAck(t, ch)
			if o.err != nil || o.ack.ID == "" || len(o.ack.Peers) != 0 {
				t.Fatalf("ack = %+v, err = %v", o.ack, o.err)
			}
		})
	}
}

func TestPublishAcked_VetoIsReturned(t *testing.T) {
	b := New()
	veto := errors.New("veto")
	b.Subscribe("acme.#", func(context.Context, Event) error { return veto })
	if o := waitAck(t, localAcked(context.Background(), b, "acme.x")); !errors.Is(o.err, veto) {
		t.Fatalf("err = %v", o.err)
	}
}

func TestPublishAcked_EventsPublishedByHandlersAreNotAwaited(t *testing.T) {
	b := New()
	release := newGate(t)
	b.SubscribeAsync("acme.child", func(context.Context, Event) { <-release.ch })
	b.SubscribeAsync("acme.parent", func(ctx context.Context, _ Event) {
		_ = b.Publish(ctx, NewEvent("acme.child", "test", nil))
	})
	b.Subscribe("acme.parent", func(ctx context.Context, _ Event) error {
		return b.Publish(ctx, NewEvent("acme.child", "test", nil))
	})
	if o := waitAck(t, localAcked(context.Background(), b, "acme.parent")); o.err != nil {
		t.Fatal(o.err)
	}
}

func TestPublishAcked_LocalTimeout(t *testing.T) {
	b := New()
	release := newGate(t)
	b.SubscribeAsync("acme.#", func(context.Context, Event) { <-release.ch })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	o := waitAck(t, localAcked(ctx, b, "acme.x"))
	var ae *AckError
	if !errors.As(o.err, &ae) || ae.Code != AckTimeout || ae.Peer != "" {
		t.Fatalf("err = %v", o.err)
	}
}

type countSink struct{ n int }

func (s *countSink) Drain(context.Context, Event) error { s.n++; return nil }
func (s *countSink) Close() error                       { return nil }

func TestPublishAcked_TeeDrainsSinksOnlyWhenPublished(t *testing.T) {
	inner := New()
	sink := &countSink{}
	tee := NewTeeBus(inner, []Sink{sink})
	inner.Subscribe("acme.veto", func(context.Context, Event) error { return errors.New("no") })
	waitAck(t, localAcked(context.Background(), tee, "acme.ok"))
	waitAck(t, localAcked(context.Background(), tee, "acme.veto"))
	if sink.n != 1 {
		t.Fatalf("sink drained %d events, want 1", sink.n)
	}
}

func TestPublishAcked_UnsupportedBus(t *testing.T) {
	if _, err := PublishAcked(context.Background(), struct{ Bus }{New()}, NewEvent("acme.x", "t", nil)); !errors.Is(err, ErrAcksUnsupported) {
		t.Fatalf("err = %v", err)
	}
}
