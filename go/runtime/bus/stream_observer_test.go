package bus

import (
	"context"
	"errors"
	"testing"
)

func TestObservePublished_OrderAndVeto(t *testing.T) {
	b := New()
	po := publishObserverOf(b)
	if po == nil {
		t.Fatal("memory bus must expose the publish observer hook")
	}
	var got []string
	unsub := po.ObservePublished(func(_ context.Context, e Event) {
		got = append(got, string(e.Topic))
	})
	// A sync handler registered AFTER the observer still vetoes first.
	b.Subscribe("a.b.c.vetoed", func(context.Context, Event) error {
		return errors.New("no")
	})
	for _, topic := range []string{"a.b.c.one", "a.b.c.vetoed", "a.b.c.two", "a.b.c.three"} {
		_ = b.Publish(context.Background(), NewEvent(Topic(topic), "t", nil))
	}
	want := []string{"a.b.c.one", "a.b.c.two", "a.b.c.three"}
	if len(got) != len(want) {
		t.Fatalf("observed %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("observed %v, want %v", got, want)
		}
	}
	unsub()
	_ = b.Publish(context.Background(), NewEvent("a.b.c.four", "t", nil))
	if len(got) != 3 {
		t.Fatalf("observer ran after unsubscribe: %v", got)
	}
}

func TestPublishObserverOf_UnwrapsKitWrappers(t *testing.T) {
	cases := map[string]Bus{
		"memory":         New(),
		"memory adapter": New(WithAdapter(NewMemoryAdapter())),
		"tee":            NewTeeBus(New(), nil),
		"networked":      &networkedBus{Bus: New()},
	}
	for name, b := range cases {
		if publishObserverOf(b) == nil {
			t.Errorf("%s: no publish observer found", name)
		}
	}
	if publishObserverOf(noHookBus{}) != nil {
		t.Error("a bus without the hook must report none")
	}
}

type noHookBus struct{}

func (noHookBus) Publish(context.Context, Event) error            { return nil }
func (noHookBus) Subscribe(string, Handler) Unsubscribe           { return func() {} }
func (noHookBus) SubscribeAsync(string, AsyncHandler) Unsubscribe { return func() {} }
func (noHookBus) Close(context.Context) error                     { return nil }
